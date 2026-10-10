// Package pi is the Pi adapter (badlogic/pi-mono coding agent). Pi is the
// rare agent with a documented, versioned session format
// (packages/coding-agent/docs/session-format.md): JSONL files whose first
// line is a session header and whose entries form a tree via id/parentId,
// enabling in-place branching. Assistant entries carry token usage AND a
// pre-computed cost breakdown — the first consumer of the cost engine's
// automatic reported-first selection (docs/v2-plan.md §6).
package pi

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/jsonl"
)

// Slug identifies this adapter.
const Slug = canon.AgentSlug("pi")

const (
	maxLineBytes = 10 * 1024 * 1024
)

// Adapter implements agent.Adapter for Pi.
type Adapter struct{}

// New returns the Pi adapter.
func New() *Adapter { return &Adapter{} }

// Slug implements agent.Adapter.
func (*Adapter) Slug() canon.AgentSlug { return Slug }

// ParseVersion forces one full reparse after per-message provider/model,
// summary-entry usage, fallback-title normalization, codemode scripts with
// their nested calls, thinking text with image locators, error/abort
// events, and tool timing became canonical.
func (*Adapter) ParseVersion() int { return 7 }

// RootSpec implements agent.Adapter: Pi relocates its data dir via
// PI_CODING_AGENT_DIR.
func (*Adapter) RootSpec() agent.RootSpec {
	return agent.RootSpec{
		EnvVars:  []string{"PI_CODING_AGENT_DIR"},
		Defaults: []string{"~/.pi/agent"},
	}
}

// Discover enumerates session JSONL files under sessions/<encoded-cwd>/.
// The directory name is treated as opaque — cwd comes from the session
// header, per the session-centric rule that paths are never identity.
func (*Adapter) Discover(ctx context.Context, root agent.Root) ([]agent.SourceRef, error) {
	sessionsDir := filepath.Join(root.Path, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", sessionsDir, err)
	}
	var refs []agent.SourceRef
	for _, dir := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !dir.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(sessionsDir, dir.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			refs = append(refs, agent.SourceRef{
				Root: root,
				Path: filepath.Join(sessionsDir, dir.Name(), f.Name()),
				Kind: agent.SourceFile,
			})
		}
	}
	return refs, nil
}

// entry is the union of Pi's typed JSONL entries (session-format.md).
type entry struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	ParentID  string    `json:"parentId"`
	Timestamp time.Time `json:"timestamp"`

	// type == "session" (header)
	Version       int    `json:"version"`
	CWD           string `json:"cwd"`
	ParentSession string `json:"parentSession"`

	// type == "message"
	Message json.RawMessage `json:"message"`

	// type == "model_change"
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`

	// type == "compaction" | "branch_summary"
	Summary          string   `json:"summary"`
	FirstKeptEntryID string   `json:"firstKeptEntryId"`
	TokensBefore     int64    `json:"tokensBefore"`
	FromID           string   `json:"fromId"`
	Usage            *piUsage `json:"usage"`

	// type == "label"
	TargetID string `json:"targetId"`
	Label    string `json:"label"`

	// type == "session_info"
	Name string `json:"name"`

	// type == "custom" | "custom_message"
	CustomType string          `json:"customType"`
	Content    json.RawMessage `json:"content"`
}

type piMessage struct {
	Role     string          `json:"role"`
	Content  json.RawMessage `json:"content"`
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Usage    *piUsage        `json:"usage"`

	// role == "toolResult": the outcome of an earlier toolCall block,
	// delivered as its own message entry.
	// role == "assistant": why the turn ended; "error" and "aborted" carry
	// errorMessage.
	StopReason   string `json:"stopReason"`
	ErrorMessage string `json:"errorMessage"`

	ToolCallID  string         `json:"toolCallId"`
	ToolName    string         `json:"toolName"`
	IsError     bool           `json:"isError"`
	NestedCalls *piNestedCalls `json:"nestedCalls"`
}

// piNestedCalls is Pi's bounded record of the tools a tool ran itself
// (codemode's tools.read, tools.edit, …). Pi keeps no transcript entries
// for them, so this is the only trace of what a script did. Results are
// never recorded; arguments may be omitted when they are too large.
type piNestedCalls struct {
	Calls []piNestedCall `json:"calls"`
}

type piNestedCall struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Status     string          `json:"status"`
	Arguments  json.RawMessage `json:"arguments"`
	Error      string          `json:"error"`
	DurationMs float64         `json:"durationMs"`
}

type piUsage struct {
	Input       int64   `json:"input"`
	Output      int64   `json:"output"`
	CacheRead   int64   `json:"cacheRead"`
	CacheWrite  int64   `json:"cacheWrite"`
	TotalTokens int64   `json:"totalTokens"`
	Cost        *piCost `json:"cost"`
}

type piCost struct {
	Total float64 `json:"total"`
}

func canonicalUsage(u *piUsage) *canon.Usage {
	if u == nil {
		return nil
	}
	out := &canon.Usage{
		InputTokens: u.Input, OutputTokens: u.Output,
		CacheReadTokens: u.CacheRead, CacheWriteTokens: u.CacheWrite,
	}
	if u.Cost != nil {
		total := u.Cost.Total
		out.ReportedCostUSD = &total
	}
	return out
}

type piBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`

	// type == "thinking" | "image". An image's base64 data is deliberately
	// not decoded: it stays in the stored content, located by pointer.
	Thinking string `json:"thinking"`
	MimeType string `json:"mimeType"`

	// type == "toolCall" (assistant content blocks).
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// normalizeTool maps Pi's tool names (bash, read, edit, write,
// web_search, todo, …) onto the shared taxonomy.
func normalizeTool(name string) canon.ToolKind {
	switch name {
	case "bash":
		return canon.ToolShell
	case "read":
		return canon.ToolFileRead
	case "write":
		return canon.ToolFileWrite
	case "edit", "multi_edit":
		return canon.ToolFileEdit
	case "grep", "search":
		return canon.ToolSearch
	case "find", "ls", "glob":
		return canon.ToolDiscovery
	case "web_search", "web_fetch", "fetch":
		return canon.ToolWeb
	case "codemode":
		return canon.ToolScript
	default:
		return canon.ToolOther
	}
}

// normalizeRole maps Pi's message roles onto canon's closed vocabulary
// (user/assistant/system/tool). Pi delivers a tool's outcome as a message
// with role "toolResult"; storing that string verbatim put a value outside
// the vocabulary in messages.role, so every role-keyed surface — the
// transcript's rendering register, the role filters, anything grouping by
// author — simply missed Pi's tool results. The raw payload in
// Message.Content keeps the original spelling either way.
func normalizeRole(role string) canon.Role {
	if role == "toolResult" {
		return canon.RoleTool
	}
	return canon.Role(role)
}

// piToolArgs is the subset of a toolCall's arguments the canonical record
// keeps beside the verbatim JSON. Pi spells the edit payload oldText /
// newText where Claude spells it old_string / new_string — normalizing
// here is what lets the query layer stop knowing either.
type piToolArgs struct {
	Path    string `json:"path"`
	Command string `json:"command"`
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
	Content string `json:"content"` // write
	Code    string `json:"code"`    // codemode
}

// toolCall builds the canonical record for one Pi tool invocation, either
// a model-issued toolCall block or a nested call a tool made itself.
func toolCall(sessionID string, messageSeq, seq int, id, name string,
	arguments json.RawMessage, at time.Time,
) canon.ToolCall {
	var a piToolArgs
	_ = json.Unmarshal(arguments, &a)
	return canon.ToolCall{
		SessionExternalID: sessionID,
		MessageSeq:        messageSeq,
		Seq:               seq,
		ExternalID:        id,
		Name:              name,
		Kind:              normalizeTool(name),
		Input:             arguments,
		FilePath:          a.Path,
		Command:           cmp.Or(a.Command, a.Code),
		OldText:           a.OldText,
		NewText:           cmp.Or(a.NewText, a.Content),
		StartedAt:         at,
	}
}

// Parse reads one Pi session file: header first, then tree entries.
// Unknown entry types are tolerated silently (the spec allows extensions);
// unparseable lines become diagnostics.
func (a *Adapter) Parse(ctx context.Context, src agent.SourceRef, sink agent.RecordSink) error {
	f, err := os.Open(src.Path)
	if err != nil {
		return fmt.Errorf("opening %s: %w", src.Path, err)
	}
	defer f.Close()

	// Records stream to the sink as they parse — memory stays bounded by
	// one line, not the session. The session is emitted right after the
	// header (children need it) and re-emitted at EOF with the folded
	// title and final ModifiedAt; the sink clears prior children only on
	// the first emit. Tool results pair through sink.ToolResult by call
	// id instead of an in-memory index.
	var (
		sess            canon.Session
		haveHeader      bool
		messageCount    int
		toolCount       int
		currentProvider string
		currentModel    string
	)

	scanErr := jsonl.Scan(f, maxLineBytes, func(lineNo int, line []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(strings.TrimSpace(string(line))) == 0 {
			return nil
		}
		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			return sink.Issue(canon.Issue{
				Agent: Slug, Severity: canon.SeverityWarn, Category: "parse",
				SourcePath: src.Path, Line: lineNo,
				Detail: fmt.Sprintf("skipping unparseable line: %v", err),
			})
		}

		if !haveHeader {
			if e.Type != "session" {
				return fmt.Errorf("%s: first line is %q, want session header", src.Path, e.Type)
			}
			haveHeader = true
			sess = canon.Session{
				Agent:      Slug,
				ExternalID: e.ID,
				CreatedAt:  e.Timestamp,
				ModifiedAt: e.Timestamp,
				CWD:        e.CWD,
				SourcePath: src.Path,
			}
			if err := sink.Session(sess); err != nil {
				return err
			}
			if e.ParentSession != "" {
				return sink.SessionRelation(canon.SessionRelation{
					Agent:          Slug,
					FromExternalID: e.ID,
					ToExternalID:   e.ParentSession,
					Kind:           canon.RelForkOf,
					Evidence:       json.RawMessage(`{"source":"header.parentSession"}`),
				})
			}
			return nil
		}

		if !e.Timestamp.IsZero() {
			sess.ModifiedAt = e.Timestamp
		}

		msg, dec, ok := a.convertEntry(e, messageCount, &sess,
			&currentProvider, &currentModel)
		if !ok {
			return nil
		}
		msg.SessionExternalID = sess.ExternalID

		// Tool calls ride in assistant content blocks; their results
		// arrive later as dedicated role=toolResult messages referencing
		// the call id. Both read the payload convertEntry already decoded.
		if dec.ok {
			switch {
			case dec.pm.Role == "toolResult" && dec.pm.ToolCallID != "":
				status := "ok"
				if dec.pm.IsError {
					status = "error"
				}
				if err := sink.ToolResult(canon.ToolResult{
					SessionExternalID: sess.ExternalID,
					CallExternalID:    dec.pm.ToolCallID,
					Status:            status,
					Excerpt:           canon.TruncateBytes(strings.TrimSpace(msg.Text), canon.ToolResultExcerptLimit),
					At:                e.Timestamp,
				}); err != nil {
					return err
				}
				// Nested calls anchor to the result message that records
				// them, so the transcript shows what a script did right
				// under its output.
				if dec.pm.NestedCalls != nil {
					for _, nc := range dec.pm.NestedCalls.Calls {
						tc := toolCall(sess.ExternalID, msg.Seq, toolCount,
							nc.ID, nc.Name, nc.Arguments, e.Timestamp)
						tc.ResultStatus = nc.Status
						tc.ResultExcerpt = canon.TruncateBytes(strings.TrimSpace(nc.Error), canon.ToolResultExcerptLimit)
						tc.DurationMs = int64(math.Round(nc.DurationMs))
						if err := sink.ToolCall(tc); err != nil {
							return err
						}
						toolCount++
					}
				}
			default:
				for _, b := range dec.blocks {
					if b.Type != "toolCall" {
						continue
					}
					if err := sink.ToolCall(toolCall(sess.ExternalID, msg.Seq, toolCount,
						b.ID, b.Name, b.Arguments, e.Timestamp)); err != nil {
						return err
					}
					toolCount++
				}
			}
		}
		if sess.Title == "" && msg.Role == canon.RoleUser {
			if title := piPromptTitle(msg.Text); title != "" {
				sess.Title = canon.TruncateBytes(title, canon.SessionTitleLimit)
			}
		}
		if err := sink.Message(msg); err != nil {
			return err
		}
		if dec.ok {
			if ev, ok := stopEvent(dec.pm, msg); ok {
				if err := sink.SessionEvent(ev); err != nil {
					return err
				}
			}
		}
		messageCount++
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
	if !haveHeader {
		return nil // empty file
	}

	// Final emit: the folded title and ModifiedAt.
	return sink.Session(sess)
}

// stopEvent turns an assistant turn that ended in error or was aborted
// into a session event.
func stopEvent(pm piMessage, msg canon.Message) (canon.SessionEvent, bool) {
	ev := canon.SessionEvent{
		SessionExternalID: msg.SessionExternalID,
		MessageSeq:        msg.Seq,
		At:                msg.CreatedAt,
		Detail:            strings.TrimSpace(pm.ErrorMessage),
	}
	switch pm.StopReason {
	case "error":
		ev.Kind = canon.EventAPIError
		ev.Label = errorClass(pm.ErrorMessage)
	case "aborted":
		ev.Kind = canon.EventAborted
		ev.Label = "user"
	default:
		return canon.SessionEvent{}, false
	}
	return ev, true
}

// errorClass maps Pi's free-text provider errors onto the classes Claude
// Code records natively, so errors group the same way across agents.
func errorClass(message string) string {
	m := strings.ToLower(message)
	switch {
	case strings.Contains(m, "rate limit"), strings.Contains(m, "usage limit"), strings.Contains(m, "quota"):
		return "rate_limit"
	case strings.Contains(m, "context window"), strings.Contains(m, "context_length"):
		return "invalid_request"
	case strings.Contains(m, "oauth"), strings.Contains(m, "unauthorized"), strings.Contains(m, "api key"),
		strings.Contains(m, "authentication"):
		return "authentication_failed"
	case strings.Contains(m, "socket"), strings.Contains(m, "connection"), strings.Contains(m, "fetch failed"),
		strings.Contains(m, "unable to connect"), strings.Contains(m, "timed out"):
		return "connection"
	case strings.Contains(m, "overloaded"), strings.Contains(m, "server error"),
		strings.Contains(m, "internal error"), strings.Contains(m, "service unavailable"):
		return "server_error"
	default:
		return "error"
	}
}

// piPromptTitle rejects skill envelopes injected as synthetic user messages.
// They remain transcript provenance but do not describe the session's purpose.
func piPromptTitle(text string) string {
	title := strings.TrimSpace(text)
	if strings.HasPrefix(title, "<skill ") {
		return ""
	}
	return title
}

// decodedMessage is a "message" entry's payload decoded ONCE. The caller
// needs the same piMessage and the same content blocks convertEntry has
// already read, to emit tool calls and results; unmarshalling them again
// cost two extra decodes of every message line on the parse path.
type decodedMessage struct {
	pm     piMessage
	blocks []piBlock
	ok     bool
}

// convertEntry maps one non-header entry to a canonical message, handing
// back the decoded payload for "message" entries. Model is per-entry state
// established by model_change entries; the walk is file order, which
// matches the main path for non-branching sessions and is a documented
// approximation for branched ones.
func (a *Adapter) convertEntry(e entry, seq int, sess *canon.Session, currentProvider, currentModel *string) (canon.Message, decodedMessage, bool) {
	base := canon.Message{
		Seq:              seq,
		ExternalID:       e.ID,
		ParentExternalID: e.ParentID,
		CreatedAt:        e.Timestamp,
		Provider:         *currentProvider,
		Model:            *currentModel,
	}

	switch e.Type {
	case "message":
		var pm piMessage
		if err := json.Unmarshal(e.Message, &pm); err != nil {
			return canon.Message{}, decodedMessage{}, false
		}
		dec := decodedMessage{pm: pm, blocks: piBlocks(pm.Content), ok: true}
		if pm.Provider != "" {
			base.Provider = pm.Provider
			*currentProvider = pm.Provider
		}
		if pm.Model != "" {
			// A model-only change cannot safely inherit the old provider: the
			// pair may resolve to a provider-specific price for a backend Pi no
			// longer uses. Keep the provider only when the model is unchanged.
			if pm.Provider == "" && pm.Model != *currentModel {
				base.Provider = ""
				*currentProvider = ""
			}
			base.Model = pm.Model
			*currentModel = pm.Model
		}
		base.Kind = canon.KindMessage
		base.Role = normalizeRole(pm.Role)
		base.Content = e.Message
		base.Text = piText(pm.Content, dec.blocks)
		base.Thinking, base.Images = piThinkingAndImages(dec.blocks)
		if pm.Usage != nil {
			// Pi branches in place and forks reference their parent without
			// copying entries, so its lack of a request-id dedupe key is safe.
			base.Usage = canonicalUsage(pm.Usage)
		}
		return base, dec, true

	case "model_change":
		*currentProvider = e.Provider
		*currentModel = e.ModelID
		base.Kind = canon.KindModelChange
		base.Role = canon.RoleSystem
		base.Provider = e.Provider
		base.Model = e.ModelID
		base.Text = fmt.Sprintf("model changed to %s/%s", e.Provider, e.ModelID)
		base.Content = mustJSON(map[string]string{"provider": e.Provider, "modelId": e.ModelID})
		return base, decodedMessage{}, true

	case "compaction":
		base.Kind = canon.KindCompaction
		base.Role = canon.RoleSystem
		base.Text = e.Summary
		base.Usage = canonicalUsage(e.Usage)
		base.Content = mustJSON(map[string]any{
			"summary": e.Summary, "firstKeptEntryId": e.FirstKeptEntryID,
			"tokensBefore": e.TokensBefore,
		})
		return base, decodedMessage{}, true

	case "branch_summary":
		base.Kind = canon.KindBranchPoint
		base.Role = canon.RoleSystem
		base.Text = e.Summary
		base.Usage = canonicalUsage(e.Usage)
		base.Content = mustJSON(map[string]string{"summary": e.Summary, "fromId": e.FromID})
		return base, decodedMessage{}, true

	case "label":
		base.Kind = canon.KindInfo
		base.Role = canon.RoleSystem
		base.Text = fmt.Sprintf("label %q on %s", e.Label, e.TargetID)
		base.Content = mustJSON(map[string]string{"label": e.Label, "targetId": e.TargetID})
		return base, decodedMessage{}, true

	case "session_info":
		if name := strings.TrimSpace(e.Name); name != "" {
			sess.Title = canon.TruncateBytes(name, canon.SessionTitleLimit)
			sess.TitleOverride = true
		}
		return canon.Message{}, decodedMessage{}, false

	case "custom_message":
		base.Kind = canon.KindInfo
		base.Role = canon.RoleSystem
		base.Text = e.CustomType
		base.Content = e.Content
		return base, decodedMessage{}, true

	default:
		// "custom", "thinking_level_change", and future extension types are
		// non-transcript state; tolerate silently per the spec.
		return canon.Message{}, decodedMessage{}, false
	}
}

// piBlocks decodes a message's content array. A content field that is a
// bare string (Pi's user turns and most tool results) is recognised by
// shape rather than by attempting the array decode and letting it fail.
func piBlocks(content json.RawMessage) []piBlock {
	if jsonl.FirstByte(content) != '[' {
		return nil
	}
	var arr []piBlock
	if err := json.Unmarshal(content, &arr); err != nil {
		return nil
	}
	return arr
}

// piThinkingAndImages lifts reasoning text and image locators out of the
// decoded blocks. Pointers are relative to the message payload stored as
// Content.
func piThinkingAndImages(decoded []piBlock) (string, []canon.ImageRef) {
	var thinking []string
	var images []canon.ImageRef
	for i, b := range decoded {
		switch b.Type {
		case "thinking":
			if t := strings.TrimSpace(b.Thinking); t != "" {
				thinking = append(thinking, t)
			}
		case "image":
			images = append(images, canon.ImageRef{
				MediaType: b.MimeType,
				Pointer:   fmt.Sprintf("/content/%d/data", i),
			})
		}
	}
	return strings.Join(thinking, "\n\n"), images
}

// piText takes the content array ALREADY decoded by the caller.
func piText(content json.RawMessage, decoded []piBlock) string {
	if len(content) == 0 {
		return ""
	}
	if jsonl.FirstByte(content) == '"' {
		var s string
		if err := json.Unmarshal(content, &s); err == nil {
			return s
		}
	}
	var parts []string
	for _, b := range decoded {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}
