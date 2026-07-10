// Package opencode is the OpenCode adapter. OpenCode stores one JSON file
// per session under storage/session/<project-hash>/ses_*.json and one per
// message under storage/message/<sessionID>/msg_*.json
// (docs/v2-plan.md §6). Messages natively carry tokens (with cache
// read/write) AND a reported cost, so the cost engine's auto mode uses
// OpenCode's own figures. The project-hash directory is opaque — cwd comes
// from the session document.
package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

// Slug identifies this adapter.
const Slug = canon.AgentSlug("opencode")

const titleLimit = 200

// Adapter implements agent.Adapter for OpenCode.
type Adapter struct{}

// New returns the OpenCode adapter.
func New() *Adapter { return &Adapter{} }

// Slug implements agent.Adapter.
func (*Adapter) Slug() canon.AgentSlug { return Slug }

// RootSpec implements agent.Adapter. OPENCODE_DATA_DIR may hold a
// comma-separated list of directories.
func (*Adapter) RootSpec() agent.RootSpec {
	return agent.RootSpec{
		EnvVars:   []string{"OPENCODE_DATA_DIR"},
		EnvIsList: true,
		Defaults:  []string{"~/.local/share/opencode"},
	}
}

// Discover treats each session document as one source: a session's message
// directory is folded into its hash so message edits re-index the session.
// The SourceRef points at the session JSON; Parse locates the message dir.
func (*Adapter) Discover(ctx context.Context, root agent.Root) ([]agent.SourceRef, error) {
	sessionDir := filepath.Join(root.Path, "storage", "session")
	entries, err := os.ReadDir(sessionDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", sessionDir, err)
	}
	var refs []agent.SourceRef
	for _, dir := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !dir.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(sessionDir, dir.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			refs = append(refs, agent.SourceRef{
				Root: root,
				Path: filepath.Join(sessionDir, dir.Name(), f.Name()),
				Kind: agent.SourceFile,
			})
		}
	}
	// Message directories change independently of the session document, so
	// they are sources too (dir kind → hashed by children).
	messageDir := filepath.Join(root.Path, "storage", "message")
	if dirs, err := os.ReadDir(messageDir); err == nil {
		for _, d := range dirs {
			if d.IsDir() {
				refs = append(refs, agent.SourceRef{
					Root: root,
					Path: filepath.Join(messageDir, d.Name()),
					Kind: agent.SourceDir,
				})
			}
		}
	}
	return refs, nil
}

type sessionDoc struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Time  struct {
		Created int64 `json:"created"`
		Updated int64 `json:"updated"`
	} `json:"time"`
	Directory string `json:"directory"`
}

type messageDoc struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
	Role      string `json:"role"`
	Time      struct {
		Created int64 `json:"created"`
	} `json:"time"`
	ProviderID string   `json:"providerID"`
	ModelID    string   `json:"modelID"`
	Cost       *float64 `json:"cost"`
	Tokens     *struct {
		Input     int64 `json:"input"`
		Output    int64 `json:"output"`
		Reasoning int64 `json:"reasoning"`
		Cache     struct {
			Read  int64 `json:"read"`
			Write int64 `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
	Parts []part `json:"parts"`
}

type part struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Tool  string          `json:"tool"`
	State json.RawMessage `json:"state"`
}

// Parse handles both source shapes: a session document (emits the session
// plus its messages) and a message directory (re-emits its session by
// reading the session doc, so either changing triggers a consistent
// re-index).
func (a *Adapter) Parse(ctx context.Context, src agent.SourceRef, sink agent.RecordSink) error {
	if src.Kind == agent.SourceDir {
		// storage/message/<sessionID> changed: find its session document.
		sessionID := filepath.Base(src.Path)
		docPath, err := findSessionDoc(src.Root.Path, sessionID)
		if err != nil {
			return sink.Issue(canon.Issue{
				Agent: Slug, Severity: canon.SeverityWarn, Category: "format",
				SourcePath: src.Path,
				Detail:     fmt.Sprintf("message dir without session document: %v", err),
			})
		}
		return a.parseSession(ctx, src.Root, docPath, sink)
	}
	return a.parseSession(ctx, src.Root, src.Path, sink)
}

func (a *Adapter) parseSession(ctx context.Context, root agent.Root, docPath string, sink agent.RecordSink) error {
	data, err := os.ReadFile(docPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", docPath, err)
	}
	var doc sessionDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return sink.Issue(canon.Issue{
			Agent: Slug, Severity: canon.SeverityWarn, Category: "parse",
			SourcePath: docPath, Detail: fmt.Sprintf("invalid session JSON: %v", err),
		})
	}
	if doc.ID == "" {
		return sink.Issue(canon.Issue{
			Agent: Slug, Severity: canon.SeverityWarn, Category: "format",
			SourcePath: docPath, Detail: "session document without id",
		})
	}

	sess := canon.Session{
		Agent:      Slug,
		ExternalID: doc.ID,
		Title:      truncate(doc.Title, titleLimit),
		CreatedAt:  millis(doc.Time.Created),
		ModifiedAt: millis(doc.Time.Updated),
		CWD:        doc.Directory,
		SourcePath: docPath,
	}
	if err := sink.Session(sess); err != nil {
		return err
	}

	msgDir := filepath.Join(root.Path, "storage", "message", doc.ID)
	entries, err := os.ReadDir(msgDir)
	if os.IsNotExist(err) {
		return nil // session without messages yet
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", msgDir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // msg_<ulid> sorts chronologically

	seq := 0
	toolSeq := 0
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw, err := os.ReadFile(filepath.Join(msgDir, name))
		if err != nil {
			continue
		}
		var md messageDoc
		if err := json.Unmarshal(raw, &md); err != nil {
			if serr := sink.Issue(canon.Issue{
				Agent: Slug, Severity: canon.SeverityWarn, Category: "parse",
				SourcePath: filepath.Join(msgDir, name),
				Detail:     fmt.Sprintf("invalid message JSON: %v", err),
			}); serr != nil {
				return serr
			}
			continue
		}

		msg := canon.Message{
			SessionExternalID: doc.ID,
			Seq:               seq,
			ExternalID:        md.ID,
			ContentID:         md.ID,
			Role:              canon.Role(md.Role),
			Kind:              canon.KindMessage,
			CreatedAt:         millis(md.Time.Created),
			Model:             md.ModelID,
			CWD:               doc.Directory,
			Content:           json.RawMessage(raw),
			Text:              partsText(md.Parts),
		}
		if md.Tokens != nil {
			msg.Usage = &canon.Usage{
				InputTokens:      md.Tokens.Input,
				OutputTokens:     md.Tokens.Output,
				CacheReadTokens:  md.Tokens.Cache.Read,
				CacheWriteTokens: md.Tokens.Cache.Write,
				ReasoningTokens:  md.Tokens.Reasoning,
				ReportedCostUSD:  md.Cost,
				RequestID:        md.ID,
			}
		}
		if err := sink.Message(msg); err != nil {
			return err
		}

		for _, p := range md.Parts {
			if p.Type != "tool" || p.Tool == "" {
				continue
			}
			tc := canon.ToolCall{
				SessionExternalID: doc.ID,
				MessageSeq:        seq,
				Seq:               toolSeq,
				Name:              p.Tool,
				Kind:              normalizeTool(p.Tool),
				Input:             toolInput(p.State),
				ResultStatus:      toolStatus(p.State),
				FilePath:          toolFilePath(p.State),
				StartedAt:         millis(md.Time.Created),
			}
			if err := sink.ToolCall(tc); err != nil {
				return err
			}
			toolSeq++
		}
		seq++
	}
	return nil
}

// findSessionDoc scans storage/session/*/ for <sessionID>.json.
func findSessionDoc(rootPath, sessionID string) (string, error) {
	sessionDir := filepath.Join(rootPath, "storage", "session")
	dirs, err := os.ReadDir(sessionDir)
	if err != nil {
		return "", err
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		candidate := filepath.Join(sessionDir, d.Name(), sessionID+".json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no session document for %s", sessionID)
}

func partsText(parts []part) string {
	var out []string
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			out = append(out, p.Text)
		}
	}
	return strings.Join(out, "\n")
}

func toolInput(state json.RawMessage) json.RawMessage {
	var s struct {
		Input json.RawMessage `json:"input"`
	}
	if json.Unmarshal(state, &s) == nil && len(s.Input) > 0 {
		return s.Input
	}
	return json.RawMessage(`{}`)
}

func toolStatus(state json.RawMessage) string {
	var s struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(state, &s) != nil {
		return ""
	}
	switch s.Status {
	case "completed":
		return "ok"
	case "error":
		return "error"
	default:
		return s.Status
	}
}

func toolFilePath(state json.RawMessage) string {
	var s struct {
		Input struct {
			FilePath string `json:"filePath"`
		} `json:"input"`
	}
	_ = json.Unmarshal(state, &s)
	return s.Input.FilePath
}

func normalizeTool(name string) canon.ToolKind {
	switch name {
	case "bash":
		return canon.ToolShell
	case "read":
		return canon.ToolFileRead
	case "write":
		return canon.ToolFileWrite
	case "edit", "patch":
		return canon.ToolFileEdit
	case "grep":
		return canon.ToolSearch
	case "glob", "list":
		return canon.ToolDiscovery
	case "task", "agent":
		return canon.ToolSubagent
	case "webfetch":
		return canon.ToolWeb
	default:
		return canon.ToolOther
	}
}

func millis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
