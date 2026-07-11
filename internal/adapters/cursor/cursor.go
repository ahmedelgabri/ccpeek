// Package cursor is the Cursor CLI adapter — the launch set's only
// non-file source (docs/v2-plan.md §6): one SQLite database per session at
// chats/{workspace-hash}/{session-uuid}/store.db, with hex-encoded (not
// encrypted) JSON in `meta` and `blobs` tables. The SourceRef is the
// per-session store.db file; change detection hashes it like any other
// file, and Parse queries it as a database — the SourceDatabase path the
// adapter framework was designed around.
//
// Field mapping is fixture-based pending the real-world format spike the
// plan schedules; unknown blob shapes are tolerated silently.
package cursor

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
	_ "modernc.org/sqlite"
)

// Slug identifies this adapter.
const Slug = canon.AgentSlug("cursor")

const titleLimit = 200

// Adapter implements agent.Adapter for Cursor.
type Adapter struct{}

// New returns the Cursor adapter.
func New() *Adapter { return &Adapter{} }

// Slug implements agent.Adapter.
func (*Adapter) Slug() canon.AgentSlug { return Slug }

// RootSpec implements agent.Adapter. Cursor documents no relocation env
// var of its own, so ccpeek provides one — needed anywhere the real
// ~/.cursor must not be scanned (tests, sandboxes).
func (*Adapter) RootSpec() agent.RootSpec {
	return agent.RootSpec{
		EnvVars:  []string{"CCPEEK_CURSOR_DIR"},
		Defaults: []string{"~/.cursor"},
	}
}

// Discover walks chats/{workspace-hash}/{session-uuid}/store.db. The
// workspace-hash directory is opaque; identity is the session uuid and
// context comes from the database itself.
func (*Adapter) Discover(ctx context.Context, root agent.Root) ([]agent.SourceRef, error) {
	chatsDir := filepath.Join(root.Path, "chats")
	wsDirs, err := os.ReadDir(chatsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", chatsDir, err)
	}
	var refs []agent.SourceRef
	for _, ws := range wsDirs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !ws.IsDir() {
			continue
		}
		sessions, err := os.ReadDir(filepath.Join(chatsDir, ws.Name()))
		if err != nil {
			continue
		}
		for _, sess := range sessions {
			if !sess.IsDir() {
				continue
			}
			dbPath := filepath.Join(chatsDir, ws.Name(), sess.Name(), "store.db")
			if fi, err := os.Stat(dbPath); err == nil && !fi.IsDir() {
				refs = append(refs, agent.SourceRef{Root: root, Path: dbPath, Kind: agent.SourceDatabase})
			}
		}
	}
	return refs, nil
}

type metaDoc struct {
	Name          string `json:"name"`
	AgentID       string `json:"agentId"`
	CreatedAt     int64  `json:"createdAt"`
	WorkspaceRoot string `json:"workspaceRoot"`
}

type blobMessage struct {
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Timestamp int64           `json:"timestamp"`
	Model     string          `json:"model"`
	Usage     *blobUsage      `json:"usage"`
}

type blobUsage struct {
	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`
}

// Parse opens the per-session database read-only and emits its records.
func (a *Adapter) Parse(ctx context.Context, src agent.SourceRef, sink agent.RecordSink) error {
	sdb, err := sql.Open("sqlite", "file:"+src.Path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return fmt.Errorf("opening %s: %w", src.Path, err)
	}
	defer sdb.Close()

	// Session identity: directory name (uuid); meta enriches it.
	sessionID := filepath.Base(filepath.Dir(src.Path))
	sess := canon.Session{
		Agent:      Slug,
		ExternalID: sessionID,
		SourcePath: src.Path,
	}

	var metaHex string
	err = sdb.QueryRowContext(ctx, `SELECT value FROM meta LIMIT 1`).Scan(&metaHex)
	if err == nil {
		var meta metaDoc
		if decodeHexJSON(metaHex, &meta) == nil {
			sess.Title = truncate(meta.Name, titleLimit)
			sess.CWD = meta.WorkspaceRoot
			if meta.CreatedAt > 0 {
				sess.CreatedAt = time.UnixMilli(meta.CreatedAt).UTC()
			}
		}
	} else if !isMissingTable(err) && err != sql.ErrNoRows {
		return fmt.Errorf("reading meta from %s: %w", src.Path, err)
	}

	rows, err := sdb.QueryContext(ctx, `SELECT id, data FROM blobs ORDER BY id`)
	if err != nil {
		if isMissingTable(err) {
			return sink.Issue(canon.Issue{
				Agent: Slug, Severity: canon.SeverityWarn, Category: "format",
				SourcePath: src.Path, Detail: "store.db without a blobs table",
			})
		}
		return fmt.Errorf("reading blobs from %s: %w", src.Path, err)
	}
	defer rows.Close()

	type blobRow struct {
		id  string
		msg blobMessage
		raw []byte
	}
	var msgs []blobRow
	for rows.Next() {
		var id, dataHex string
		if err := rows.Scan(&id, &dataHex); err != nil {
			return err
		}
		raw, err := hex.DecodeString(strings.TrimSpace(dataHex))
		if err != nil {
			if serr := sink.Issue(canon.Issue{
				Agent: Slug, Severity: canon.SeverityWarn, Category: "parse",
				SourcePath: src.Path,
				Detail:     fmt.Sprintf("blob %s is not hex-encoded", id),
			}); serr != nil {
				return serr
			}
			continue
		}
		var msg blobMessage
		if json.Unmarshal(raw, &msg) != nil || msg.Role == "" {
			continue // non-message blob (checkpoints, internal state): tolerated
		}
		msgs = append(msgs, blobRow{id: id, msg: msg, raw: raw})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(msgs) == 0 {
		return nil
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].id < msgs[j].id })

	for _, b := range msgs {
		ts := time.UnixMilli(b.msg.Timestamp).UTC()
		if sess.CreatedAt.IsZero() {
			sess.CreatedAt = ts
		}
		if ts.After(sess.ModifiedAt) {
			sess.ModifiedAt = ts
		}
	}
	if sess.Title == "" {
		for _, b := range msgs {
			if b.msg.Role == "user" {
				if t := contentText(b.msg.Content); t != "" {
					sess.Title = truncate(strings.TrimSpace(t), titleLimit)
					break
				}
			}
		}
	}

	if err := sink.Session(sess); err != nil {
		return err
	}
	for seq, b := range msgs {
		msg := canon.Message{
			SessionExternalID: sessionID,
			Seq:               seq,
			ExternalID:        b.id,
			Role:              canon.Role(b.msg.Role),
			Kind:              canon.KindMessage,
			CreatedAt:         time.UnixMilli(b.msg.Timestamp).UTC(),
			Model:             b.msg.Model,
			CWD:               sess.CWD,
			Content:           json.RawMessage(b.raw),
			Text:              contentText(b.msg.Content),
		}
		if b.msg.Usage != nil {
			msg.Usage = &canon.Usage{
				InputTokens:      b.msg.Usage.InputTokens,
				OutputTokens:     b.msg.Usage.OutputTokens,
				CacheReadTokens:  b.msg.Usage.CacheReadTokens,
				CacheWriteTokens: b.msg.Usage.CacheWriteTokens,
			}
		}
		if err := sink.Message(msg); err != nil {
			return err
		}
	}
	return nil
}

func decodeHexJSON(hexStr string, v any) error {
	raw, err := hex.DecodeString(strings.TrimSpace(hexStr))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func contentText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func isMissingTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
