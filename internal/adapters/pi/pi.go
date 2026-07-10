// Package pi is the Pi adapter (badlogic/pi-mono coding agent). Pi is the
// rare agent with a documented, versioned session format
// (packages/coding-agent/docs/session-format.md): JSONL files whose first
// line is a session header and whose entries form a tree via id/parentId,
// enabling in-place branching. Assistant entries carry token usage AND a
// pre-computed cost breakdown — the first consumer of the cost engine's
// reported-vs-calculated mode (docs/v2-plan.md §6).
package pi

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
const Slug = canon.AgentSlug("pi")

const (
	maxLineBytes = 10 * 1024 * 1024
	titleLimit   = 200
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

	var (
		sess         canon.Session
		haveHeader   bool
		messages     []canon.Message
		relation     *canon.SessionRelation
		currentModel string
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
		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			if serr := sink.Issue(canon.Issue{
				Agent: Slug, Severity: canon.SeverityWarn, Category: "parse",
				SourcePath: src.Path, Line: lineNo,
				Detail: fmt.Sprintf("skipping unparseable line: %v", err),
			}); serr != nil {
				return serr
			}
			continue
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
			if e.ParentSession != "" {
				relation = &canon.SessionRelation{
					Agent:          Slug,
					FromExternalID: e.ID,
					ToExternalID:   e.ParentSession,
					Kind:           canon.RelForkOf,
					Evidence:       json.RawMessage(`{"source":"header.parentSession"}`),
				}
			}
			continue
		}

		if !e.Timestamp.IsZero() {
			sess.ModifiedAt = e.Timestamp
		}

		msg, ok := a.convertEntry(e, len(messages), &sess, &currentModel)
		if !ok {
			continue
		}
		msg.SessionExternalID = sess.ExternalID
		messages = append(messages, msg)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanning %s: %w", src.Path, err)
	}
	if !haveHeader {
		return nil // empty file
	}

	if sess.Title == "" {
		for _, m := range messages {
			if m.Role == canon.RoleUser && m.Text != "" {
				sess.Title = truncate(strings.TrimSpace(m.Text), titleLimit)
				break
			}
		}
	}

	if err := sink.Session(sess); err != nil {
		return err
	}
	if relation != nil {
		if err := sink.SessionRelation(*relation); err != nil {
			return err
		}
	}
	for _, m := range messages {
		if err := sink.Message(m); err != nil {
			return err
		}
	}
	return nil
}

// convertEntry maps one non-header entry to a canonical message. Model is
// per-entry state established by model_change entries; the walk is file
// order, which matches the main path for non-branching sessions and is a
// documented approximation for branched ones.
func (a *Adapter) convertEntry(e entry, seq int, sess *canon.Session, currentModel *string) (canon.Message, bool) {
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
			return canon.Message{}, false
		}
		base.Kind = canon.KindMessage
		base.Role = canon.Role(pm.Role)
		base.Content = e.Message
		base.Text = piText(pm.Content)
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
		return base, true

	case "model_change":
		*currentModel = e.ModelID
		base.Kind = canon.KindModelChange
		base.Role = canon.RoleSystem
		base.Model = e.ModelID
		base.Text = fmt.Sprintf("model changed to %s/%s", e.Provider, e.ModelID)
		base.Content = mustJSON(map[string]string{"provider": e.Provider, "modelId": e.ModelID})
		return base, true

	case "compaction":
		base.Kind = canon.KindCompaction
		base.Role = canon.RoleSystem
		base.Text = e.Summary
		base.Content = mustJSON(map[string]any{
			"summary": e.Summary, "firstKeptEntryId": e.FirstKeptEntryID,
			"tokensBefore": e.TokensBefore,
		})
		return base, true

	case "branch_summary":
		base.Kind = canon.KindBranchPoint
		base.Role = canon.RoleSystem
		base.Text = e.Summary
		base.Content = mustJSON(map[string]string{"summary": e.Summary, "fromId": e.FromID})
		return base, true

	case "label":
		base.Kind = canon.KindInfo
		base.Role = canon.RoleSystem
		base.Text = fmt.Sprintf("label %q on %s", e.Label, e.TargetID)
		base.Content = mustJSON(map[string]string{"label": e.Label, "targetId": e.TargetID})
		return base, true

	case "session_info":
		sess.Title = truncate(e.Name, titleLimit)
		return canon.Message{}, false

	case "custom_message":
		base.Kind = canon.KindInfo
		base.Role = canon.RoleSystem
		base.Text = e.CustomType
		base.Content = e.Content
		return base, true

	default:
		// "custom", "thinking_level_change", and future extension types are
		// non-transcript state; tolerate silently per the spec.
		return canon.Message{}, false
	}
}

func piText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}
	var arr []piBlock
	if err := json.Unmarshal(content, &arr); err != nil {
		return ""
	}
	var parts []string
	for _, b := range arr {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}
