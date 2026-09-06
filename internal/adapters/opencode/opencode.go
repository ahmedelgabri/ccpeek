// Package opencode reads native OpenCode JSON storage and SQLite archives.
package opencode

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/sqliteutil"
	_ "modernc.org/sqlite"
)

const Slug = canon.AgentSlug("opencode")

type Adapter struct{}

func New() *Adapter                    { return &Adapter{} }
func (*Adapter) Slug() canon.AgentSlug { return Slug }
func (*Adapter) ParseVersion() int     { return 3 }
func (*Adapter) RootSpec() agent.RootSpec {
	return agent.RootSpec{EnvVars: []string{"OPENCODE_DATA_DIR"}, EnvIsList: true, Defaults: []string{"~/.local/share/opencode"}}
}

// Database sessions take precedence over leftover JSON from migration. Legacy
// sessions not present in any database are still discovered and retained.
func (*Adapter) Discover(ctx context.Context, root agent.Root) ([]agent.SourceRef, error) {
	var refs []agent.SourceRef
	migrated := map[string]bool{}
	databases, err := filepath.Glob(filepath.Join(root.Path, "opencode*.db"))
	if err != nil {
		return nil, err
	}
	for _, path := range databases {
		db, err := openDatabase(ctx, path)
		if err != nil {
			return nil, err
		}
		rows, err := db.QueryContext(ctx, `SELECT id FROM session`)
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("unsupported OpenCode database %s: %w", path, err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				db.Close()
				return nil, err
			}
			migrated[id] = true
		}
		rows.Close()
		err = rows.Err()
		db.Close()
		if err != nil {
			return nil, err
		}
		refs = append(refs, agent.SourceRef{Root: root, Path: path, Kind: agent.SourceDatabase, CompanionPaths: []string{path + "-wal"}})
	}
	sessionDir := filepath.Join(root.Path, "storage", "session")
	entries, err := os.ReadDir(sessionDir)
	if os.IsNotExist(err) {
		return refs, nil
	}
	if err != nil {
		return nil, err
	}
	for _, dir := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !dir.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(sessionDir, dir.Name()))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			id := strings.TrimSuffix(f.Name(), ".json")
			if migrated[id] {
				continue
			}
			msgDir := filepath.Join(root.Path, "storage", "message", id)
			companions := []string{msgDir}
			msgs, err := os.ReadDir(msgDir)
			if err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			for _, msg := range msgs {
				if !msg.IsDir() && strings.HasSuffix(msg.Name(), ".json") {
					companions = append(companions, filepath.Join(root.Path, "storage", "part", strings.TrimSuffix(msg.Name(), ".json")))
				}
			}
			refs = append(refs, agent.SourceRef{Root: root, Path: filepath.Join(sessionDir, dir.Name(), f.Name()), Kind: agent.SourceFile, CompanionPaths: companions})
		}
	}
	return refs, nil
}

type sessionDoc struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	ParentID  string `json:"parentID"`
	Directory string `json:"directory"`
	Time      struct {
		Created int64 `json:"created"`
		Updated int64 `json:"updated"`
	} `json:"time"`
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
	Raw    json.RawMessage `json:"-"`
	Type   string          `json:"type"`
	Text   string          `json:"text"`
	Tool   string          `json:"tool"`
	CallID string          `json:"callID"`
	State  json.RawMessage `json:"state"`
}

func (p *part) UnmarshalJSON(raw []byte) error {
	type fields part
	if err := json.Unmarshal(raw, (*fields)(p)); err != nil {
		return err
	}
	p.Raw = append(p.Raw[:0], raw...)
	return nil
}

func (p part) MarshalJSON() ([]byte, error) {
	if len(p.Raw) > 0 {
		return p.Raw, nil
	}
	type fields part
	return json.Marshal(fields(p))
}

func (a *Adapter) Parse(ctx context.Context, src agent.SourceRef, sink agent.RecordSink) error {
	if src.Kind == agent.SourceDatabase {
		return a.parseDatabase(ctx, src, sink)
	}
	data, err := os.ReadFile(src.Path)
	if err != nil {
		return err
	}
	var doc sessionDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("invalid session JSON %s: %w", src.Path, err)
	}
	if doc.ID == "" || doc.ID != strings.TrimSuffix(filepath.Base(src.Path), ".json") {
		return fmt.Errorf("session id %q disagrees with filename %s", doc.ID, src.Path)
	}
	msgDir := filepath.Join(src.Root.Path, "storage", "message", doc.ID)
	names, err := os.ReadDir(msgDir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var msgs []json.RawMessage
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		if name.IsDir() || !strings.HasSuffix(name.Name(), ".json") {
			continue
		}
		path := filepath.Join(msgDir, name.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var md messageDoc
		if err := json.Unmarshal(raw, &md); err != nil {
			return fmt.Errorf("invalid message JSON %s: %w", path, err)
		}
		if md.ID == "" {
			md.ID = strings.TrimSuffix(name.Name(), ".json")
		}
		// The filename, not an unchecked JSON id, selects a directory to read.
		partDir := filepath.Join(src.Root.Path, "storage", "part", strings.TrimSuffix(name.Name(), ".json"))
		parts, err := os.ReadDir(partDir)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if len(parts) > 0 {
			md.Parts = nil
		}
		for _, p := range parts {
			if p.IsDir() || !strings.HasSuffix(p.Name(), ".json") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(partDir, p.Name()))
			if err != nil {
				return err
			}
			var part part
			if err := json.Unmarshal(data, &part); err != nil {
				return err
			}
			md.Parts = append(md.Parts, part)
		}
		// Preserve unknown message fields; merge the native parts without losing
		// raw provenance that a newer parser or the secret scanner may need.
		raw, err = withParts(raw, md.ID, doc.ID, md.Parts)
		if err != nil {
			return err
		}
		msgs = append(msgs, raw)
	}
	return emitSession(ctx, src.Path, doc, msgs, sink)
}

func withParts(raw []byte, id, sessionID string, parts []part) ([]byte, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, fmt.Errorf("message must be an object")
	}
	object["id"], _ = json.Marshal(id)
	object["sessionID"], _ = json.Marshal(sessionID)
	object["parts"], _ = json.Marshal(parts)
	return json.Marshal(object)
}

func emitSession(ctx context.Context, source string, doc sessionDoc, raws []json.RawMessage, sink agent.RecordSink) error {
	title := strings.TrimSpace(doc.Title)
	sess := canon.Session{Agent: Slug, ExternalID: doc.ID, Title: canon.TruncateBytes(title, canon.SessionTitleLimit), TitleOverride: title != "", CreatedAt: millis(doc.Time.Created), ModifiedAt: millis(doc.Time.Updated), CWD: doc.Directory, SourcePath: source}
	if err := sink.Session(sess); err != nil {
		return err
	}
	if doc.ParentID != "" {
		if err := sink.SessionRelation(canon.SessionRelation{Agent: Slug, FromExternalID: doc.ID, ToExternalID: doc.ParentID, Kind: canon.RelForkOf}); err != nil {
			return err
		}
	}
	toolSeq := 0
	for seq, raw := range raws {
		if err := ctx.Err(); err != nil {
			return err
		}
		var md messageDoc
		if err := json.Unmarshal(raw, &md); err != nil {
			return err
		}
		if md.SessionID != "" && md.SessionID != doc.ID {
			return fmt.Errorf("message %s belongs to session %s, not %s", md.ID, md.SessionID, doc.ID)
		}
		msg := canon.Message{SessionExternalID: doc.ID, Seq: seq, ExternalID: md.ID, ContentID: md.ID, Role: canon.Role(md.Role), Kind: canon.KindMessage, CreatedAt: millis(md.Time.Created), Provider: md.ProviderID, Model: md.ModelID, CWD: doc.Directory, Content: raw, Text: partsText(md.Parts)}
		if md.Tokens != nil || md.Cost != nil {
			msg.Usage = &canon.Usage{ReportedCostUSD: md.Cost, RequestID: md.ID}
			if md.Tokens != nil {
				msg.Usage.InputTokens = md.Tokens.Input
				msg.Usage.OutputTokens = md.Tokens.Output + md.Tokens.Reasoning
				msg.Usage.ReasoningTokens = md.Tokens.Reasoning
				msg.Usage.CacheReadTokens = md.Tokens.Cache.Read
				msg.Usage.CacheWriteTokens = md.Tokens.Cache.Write
			}
		}
		if err := sink.Message(msg); err != nil {
			return err
		}
		for _, p := range md.Parts {
			if p.Type != "tool" || p.Tool == "" {
				continue
			}
			st := decodeToolState(p.State)
			tc := canon.ToolCall{SessionExternalID: doc.ID, MessageSeq: seq, Seq: toolSeq, ExternalID: p.CallID, Name: p.Tool, Kind: normalizeTool(p.Tool), Input: st.rawInput(), ResultStatus: st.status(), ResultExcerpt: canon.TruncateBytes(st.Output, canon.ToolResultExcerptLimit), FilePath: st.Args.FilePath, Command: st.Args.Command, OldText: st.Args.OldString, NewText: cmp.Or(st.Args.NewString, st.Args.Content), StartedAt: millis(md.Time.Created)}
			if err := sink.ToolCall(tc); err != nil {
				return err
			}
			toolSeq++
		}
	}
	return nil
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

type (
	opencodeToolArgs struct {
		FilePath  string `json:"filePath"`
		Command   string `json:"command"`
		OldString string `json:"oldString"`
		NewString string `json:"newString"`
		Content   string `json:"content"`
	}
	toolState struct {
		Status string           `json:"status"`
		Input  json.RawMessage  `json:"input"`
		Output string           `json:"output"`
		Args   opencodeToolArgs `json:"-"`
	}
)

func decodeToolState(raw json.RawMessage) toolState {
	var s toolState
	if json.Unmarshal(raw, &s) != nil {
		return toolState{}
	}
	_ = json.Unmarshal(s.Input, &s.Args)
	return s
}

func (s toolState) rawInput() json.RawMessage {
	if len(s.Input) > 0 {
		return s.Input
	}
	return json.RawMessage(`{}`)
}

func (s toolState) status() string {
	switch s.Status {
	case "completed":
		return "ok"
	case "error":
		return "error"
	default:
		return s.Status
	}
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

func openDatabase(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", sqliteutil.URI(path, "mode=ro&_pragma=busy_timeout(5000)"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func (a *Adapter) parseDatabase(ctx context.Context, src agent.SourceRef, sink agent.RecordSink) error {
	db, err := openDatabase(ctx, src.Path)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id,title,directory,time_created,time_updated,COALESCE(parent_id,'') FROM session ORDER BY id`)
	if err != nil {
		return fmt.Errorf("unsupported OpenCode session schema: %w", err)
	}
	var sessions []sessionDoc
	for rows.Next() {
		var d sessionDoc
		if err := rows.Scan(&d.ID, &d.Title, &d.Directory, &d.Time.Created, &d.Time.Updated, &d.ParentID); err != nil {
			rows.Close()
			return err
		}
		sessions = append(sessions, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, doc := range sessions {
		rows, err := tx.QueryContext(ctx, `SELECT id,data FROM message WHERE session_id=? ORDER BY id`, doc.ID)
		if err != nil {
			return err
		}
		rawByID := map[string]json.RawMessage{}
		var ids []string
		for rows.Next() {
			var id string
			var raw []byte
			if err := rows.Scan(&id, &raw); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
			rawByID[id] = raw
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		rows, err = tx.QueryContext(ctx, `SELECT message_id,data FROM part WHERE session_id=? ORDER BY id`, doc.ID)
		if err != nil {
			return fmt.Errorf("unsupported OpenCode part schema: %w", err)
		}
		byMessage := map[string][]part{}
		for rows.Next() {
			var id string
			var raw []byte
			if err := rows.Scan(&id, &raw); err != nil {
				rows.Close()
				return err
			}
			var p part
			if err := json.Unmarshal(raw, &p); err != nil {
				rows.Close()
				return err
			}
			byMessage[id] = append(byMessage[id], p)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		sort.Strings(ids)
		var messages []json.RawMessage
		for _, id := range ids {
			raw, err := withParts(rawByID[id], id, doc.ID, byMessage[id])
			if err != nil {
				return err
			}
			messages = append(messages, raw)
		}
		if err := emitSession(ctx, src.Path, doc, messages, sink); err != nil {
			return err
		}
	}
	return tx.Commit()
}
