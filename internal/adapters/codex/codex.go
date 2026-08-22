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
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/jsonl"
)

// Slug identifies this adapter.
const Slug = canon.AgentSlug("codex")

const (
	maxLineBytes = 10 * 1024 * 1024
)

// Adapter implements agent.Adapter for Codex CLI. Discover snapshots the
// optional thread-name index once per root so concurrent rollout parses do not
// rescan the same sidecar for every session.
type Adapter struct {
	mu          sync.RWMutex
	threadNames map[string]map[string]string
}

// New returns the Codex adapter.
func New() *Adapter { return &Adapter{threadNames: make(map[string]map[string]string)} }

// Slug implements agent.Adapter.
func (*Adapter) Slug() canon.AgentSlug { return Slug }

// ParseVersion forces existing recoverable rollouts through the latest usage
// normalization and session-title enrichment.
func (*Adapter) ParseVersion() int { return 3 }

// RootSpec implements agent.Adapter: Codex relocates via CODEX_HOME.
func (*Adapter) RootSpec() agent.RootSpec {
	return agent.RootSpec{
		EnvVars:  []string{"CODEX_HOME"},
		Defaults: []string{"~/.codex"},
	}
}

// Discover walks sessions/YYYY/MM/DD/*.jsonl and snapshots the optional
// session_index.jsonl that records user-assigned Codex thread names.
func (a *Adapter) Discover(ctx context.Context, root agent.Root) ([]agent.SourceRef, error) {
	indexPath := filepath.Join(root.Path, "session_index.jsonl")
	names, err := loadThreadNames(indexPath)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.threadNames[root.Path] = names
	a.mu.Unlock()

	sessionsDir := filepath.Join(root.Path, "sessions")
	if _, err := os.Stat(sessionsDir); os.IsNotExist(err) {
		return nil, nil
	}
	var refs []agent.SourceRef
	err = filepath.WalkDir(sessionsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: pipeline diagnostics cover it
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
			// The index is one shared sidecar, so any rename invalidates every
			// rollout. That favors correct titles over selective reparse complexity.
			refs = append(refs, agent.SourceRef{
				Root: root, Path: path, Kind: agent.SourceFile,
				CompanionPaths: []string{indexPath},
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return refs, nil
}

type threadIndexEntry struct {
	ID         string `json:"id"`
	ThreadName string `json:"thread_name"`
}

func loadThreadNames(path string) (map[string]string, error) {
	names := make(map[string]string)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return names, nil
	}
	if err != nil {
		return nil, fmt.Errorf("opening Codex session index %s: %w", path, err)
	}
	defer f.Close()

	err = jsonl.Scan(f, maxLineBytes, func(_ int, raw []byte) error {
		var entry threadIndexEntry
		if json.Unmarshal(raw, &entry) == nil && entry.ID != "" {
			name := strings.TrimSpace(entry.ThreadName)
			if name != "" {
				names[entry.ID] = name // later rename entries win
			}
		}
		return nil
	}, func(_ int, _ int64) error { return nil })
	if err != nil {
		return nil, fmt.Errorf("reading Codex session index %s: %w", path, err)
	}
	return names, nil
}

func (a *Adapter) threadName(root, sessionID string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.threadNames[root][sessionID]
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

// tokenUsage mirrors Codex's token_count payloads. The response API reports
// cached reads and cache writes as disjoint details of input_tokens; its own
// parser fixture uses 40 cached + 60 written = 100 input. Reasoning is likewise
// a subset of output_tokens. Canonical ordinary input must subtract both cache
// details, while output must not add reasoning again.
type tokenUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
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
	if title := a.threadName(src.Root.Path, fallbackID); title != "" {
		sess.Title = canon.TruncateBytes(title, canon.SessionTitleLimit)
		sess.TitleOverride = true
	}

	// Records stream to the sink as they parse — memory stays bounded by
	// one line, not the rollout. The session is emitted before its first
	// child (session_meta is the first rollout line, so the external id is
	// settled by then) and re-emitted at EOF with the folded title and
	// timestamps. Exactly ONE message may be held back: the latest
	// assistant message waiting for the token_count that usually follows
	// it; any other emit flushes it first, so order is preserved and the
	// holdback never grows. Tool results pair through sink.ToolResult by
	// call id.
	var (
		messageCount int
		toolCount    int
		currentModel string
		tokens       tokenState
		pendingAsst  *canon.Message
		emitted      bool
	)
	emitSession := func() error {
		if emitted {
			return nil
		}
		emitted = true
		return sink.Session(sess)
	}
	emitMessage := func(m canon.Message) error {
		if err := emitSession(); err != nil {
			return err
		}
		m.SessionExternalID = sess.ExternalID
		return sink.Message(m)
	}
	flushPending := func() error {
		if pendingAsst == nil {
			return nil
		}
		m := *pendingAsst
		pendingAsst = nil
		return emitMessage(m)
	}

	scanErr := jsonl.Scan(f, maxLineBytes, func(lineNo int, raw []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(strings.TrimSpace(string(raw))) == 0 {
			return nil
		}
		var line rolloutLine
		if err := json.Unmarshal(raw, &line); err != nil {
			return sink.Issue(canon.Issue{
				Agent: Slug, Severity: canon.SeverityWarn, Category: "parse",
				SourcePath: src.Path, Line: lineNo,
				Detail: fmt.Sprintf("skipping unparseable line: %v", err),
			})
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
				if meta.ID != "" && !emitted {
					sess.ExternalID = meta.ID
					if title := a.threadName(src.Root.Path, meta.ID); title != "" {
						sess.Title = canon.TruncateBytes(title, canon.SessionTitleLimit)
						sess.TitleOverride = true
					}
				} else if meta.ID != "" && meta.ID != sess.ExternalID {
					// Children already carry the earlier id; switching now
					// would orphan them. Keep the id and say so.
					if err := sink.Issue(canon.Issue{
						Agent: Slug, Severity: canon.SeverityWarn, Category: "format",
						SourcePath: src.Path, Line: lineNo,
						Detail: fmt.Sprintf("late session_meta id %q ignored (session already %q)", meta.ID, sess.ExternalID),
					}); err != nil {
						return err
					}
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
				return nil
			}
			switch item.Type {
			case "message":
				if err := flushPending(); err != nil {
					return err
				}
				msg := canon.Message{
					Seq:       messageCount,
					Role:      canon.Role(item.Role),
					Kind:      canon.KindMessage,
					CreatedAt: line.Timestamp,
					Model:     currentModel,
					CWD:       sess.CWD,
					Content:   line.Payload,
					Text:      itemText(item),
				}
				messageCount++
				if msg.Role == canon.RoleUser && sess.Title == "" {
					if title := codexPromptTitle(msg.Text); title != "" {
						sess.Title = canon.TruncateBytes(title, canon.SessionTitleLimit)
					}
				}
				if msg.Role == canon.RoleAssistant {
					pendingAsst = &msg
					return nil
				}
				return emitMessage(msg)

			case "function_call":
				if err := flushPending(); err != nil {
					return err
				}
				if err := emitSession(); err != nil {
					return err
				}
				argsJSON := argumentsJSON(item.Arguments)
				args := toolArgs(argsJSON)
				tc := canon.ToolCall{
					SessionExternalID: sess.ExternalID,
					MessageSeq:        max(messageCount-1, 0),
					Seq:               toolCount,
					ExternalID:        item.CallID,
					Name:              item.Name,
					Kind:              normalizeTool(item.Name),
					Input:             json.RawMessage(argsJSON),
					FilePath:          args.Path,
					Command:           args.command(),
					OldText:           args.OldText,
					NewText:           cmp.Or(args.NewText, args.Content),
					StartedAt:         line.Timestamp,
				}
				toolCount++
				return sink.ToolCall(tc)

			case "function_call_output":
				if item.CallID == "" {
					return nil
				}
				if err := emitSession(); err != nil {
					return err
				}
				out := canon.TruncateBytes(strings.TrimSpace(item.Output), canon.ToolResultExcerptLimit)
				return sink.ToolResult(canon.ToolResult{
					SessionExternalID: sess.ExternalID,
					CallExternalID:    item.CallID,
					Status:            "ok",
					Excerpt:           out,
				})
			}

		case "event_msg":
			var ev eventMsg
			if json.Unmarshal(line.Payload, &ev) != nil || ev.Type != "token_count" {
				return nil
			}
			var info tokenCountInfo
			if json.Unmarshal(ev.Info, &info) != nil {
				return nil
			}
			delta := perTurnUsage(info, &tokens)
			if delta == nil {
				return nil
			}
			ordinaryInput := delta.InputTokens - delta.CachedInputTokens - delta.CacheWriteInputTokens
			if ordinaryInput < 0 {
				if err := sink.Issue(canon.Issue{
					Agent: Slug, Severity: canon.SeverityWarn, Category: "usage",
					SourcePath: src.Path, Line: lineNo,
					Detail: fmt.Sprintf(
						"Codex input subsets exceed input total (%d cached + %d cache write > %d input); clamping ordinary input to zero",
						delta.CachedInputTokens, delta.CacheWriteInputTokens, delta.InputTokens),
				}); err != nil {
					return err
				}
				ordinaryInput = 0
			}
			usage := &canon.Usage{
				InputTokens:      ordinaryInput,
				OutputTokens:     delta.OutputTokens,
				CacheReadTokens:  delta.CachedInputTokens,
				CacheWriteTokens: delta.CacheWriteInputTokens,
				ReasoningTokens:  delta.ReasoningOutputTokens,
			}
			// Attach to the held assistant message when one is waiting;
			// otherwise (the count preceded the assistant text, as Codex
			// often orders it) park it on a usage-info entry so nothing is
			// lost.
			if pendingAsst != nil && pendingAsst.Usage == nil {
				pendingAsst.Usage = usage
				return flushPending()
			}
			info2 := canon.Message{
				Seq:       messageCount,
				Role:      canon.RoleAssistant,
				Kind:      canon.KindInfo,
				CreatedAt: line.Timestamp,
				Model:     currentModel,
				Content:   line.Payload,
				Usage:     usage,
			}
			messageCount++
			return emitMessage(info2)
		}
		return nil
	}, func(lineNo int, size int64) error {
		return sink.Issue(canon.Issue{
			Agent: Slug, Severity: canon.SeverityWarn, Category: "parse",
			SourcePath: src.Path, Line: lineNo,
			Detail: fmt.Sprintf("skipping oversized line (%d bytes > %d limit)", size, maxLineBytes),
		})
	})
	if scanErr != nil {
		return scanErr
	}
	if err := flushPending(); err != nil {
		return err
	}
	if !emitted {
		return nil // nothing indexable in this rollout
	}

	// Final emit: the folded title, timestamps, cwd, and branch.
	return sink.Session(sess)
}

// tokenState is one rollout's token_count bookkeeping: the last cumulative
// total seen (the delta path's baseline) and the last per-turn usage
// actually emitted (what makes a repeat recognizable).
type tokenState struct {
	total *tokenUsage
	last  *tokenUsage
}

// perTurnUsage recovers this turn's usage: last_token_usage when the CLI
// provides it, else the delta from the previous cumulative total. A total
// smaller than the previous one means the counter reset (new sub-session);
// treat it as absolute. It returns nil when the event carries no new
// tokens.
//
// A token_count event whose payload REPEATS the one before it — the same
// last_token_usage beside a cumulative total that has not advanced — is one
// turn emitted twice, not two turns that happened to cost the same. Codex
// rows carry neither a content id nor a request id, so the store's
// (content_id, request_id) usage dedupe cannot see them and the tokens
// would be recorded, and billed, twice. The delta path never had this
// problem: a total that has not moved yields a zero delta. The cumulative
// total is what distinguishes the two cases, which is why it, and not the
// per-turn figure alone, decides.
func perTurnUsage(info tokenCountInfo, st *tokenState) *tokenUsage {
	if info.Last != nil {
		if st.last != nil && *st.last == *info.Last && sameUsage(st.total, info.Total) {
			return nil
		}
		st.last = info.Last
		if info.Total != nil {
			st.total = info.Total
		}
		return info.Last
	}
	// An event without last_token_usage breaks any run of repeats: a later
	// identical Last is a genuinely new turn, not a duplicate of this one.
	st.last = nil
	if info.Total == nil {
		return nil
	}
	cur := info.Total
	p := st.total
	st.total = cur
	if p == nil || cur.TotalTokens < p.TotalTokens {
		return cur
	}
	return &tokenUsage{
		InputTokens:           cur.InputTokens - p.InputTokens,
		CachedInputTokens:     cur.CachedInputTokens - p.CachedInputTokens,
		CacheWriteInputTokens: cur.CacheWriteInputTokens - p.CacheWriteInputTokens,
		OutputTokens:          cur.OutputTokens - p.OutputTokens,
		ReasoningOutputTokens: cur.ReasoningOutputTokens - p.ReasoningOutputTokens,
		TotalTokens:           cur.TotalTokens - p.TotalTokens,
	}
}

// sameUsage compares two optional payloads, treating "both absent" as
// equal — a rollout that reports no cumulative total at all still has its
// consecutive identical per-turn figures collapsed.
func sameUsage(a, b *tokenUsage) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
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

// codexPromptTitle rejects user-role context envelopes injected by Codex.
// They remain transcript provenance but do not describe the session's purpose.
func codexPromptTitle(text string) string {
	title := strings.TrimSpace(text)
	for _, prefix := range []string{
		"<environment_context>",
		"<permissions",
		"<recommended_plugins>",
		"<user_instructions>",
	} {
		if strings.HasPrefix(title, prefix) {
			return ""
		}
	}
	return title
}

// codexToolArgs is the subset of a function call's arguments the
// canonical record keeps beside the verbatim JSON.
//
// Codex writes shell commands as an ARRAY of argv, not a string —
// typically ["bash", "-lc", "<script>"]. Rendering that array's JSON was
// what the commands browser did before Command existed, so every Codex row
// read as `["bash","-lc","go test ./..."]`.
type codexToolArgs struct {
	Command []string `json:"command"`
	Path    string   `json:"path"`
	OldText string   `json:"old_text"`
	NewText string   `json:"new_text"`
	Content string   `json:"content"`
}

func toolArgs(raw string) codexToolArgs {
	var a codexToolArgs
	_ = json.Unmarshal([]byte(raw), &a)
	return a
}

// command renders the argv as the line a user would actually run. The
// shell wrapper is unwrapped — `["bash","-lc","go test ./..."]` is the
// script, not three arguments — and anything else is joined.
func (a codexToolArgs) command() string {
	switch {
	case len(a.Command) == 0:
		return ""
	case len(a.Command) == 3 && strings.HasPrefix(a.Command[1], "-") &&
		strings.Contains(a.Command[1], "c"):
		return a.Command[2]
	default:
		return strings.Join(a.Command, " ")
	}
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
