// Package pi is the Pi adapter (badlogic/pi-mono coding agent). Pi is the
// rare agent with a documented, versioned session format
// (packages/coding-agent/docs/session-format.md): JSONL files whose first
// line is a session header and whose entries form a tree via id/parentId,
// enabling in-place branching. Assistant entries carry token usage AND a
// pre-computed cost breakdown — the first consumer of the cost engine's
// reported-vs-calculated mode (docs/v2-plan.md §6).
package pi

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
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
	Summary          string `json:"summary"`
	FirstKeptEntryID string `json:"firstKeptEntryId"`
	TokensBefore     int64  `json:"tokensBefore"`
	FromID           string `json:"fromId"`

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
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Usage   *piUsage        `json:"usage"`

	// role == "toolResult": the outcome of an earlier toolCall block,
	// delivered as its own message entry.
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	IsError    bool   `json:"isError"`
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

type piBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`

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
	default:
		return canon.ToolOther
	}
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
}

func toolArgs(arguments json.RawMessage) piToolArgs {
	var a piToolArgs
	_ = json.Unmarshal(arguments, &a)
	return a
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
		sess         canon.Session
		haveHeader   bool
		messageCount int
		toolCount    int
		currentModel string
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

		msg, dec, ok := a.convertEntry(e, messageCount, &sess, &currentModel)
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
				}); err != nil {
					return err
				}
			default:
				for _, b := range dec.blocks {
					if b.Type != "toolCall" {
						continue
					}
					args := toolArgs(b.Arguments)
					if err := sink.ToolCall(canon.ToolCall{
						SessionExternalID: sess.ExternalID,
						MessageSeq:        msg.Seq,
						Seq:               toolCount,
						ExternalID:        b.ID,
						Name:              b.Name,
						Kind:              normalizeTool(b.Name),
						Input:             b.Arguments,
						FilePath:          args.Path,
						Command:           args.Command,
						OldText:           args.OldText,
						NewText:           cmp.Or(args.NewText, args.Content),
						StartedAt:         e.Timestamp,
					}); err != nil {
						return err
					}
					toolCount++
				}
			}
		}
		if sess.Title == "" && msg.Role == canon.RoleUser && msg.Text != "" {
			sess.Title = canon.TruncateBytes(strings.TrimSpace(msg.Text), canon.SessionTitleLimit)
		}
		if err := sink.Message(msg); err != nil {
			return err
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
func (a *Adapter) convertEntry(e entry, seq int, sess *canon.Session, currentModel *string) (canon.Message, decodedMessage, bool) {
	base := canon.Message{
		Seq:              seq,
		ExternalID:       e.ID,
		ParentExternalID: e.ParentID,
		CreatedAt:        e.Timestamp,
		Model:            *currentModel,
	}

	switch e.Type {
	case "message":
		var pm piMessage
		if err := json.Unmarshal(e.Message, &pm); err != nil {
			return canon.Message{}, decodedMessage{}, false
		}
		dec := decodedMessage{pm: pm, blocks: piBlocks(pm.Content), ok: true}
		base.Kind = canon.KindMessage
		base.Role = canon.Role(pm.Role)
		base.Content = e.Message
		base.Text = piText(pm.Content, dec.blocks)
		if pm.Usage != nil {
			usage := &canon.Usage{
				InputTokens:      pm.Usage.Input,
				OutputTokens:     pm.Usage.Output,
				CacheReadTokens:  pm.Usage.CacheRead,
				CacheWriteTokens: pm.Usage.CacheWrite,
			}
			if pm.Usage.Cost != nil {
				total := pm.Usage.Cost.Total
				usage.ReportedCostUSD = &total
			}
			base.Usage = usage
		}
		return base, dec, true

	case "model_change":
		*currentModel = e.ModelID
		base.Kind = canon.KindModelChange
		base.Role = canon.RoleSystem
		base.Model = e.ModelID
		base.Text = fmt.Sprintf("model changed to %s/%s", e.Provider, e.ModelID)
		base.Content = mustJSON(map[string]string{"provider": e.Provider, "modelId": e.ModelID})
		return base, decodedMessage{}, true

	case "compaction":
		base.Kind = canon.KindCompaction
		base.Role = canon.RoleSystem
		base.Text = e.Summary
		base.Content = mustJSON(map[string]any{
			"summary": e.Summary, "firstKeptEntryId": e.FirstKeptEntryID,
			"tokensBefore": e.TokensBefore,
		})
		return base, decodedMessage{}, true

	case "branch_summary":
		base.Kind = canon.KindBranchPoint
		base.Role = canon.RoleSystem
		base.Text = e.Summary
		base.Content = mustJSON(map[string]string{"summary": e.Summary, "fromId": e.FromID})
		return base, decodedMessage{}, true

	case "label":
		base.Kind = canon.KindInfo
		base.Role = canon.RoleSystem
		base.Text = fmt.Sprintf("label %q on %s", e.Label, e.TargetID)
		base.Content = mustJSON(map[string]string{"label": e.Label, "targetId": e.TargetID})
		return base, decodedMessage{}, true

	case "session_info":
		sess.Title = canon.TruncateBytes(e.Name, canon.SessionTitleLimit)
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
