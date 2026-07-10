// Package claude is the Claude Code adapter: it translates ~/.claude
// session JSONL into canonical records, capturing the fields v1 dropped —
// message.usage (real tokens), message.model, parentUuid, isSidechain,
// requestId (docs/v2-plan.md §5.3, §6).
//
// This covers the session transcript source. The sidecar sources (plans,
// todos, tasks, shell snapshots, paste cache, memories, file history,
// usage facets, history.jsonl) land as follow-up artifact emitters on the
// same Discover/Parse skeleton.
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

// Slug identifies this adapter.
const Slug = canon.AgentSlug("claude-code")

const (
	maxLineBytes     = 10 * 1024 * 1024
	titleLimit       = 200
	resultExcerptCap = 400
)

// Adapter implements agent.Adapter for Claude Code.
type Adapter struct{}

// New returns the Claude Code adapter.
func New() *Adapter { return &Adapter{} }

// Slug implements agent.Adapter.
func (*Adapter) Slug() canon.AgentSlug { return Slug }

// RootSpec implements agent.Adapter: Claude Code relocates its data dir
// via CLAUDE_CONFIG_DIR.
func (*Adapter) RootSpec() agent.RootSpec {
	return agent.RootSpec{
		EnvVars:  []string{"CLAUDE_CONFIG_DIR"},
		Defaults: []string{"~/.claude"},
	}
}

// Discover enumerates all indexable sources under a root: session JSONL
// files plus every sidecar kind (plans, snapshots, pastes, todos, tasks,
// memories, file history, usage data, history.jsonl). Missing
// subdirectories are normal (fresh installs) and yield an empty result.
func (*Adapter) Discover(ctx context.Context, root agent.Root) ([]agent.SourceRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var refs []agent.SourceRef

	projectsDir := filepath.Join(root.Path, "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading %s: %w", projectsDir, err)
	}
	for _, dir := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !dir.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(projectsDir, dir.Name()))
		if err != nil {
			continue // unreadable project dir: surfaced later as a diagnostic by the pipeline
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			refs = append(refs, agent.SourceRef{
				Root: root,
				Path: filepath.Join(projectsDir, dir.Name(), f.Name()),
				Kind: agent.SourceFile,
			})
		}
	}

	refs = append(refs, discoverSidecars(root)...)
	return refs, nil
}

// rawLine is the JSONL envelope Claude Code writes per entry.
type rawLine struct {
	Type        string          `json:"type"`
	UUID        string          `json:"uuid"`
	ParentUUID  string          `json:"parentUuid"`
	IsSidechain bool            `json:"isSidechain"`
	SessionID   string          `json:"sessionId"`
	CWD         string          `json:"cwd"`
	GitBranch   string          `json:"gitBranch"`
	Timestamp   time.Time       `json:"timestamp"`
	RequestID   string          `json:"requestId"`
	CostUSD     *float64        `json:"costUSD"` // pre-v1.0.9 Claude Code
	Content     json.RawMessage `json:"content"` // system lines
	Message     json.RawMessage `json:"message"`
}

// rawMessage is the Anthropic message payload inside a line.
type rawMessage struct {
	ID      string          `json:"id"`
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   *rawUsage       `json:"usage"`
}

type rawUsage struct {
	InputTokens              int64  `json:"input_tokens"`
	OutputTokens             int64  `json:"output_tokens"`
	CacheCreationInputTokens int64  `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64  `json:"cache_read_input_tokens"`
	ServiceTier              string `json:"service_tier"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// Parse dispatches a source by shape: session transcripts here, sidecars
// in sidecar.go. Individual bad lines become diagnostics, never file
// failures.
func (a *Adapter) Parse(ctx context.Context, src agent.SourceRef, sink agent.RecordSink) error {
	if kind := classify(src.Root, src.Path); kind != srcSession {
		return a.parseSidecar(ctx, kind, src, sink)
	}
	return a.parseSession(ctx, src, sink)
}

// parseSession reads one session JSONL file and emits Session, Message,
// and ToolCall records.
func (a *Adapter) parseSession(ctx context.Context, src agent.SourceRef, sink agent.RecordSink) error {
	f, err := os.Open(src.Path)
	if err != nil {
		return fmt.Errorf("opening %s: %w", src.Path, err)
	}
	defer f.Close()

	sessionID := strings.TrimSuffix(filepath.Base(src.Path), ".jsonl")
	sess := canon.Session{
		Agent:      Slug,
		ExternalID: sessionID,
		SourcePath: src.Path,
	}

	var (
		messages   []canon.Message
		toolCalls  []canon.ToolCall
		pendingUse = map[string]int{} // tool_use block id → index into toolCalls
	)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxLineBytes)
	lineNo := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		lineNo++
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var raw rawLine
		if err := json.Unmarshal(line, &raw); err != nil {
			if serr := sink.Issue(canon.Issue{
				Agent: Slug, Severity: canon.SeverityWarn, Category: "parse",
				SourcePath: src.Path, Line: lineNo,
				Detail: fmt.Sprintf("skipping unparseable line: %v", err),
			}); serr != nil {
				return serr
			}
			continue
		}
		switch raw.Type {
		case "user", "assistant", "system":
		default:
			continue // progress lines and future types are not transcript entries
		}
		if raw.SessionID != "" && sess.ExternalID == "" {
			sess.ExternalID = raw.SessionID
		}

		msg, calls := a.convertLine(raw, len(messages))
		a.foldSession(&sess, raw, msg)

		for _, c := range calls {
			c.call.SessionExternalID = sess.ExternalID
			c.call.Seq = len(toolCalls)
			if c.useID != "" {
				pendingUse[c.useID] = len(toolCalls)
			}
			toolCalls = append(toolCalls, c.call)
		}
		a.pairResults(raw, pendingUse, toolCalls)

		msg.SessionExternalID = sess.ExternalID
		messages = append(messages, msg)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanning %s: %w", src.Path, err)
	}
	if len(messages) == 0 {
		return nil // not a transcript (empty or all-noise file); nothing to emit
	}

	if err := sink.Session(sess); err != nil {
		return err
	}
	for _, m := range messages {
		if err := sink.Message(m); err != nil {
			return err
		}
	}
	for _, c := range toolCalls {
		if err := sink.ToolCall(c); err != nil {
			return err
		}
	}
	return nil
}

// emittedCall carries a tool call together with the tool_use block id it
// must be paired against when the matching tool_result arrives.
type emittedCall struct {
	call  canon.ToolCall
	useID string
}

// convertLine maps one JSONL entry to a canonical message plus any tool
// calls it issued.
func (a *Adapter) convertLine(raw rawLine, seq int) (canon.Message, []emittedCall) {
	msg := canon.Message{
		Seq:              seq,
		ExternalID:       raw.UUID,
		ParentExternalID: raw.ParentUUID,
		Role:             canon.Role(raw.Type),
		Kind:             canon.KindMessage,
		CreatedAt:        raw.Timestamp,
		CWD:              raw.CWD,
		IsSidechain:      raw.IsSidechain,
		Content:          raw.Message,
	}

	var payload rawMessage
	if len(raw.Message) > 0 {
		if err := json.Unmarshal(raw.Message, &payload); err == nil {
			if payload.Role != "" {
				msg.Role = canon.Role(payload.Role)
			}
			msg.ContentID = payload.ID
			msg.Model = payload.Model
			if payload.Usage != nil {
				msg.Usage = &canon.Usage{
					InputTokens:      payload.Usage.InputTokens,
					OutputTokens:     payload.Usage.OutputTokens,
					CacheReadTokens:  payload.Usage.CacheReadInputTokens,
					CacheWriteTokens: payload.Usage.CacheCreationInputTokens,
					ServiceTier:      payload.Usage.ServiceTier,
					ReportedCostUSD:  raw.CostUSD,
					RequestID:        raw.RequestID,
				}
			}
		}
	} else if len(raw.Content) > 0 {
		// System lines carry top-level content; synthesize a payload so
		// rendering stays uniform.
		synth, _ := json.Marshal(map[string]json.RawMessage{
			"role":    json.RawMessage(`"system"`),
			"content": raw.Content,
		})
		msg.Content = synth
	}

	msg.Text = extractText(payload.Content, raw.Content)

	var calls []emittedCall
	for _, block := range blocks(payload.Content) {
		if block.Type != "tool_use" {
			continue
		}
		calls = append(calls, emittedCall{
			useID: block.ID,
			call: canon.ToolCall{
				MessageSeq: seq,
				Name:       block.Name,
				Kind:       normalizeTool(block.Name),
				Input:      block.Input,
				FilePath:   inputFilePath(block.Input),
				StartedAt:  raw.Timestamp,
			},
		})
	}
	return msg, calls
}

// foldSession accumulates session attributes from entries: first
// timestamp/cwd/branch wins for creation state, last timestamp wins for
// modification, title comes from the first non-sidechain user text.
func (a *Adapter) foldSession(sess *canon.Session, raw rawLine, msg canon.Message) {
	if sess.CreatedAt.IsZero() && !raw.Timestamp.IsZero() {
		sess.CreatedAt = raw.Timestamp
	}
	if !raw.Timestamp.IsZero() {
		sess.ModifiedAt = raw.Timestamp
	}
	if sess.CWD == "" {
		sess.CWD = raw.CWD
	}
	if raw.GitBranch != "" {
		sess.GitBranch = raw.GitBranch
	}
	if sess.Title == "" && msg.Role == canon.RoleUser && !msg.IsSidechain && msg.Text != "" {
		title := strings.TrimSpace(msg.Text)
		if len(title) > titleLimit {
			title = title[:titleLimit]
		}
		sess.Title = title
	}
}

// pairResults attaches tool_result blocks in this line to the pending
// tool_use they answer.
func (a *Adapter) pairResults(raw rawLine, pending map[string]int, calls []canon.ToolCall) {
	var payload rawMessage
	if len(raw.Message) == 0 || json.Unmarshal(raw.Message, &payload) != nil {
		return
	}
	for _, block := range blocks(payload.Content) {
		if block.Type != "tool_result" || block.ToolUseID == "" {
			continue
		}
		idx, ok := pending[block.ToolUseID]
		if !ok {
			continue
		}
		status := "ok"
		if block.IsError {
			status = "error"
		}
		calls[idx].ResultStatus = status
		calls[idx].ResultExcerpt = excerpt(block.Content)
	}
}

func blocks(content json.RawMessage) []contentBlock {
	if len(content) == 0 {
		return nil
	}
	var arr []contentBlock
	if err := json.Unmarshal(content, &arr); err != nil {
		return nil
	}
	return arr
}

func extractText(content, topLevel json.RawMessage) string {
	if len(content) > 0 {
		var s string
		if err := json.Unmarshal(content, &s); err == nil {
			return s
		}
		var parts []string
		for _, b := range blocks(content) {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	var s string
	if err := json.Unmarshal(topLevel, &s); err == nil {
		return s
	}
	return ""
}

func excerpt(content json.RawMessage) string {
	var s string
	if err := json.Unmarshal(content, &s); err != nil {
		// Structured tool_result content: keep a bounded raw slice.
		s = string(content)
	}
	s = strings.TrimSpace(s)
	if len(s) > resultExcerptCap {
		s = s[:resultExcerptCap]
	}
	return s
}

func normalizeTool(name string) canon.ToolKind {
	switch name {
	case "Bash", "BashOutput":
		return canon.ToolShell
	case "Read", "NotebookRead":
		return canon.ToolFileRead
	case "Write":
		return canon.ToolFileWrite
	case "Edit", "MultiEdit", "NotebookEdit":
		return canon.ToolFileEdit
	case "Grep":
		return canon.ToolSearch
	case "Glob", "LS":
		return canon.ToolDiscovery
	case "Task", "Agent":
		return canon.ToolSubagent
	case "WebFetch", "WebSearch":
		return canon.ToolWeb
	default:
		return canon.ToolOther
	}
}

func inputFilePath(input json.RawMessage) string {
	var in struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return ""
	}
	if in.FilePath != "" {
		return in.FilePath
	}
	return in.NotebookPath
}
