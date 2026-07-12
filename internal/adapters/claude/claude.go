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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
	_, err := a.ParseTail(ctx, src, agent.TailState{}, sink)
	return err
}

// ParseTail implements agent.TailParser: session JSONL is append-only, so
// a stored cursor lets a changed source parse only its new bytes. A zero
// state parses everything and returns the initial cursor. Sidecars have
// no cursor semantics and always parse fully.
func (a *Adapter) ParseTail(ctx context.Context, src agent.SourceRef, state agent.TailState, sink agent.RecordSink) (agent.TailState, error) {
	if kind := classify(src.Root, src.Path); kind != srcSession {
		return agent.TailState{}, a.parseSidecar(ctx, kind, src, sink)
	}
	return a.parseSession(ctx, src, state, sink)
}

// parseSession reads one session JSONL file from the cursor (or the
// start) and emits Session, Message, ToolCall, and ToolResult records.
// Only complete lines are consumed: a partially-written trailing line
// stays ahead of the returned cursor for the next pass.
func (a *Adapter) parseSession(ctx context.Context, src agent.SourceRef, state agent.TailState, sink agent.RecordSink) (agent.TailState, error) {
	f, err := os.Open(src.Path)
	if err != nil {
		return state, fmt.Errorf("opening %s: %w", src.Path, err)
	}
	defer f.Close()

	hasher := sha256.New()
	r := bufio.NewReaderSize(f, 64*1024)
	resuming := state.Offset > 0
	if resuming {
		// The cursor is only valid if the bytes it covers are unchanged:
		// re-hash the prefix and compare. Cheap relative to parsing — no
		// JSON is decoded.
		if n, err := io.CopyN(hasher, r, state.Offset); err != nil || n < state.Offset {
			return agent.TailState{}, agent.ErrTailInvalid // file shrank
		}
		if hex.EncodeToString(hasher.Sum(nil)) != state.PrefixHash {
			return agent.TailState{}, agent.ErrTailInvalid // prefix rewritten
		}
	} else {
		state = agent.TailState{}
	}

	sessionID := strings.TrimSuffix(filepath.Base(src.Path), ".jsonl")
	sess := canon.Session{
		Agent:      Slug,
		ExternalID: sessionID,
		SourcePath: src.Path,
	}

	var (
		messages    []canon.Message
		toolCalls   []canon.ToolCall
		lateResults []canon.ToolResult
		pendingUse  = map[string]int{} // tool_use block id → index into toolCalls
	)

	offset := state.Offset
	lineNo := state.LineNo
	for {
		if err := ctx.Err(); err != nil {
			return state, err
		}
		raw, rerr := readLine(r)
		if rerr != nil && rerr != io.EOF {
			return state, fmt.Errorf("reading %s: %w", src.Path, rerr)
		}
		if len(raw) == 0 || raw[len(raw)-1] != '\n' {
			// Partial trailing line (mid-write): leave it for the next pass.
			break
		}
		hasher.Write(raw)
		offset += int64(len(raw))
		lineNo++

		line := strings.TrimSpace(string(raw))
		if line == "" {
			continue
		}
		var entry rawLine
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			if serr := sink.Issue(canon.Issue{
				Agent: Slug, Severity: canon.SeverityWarn, Category: "parse",
				SourcePath: src.Path, Line: lineNo,
				Detail: fmt.Sprintf("skipping unparseable line: %v", err),
			}); serr != nil {
				return state, serr
			}
			continue
		}
		switch entry.Type {
		case "user", "assistant", "system":
		default:
			continue // progress lines and future types are not transcript entries
		}
		if entry.SessionID != "" && sess.ExternalID == "" {
			sess.ExternalID = entry.SessionID
		}

		msg, calls := a.convertLine(entry, state.MessageSeq+len(messages))
		a.foldSession(&sess, entry, msg)

		for _, c := range calls {
			c.call.SessionExternalID = sess.ExternalID
			c.call.Seq = state.ToolSeq + len(toolCalls)
			if c.useID != "" {
				pendingUse[c.useID] = len(toolCalls)
			}
			toolCalls = append(toolCalls, c.call)
		}
		lateResults = a.pairResults(entry, pendingUse, toolCalls, resuming, sess.ExternalID, lateResults)

		msg.SessionExternalID = sess.ExternalID
		messages = append(messages, msg)
	}

	newState := agent.TailState{
		Offset:     offset,
		PrefixHash: hex.EncodeToString(hasher.Sum(nil)),
		MessageSeq: state.MessageSeq + len(messages),
		ToolSeq:    state.ToolSeq + len(toolCalls),
		LineNo:     lineNo,
	}
	if len(messages) == 0 {
		// Not a transcript (or nothing new): nothing to emit, but the
		// cursor still advances past what was read.
		return newState, nil
	}

	if err := sink.Session(sess); err != nil {
		return state, err
	}
	for _, m := range messages {
		if err := sink.Message(m); err != nil {
			return state, err
		}
	}
	for _, c := range toolCalls {
		if err := sink.ToolCall(c); err != nil {
			return state, err
		}
	}
	for _, res := range lateResults {
		if err := sink.ToolResult(res); err != nil {
			return state, err
		}
	}
	return newState, nil
}

// readLine returns the next line including its trailing newline, so the
// caller can hash and count exactly the bytes consumed. A final unterminated
// line is returned (without a trailing newline) alongside io.EOF.
func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		frag, err := r.ReadSlice('\n')
		line = append(line, frag...)
		if err == bufio.ErrBufferFull {
			if len(line) > maxLineBytes {
				return line, fmt.Errorf("line exceeds %d bytes", maxLineBytes)
			}
			continue
		}
		return line, err
	}
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
				ExternalID: block.ID,
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
// tool_use they answer. When resuming from a cursor, a result whose
// tool_use sits in the already-indexed prefix can't be paired in memory —
// it is collected as a canon.ToolResult for the sink to apply to the
// stored call instead.
func (a *Adapter) pairResults(raw rawLine, pending map[string]int, calls []canon.ToolCall, resuming bool, sessionID string, late []canon.ToolResult) []canon.ToolResult {
	var payload rawMessage
	if len(raw.Message) == 0 || json.Unmarshal(raw.Message, &payload) != nil {
		return late
	}
	for _, block := range blocks(payload.Content) {
		if block.Type != "tool_result" || block.ToolUseID == "" {
			continue
		}
		status := "ok"
		if block.IsError {
			status = "error"
		}
		idx, ok := pending[block.ToolUseID]
		if !ok {
			if resuming {
				late = append(late, canon.ToolResult{
					SessionExternalID: sessionID,
					CallExternalID:    block.ToolUseID,
					Status:            status,
					Excerpt:           excerpt(block.Content),
				})
			}
			continue
		}
		calls[idx].ResultStatus = status
		calls[idx].ResultExcerpt = excerpt(block.Content)
	}
	return late
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
