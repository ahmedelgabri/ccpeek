// Package codex is the OpenAI Codex CLI adapter. Codex persists every
// session as a JSONL event stream under
// ~/.codex/sessions/YYYY/MM/DD/rollout-<id>.jsonl — organized by DATE, not
// project, which is one of the reasons v2's model is session-centric
// (docs/v2-plan.md §5.2). Token usage arrives as `token_count` events with
// CUMULATIVE totals; per-turn usage is recovered from `last_token_usage`
// when present, else by delta against the previous total with reset
// detection (§5.3 correctness notes). Logs from before 2025-09 carry no
// usage at all.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

// Slug identifies this adapter.
const Slug = canon.AgentSlug("codex")

const (
	maxLineBytes = 10 * 1024 * 1024
	titleLimit   = 200
	excerptCap   = 400
)

// Adapter implements agent.Adapter for Codex CLI.
type Adapter struct{}

// New returns the Codex adapter.
func New() *Adapter { return &Adapter{} }

// Slug implements agent.Adapter.
func (*Adapter) Slug() canon.AgentSlug { return Slug }

// RootSpec implements agent.Adapter: Codex relocates via CODEX_HOME.
func (*Adapter) RootSpec() agent.RootSpec {
	return agent.RootSpec{
		EnvVars:  []string{"CODEX_HOME"},
		Defaults: []string{"~/.codex"},
	}
}

// Discover walks sessions/YYYY/MM/DD/*.jsonl.
func (*Adapter) Discover(ctx context.Context, root agent.Root) ([]agent.SourceRef, error) {
	sessionsDir := filepath.Join(root.Path, "sessions")
	if _, err := os.Stat(sessionsDir); os.IsNotExist(err) {
		return nil, nil
	}
	var refs []agent.SourceRef
	err := filepath.WalkDir(sessionsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: pipeline diagnostics cover it
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
			refs = append(refs, agent.SourceRef{Root: root, Path: path, Kind: agent.SourceFile})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return refs, nil
}

type rolloutLine struct {
	Timestamp time.Time       `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type sessionMeta struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	CWD       string    `json:"cwd"`
	Git       struct {
		Branch string `json:"branch"`
	} `json:"git"`
}

type turnContext struct {
	Model string `json:"model"`
	CWD   string `json:"cwd"`
}

type responseItem struct {
	Type    string `json:"type"` // message | function_call | function_call_output
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	CallID    string `json:"call_id"`
	Output    string `json:"output"`
}

type eventMsg struct {
	Type string          `json:"type"`
	Info json.RawMessage `json:"info"`
}

type tokenUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
}

type tokenCountInfo struct {
	Total *tokenUsage `json:"total_token_usage"`
	Last  *tokenUsage `json:"last_token_usage"`
}

// Parse reads one rollout file.
func (a *Adapter) Parse(ctx context.Context, src agent.SourceRef, sink agent.RecordSink) error {
	f, err := os.Open(src.Path)
	if err != nil {
		return fmt.Errorf("opening %s: %w", src.Path, err)
	}
	defer f.Close()

	// Fallback external id from the file name (rollout-<id>.jsonl); the
	// session_meta payload id wins when present.
	fallbackID := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(src.Path), "rollout-"), ".jsonl")

	sess := canon.Session{
		Agent:      Slug,
		ExternalID: fallbackID,
		SourcePath: src.Path,
	}
	var (
		messages     []canon.Message
		toolCalls    []canon.ToolCall
		callIndex    = map[string]int{} // call_id → toolCalls index
		currentModel string
		prevTotal    *tokenUsage
		lastAsstIdx  = -1
	)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxLineBytes)
	lineNo := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		lineNo++
		raw := scanner.Bytes()
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		var line rolloutLine
		if err := json.Unmarshal(raw, &line); err != nil {
			if serr := sink.Issue(canon.Issue{
				Agent: Slug, Severity: canon.SeverityWarn, Category: "parse",
				SourcePath: src.Path, Line: lineNo,
				Detail: fmt.Sprintf("skipping unparseable line: %v", err),
			}); serr != nil {
				return serr
			}
			continue
		}
		if !line.Timestamp.IsZero() {
			if sess.CreatedAt.IsZero() {
				sess.CreatedAt = line.Timestamp
			}
			sess.ModifiedAt = line.Timestamp
		}

		switch line.Type {
		case "session_meta":
			var meta sessionMeta
			if json.Unmarshal(line.Payload, &meta) == nil {
				if meta.ID != "" {
					sess.ExternalID = meta.ID
				}
				sess.CWD = meta.CWD
				sess.GitBranch = meta.Git.Branch
				if !meta.Timestamp.IsZero() {
					sess.CreatedAt = meta.Timestamp
				}
			}

		case "turn_context":
			var tc turnContext
			if json.Unmarshal(line.Payload, &tc) == nil && tc.Model != "" {
				currentModel = tc.Model
			}

		case "response_item":
			var item responseItem
			if json.Unmarshal(line.Payload, &item) != nil {
				continue
			}
			switch item.Type {
			case "message":
				msg := canon.Message{
					Seq:       len(messages),
					Role:      canon.Role(item.Role),
					Kind:      canon.KindMessage,
					CreatedAt: line.Timestamp,
					Model:     currentModel,
					CWD:       sess.CWD,
					Content:   line.Payload,
					Text:      itemText(item),
				}
				if msg.Role == canon.RoleUser && sess.Title == "" && msg.Text != "" {
					t := strings.TrimSpace(msg.Text)
					if len(t) > titleLimit {
						t = t[:titleLimit]
					}
					sess.Title = t
				}
				if msg.Role == canon.RoleAssistant {
					lastAsstIdx = len(messages)
				}
				messages = append(messages, msg)

			case "function_call":
				callIndex[item.CallID] = len(toolCalls)
				toolCalls = append(toolCalls, canon.ToolCall{
					MessageSeq: max(len(messages)-1, 0),
					Seq:        len(toolCalls),
					Name:       item.Name,
					Kind:       normalizeTool(item.Name),
					Input:      json.RawMessage(argumentsJSON(item.Arguments)),
					StartedAt:  line.Timestamp,
				})

			case "function_call_output":
				if idx, ok := callIndex[item.CallID]; ok {
					out := item.Output
					if len(out) > excerptCap {
						out = out[:excerptCap]
					}
					toolCalls[idx].ResultStatus = "ok"
					toolCalls[idx].ResultExcerpt = out
				}
			}

		case "event_msg":
			var ev eventMsg
			if json.Unmarshal(line.Payload, &ev) != nil || ev.Type != "token_count" {
				continue
			}
			var info tokenCountInfo
			if json.Unmarshal(ev.Info, &info) != nil {
				continue
			}
			delta := perTurnUsage(info, &prevTotal)
			if delta == nil {
				continue
			}
			usage := &canon.Usage{
				InputTokens:     delta.InputTokens - delta.CachedInputTokens,
				OutputTokens:    delta.OutputTokens,
				CacheReadTokens: delta.CachedInputTokens,
				ReasoningTokens: delta.ReasoningOutputTokens,
			}
			// Attach to the latest assistant message without usage; when
			// the count arrives before the assistant text (as Codex does),
			// park it on a usage-info entry so nothing is lost.
			if lastAsstIdx >= 0 && messages[lastAsstIdx].Usage == nil {
				messages[lastAsstIdx].Usage = usage
			} else {
				messages = append(messages, canon.Message{
					Seq:       len(messages),
					Role:      canon.RoleAssistant,
					Kind:      canon.KindInfo,
					CreatedAt: line.Timestamp,
					Model:     currentModel,
					Content:   line.Payload,
					Usage:     usage,
				})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanning %s: %w", src.Path, err)
	}
	if len(messages) == 0 && len(toolCalls) == 0 {
		return nil
	}

	if err := sink.Session(sess); err != nil {
		return err
	}
	for i := range messages {
		messages[i].SessionExternalID = sess.ExternalID
		if err := sink.Message(messages[i]); err != nil {
			return err
		}
	}
	for i := range toolCalls {
		toolCalls[i].SessionExternalID = sess.ExternalID
		if err := sink.ToolCall(toolCalls[i]); err != nil {
			return err
		}
	}
	return nil
}

// perTurnUsage recovers this turn's usage: last_token_usage when the CLI
// provides it, else the delta from the previous cumulative total. A total
// smaller than the previous one means the counter reset (new sub-session);
// treat it as absolute.
func perTurnUsage(info tokenCountInfo, prev **tokenUsage) *tokenUsage {
	if info.Last != nil {
		if info.Total != nil {
			*prev = info.Total
		}
		return info.Last
	}
	if info.Total == nil {
		return nil
	}
	cur := info.Total
	p := *prev
	*prev = cur
	if p == nil || cur.TotalTokens < p.TotalTokens {
		return cur
	}
	return &tokenUsage{
		InputTokens:           cur.InputTokens - p.InputTokens,
		CachedInputTokens:     cur.CachedInputTokens - p.CachedInputTokens,
		OutputTokens:          cur.OutputTokens - p.OutputTokens,
		ReasoningOutputTokens: cur.ReasoningOutputTokens - p.ReasoningOutputTokens,
		TotalTokens:           cur.TotalTokens - p.TotalTokens,
	}
}

func itemText(item responseItem) string {
	var parts []string
	for _, c := range item.Content {
		if c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// argumentsJSON keeps the function-call arguments as raw JSON; Codex
// double-encodes them as a string.
func argumentsJSON(args string) string {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return "{}"
	}
	if json.Valid([]byte(trimmed)) {
		return trimmed
	}
	b, _ := json.Marshal(map[string]string{"raw": args})
	return string(b)
}

func normalizeTool(name string) canon.ToolKind {
	switch name {
	case "shell", "local_shell", "exec_command":
		return canon.ToolShell
	case "apply_patch":
		return canon.ToolFileEdit
	case "read_file", "view_image":
		return canon.ToolFileRead
	case "web_search":
		return canon.ToolWeb
	default:
		return canon.ToolOther
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
