// Package migrate imports the two data categories a v1 database holds that
// cannot be re-derived from sources (docs/v2-plan.md §8.1):
//
//  1. rows whose source files no longer exist on disk — v1's
//     deleted-source retention feature — imported with origin='imported-v1';
//  2. user state — scan-finding ignore flags — imported into
//     user_annotations keyed by natural keys so they re-attach after
//     re-ingest.
//
// The v1 database is opened read-only and never modified: rollback to v1
// is running the old binary.
package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
)

// claudeSlug: every v1 row is Claude Code data by definition.
const claudeSlug = canon.AgentSlug("claude-code")

// Report summarizes an import.
type Report struct {
	OrphanSessions  int `json:"orphanSessions"`
	OrphanMessages  int `json:"orphanMessages"`
	OrphanArtifacts int `json:"orphanArtifacts"`
	IgnoreFlags     int `json:"ignoreFlags"`
}

// v1ArtifactTables maps v1 content tables to artifact kinds. Each entry
// lists the columns (name, content, source path) to read.
var v1ArtifactTables = []struct {
	table   string
	kind    canon.ArtifactKind
	nameCol string
	content string
}{
	{"plans", canon.ArtifactPlan, "file_name", "content"},
	{"shell_snapshots", canon.ArtifactShellSnapshot, "file_name", "content"},
	{"paste_cache", canon.ArtifactPaste, "file_name", "content"},
}

// ImportV1 copies non-re-derivable data from a v1 database into the v2
// store. Rows whose source_path still exists on disk are skipped — the
// ingest pipeline owns them.
func ImportV1(ctx context.Context, store *db.Store, v1Path string) (*Report, error) {
	v1, err := sql.Open("sqlite", "file:"+v1Path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("opening v1 database: %w", err)
	}
	defer v1.Close()
	if err := v1.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("reading v1 database: %w", err)
	}

	report := &Report{}
	if err := importOrphanSessions(ctx, store, v1, report); err != nil {
		return nil, err
	}
	if err := importOrphanArtifacts(ctx, store, v1, report); err != nil {
		return nil, err
	}
	if err := importIgnoreFlags(ctx, store, v1, report); err != nil {
		return nil, err
	}
	return report, nil
}

func importOrphanSessions(ctx context.Context, store *db.Store, v1 *sql.DB, report *Report) error {
	rows, err := v1.QueryContext(ctx, `
		SELECT s.id, s.session_id, COALESCE(s.first_prompt, ''),
		       COALESCE(s.created_at, ''), COALESCE(s.modified_at, ''),
		       COALESCE(s.git_branch, ''), COALESCE(s.project_path, ''),
		       COALESCE(s.source_path, ''), COALESCE(p.canonical_path, '')
		FROM sessions s
		LEFT JOIN projects p ON p.id = s.project_id`)
	if err != nil {
		return fmt.Errorf("reading v1 sessions: %w", err)
	}
	defer rows.Close()

	type v1Session struct {
		rowID                     int64
		externalID, title         string
		createdAt, modifiedAt     string
		gitBranch, projectPath    string
		sourcePath, canonicalPath string
	}
	var orphans []v1Session
	for rows.Next() {
		var s v1Session
		if err := rows.Scan(&s.rowID, &s.externalID, &s.title, &s.createdAt,
			&s.modifiedAt, &s.gitBranch, &s.projectPath, &s.sourcePath,
			&s.canonicalPath); err != nil {
			return err
		}
		if s.sourcePath != "" {
			if _, err := os.Stat(s.sourcePath); err == nil {
				continue // still on disk: the pipeline owns it
			}
		}
		orphans = append(orphans, s)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, o := range orphans {
		w, err := store.BeginWrite(ctx)
		if err != nil {
			return err
		}
		cwd := o.projectPath
		if cwd == "" {
			cwd = o.canonicalPath
		}
		sess := canon.Session{
			Agent:      claudeSlug,
			ExternalID: o.externalID,
			Title:      o.title,
			CreatedAt:  parseTime(o.createdAt),
			ModifiedAt: parseTime(o.modifiedAt),
			CWD:        cwd,
			GitBranch:  o.gitBranch,
			Origin:     canon.OriginImportedV1,
			SourcePath: o.sourcePath,
		}
		sessionID, err := w.UpsertSession(sess, "imported-v1")
		if err != nil {
			w.Rollback()
			return err
		}
		if err := w.ClearSessionChildren(sessionID); err != nil {
			w.Rollback()
			return err
		}
		n, err := importMessages(ctx, v1, w, o.rowID, sessionID)
		if err != nil {
			w.Rollback()
			return fmt.Errorf("importing messages for %s: %w", o.externalID, err)
		}
		if err := w.Commit(); err != nil {
			return err
		}
		report.OrphanSessions++
		report.OrphanMessages += n
	}
	return nil
}

func importMessages(ctx context.Context, v1 *sql.DB, w *db.Writer, v1SessionID, sessionID int64) (int, error) {
	rows, err := v1.QueryContext(ctx, `
		SELECT seq, COALESCE(type, ''), COALESCE(role, ''),
		       COALESCE(timestamp, ''), COALESCE(uuid, ''),
		       COALESCE(content, ''), COALESCE(cwd, '')
		FROM messages WHERE session_id = ? ORDER BY seq`, v1SessionID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var seq int
		var typ, role, ts, uuid, content, cwd string
		if err := rows.Scan(&seq, &typ, &role, &ts, &uuid, &content, &cwd); err != nil {
			return n, err
		}
		if role == "" {
			role = typ
		}
		msg := canon.Message{
			Seq:        seq,
			ExternalID: uuid,
			Role:       canon.Role(role),
			Kind:       canon.KindMessage,
			CreatedAt:  parseTime(ts),
			CWD:        cwd,
			Content:    json.RawMessage(content),
			Text:       textFromV1Content(content),
		}
		if err := w.InsertMessage(sessionID, claudeSlug, msg); err != nil {
			return n, err
		}
		if err := w.InsertSearchDoc(sessionID, 0, "message", seq, "", msg.Text); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

func importOrphanArtifacts(ctx context.Context, store *db.Store, v1 *sql.DB, report *Report) error {
	for _, spec := range v1ArtifactTables {
		query := fmt.Sprintf(`
			SELECT %s, COALESCE(%s, ''), COALESCE(source_path, '')
			FROM %s`, spec.nameCol, spec.content, spec.table)
		rows, err := v1.QueryContext(ctx, query)
		if err != nil {
			continue // table missing in this v1 version: nothing to import
		}
		type v1Artifact struct{ name, content, sourcePath string }
		var orphans []v1Artifact
		for rows.Next() {
			var a v1Artifact
			if err := rows.Scan(&a.name, &a.content, &a.sourcePath); err != nil {
				rows.Close()
				return err
			}
			if a.sourcePath != "" {
				if _, err := os.Stat(a.sourcePath); err == nil {
					continue
				}
			}
			orphans = append(orphans, a)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, o := range orphans {
			w, err := store.BeginWrite(ctx)
			if err != nil {
				return err
			}
			meta, _ := json.Marshal(map[string]bool{"importedV1": true})
			id, err := w.UpsertArtifact(canon.Artifact{
				Agent:      claudeSlug,
				Kind:       spec.kind,
				Name:       o.name,
				Content:    o.content,
				Metadata:   meta,
				SourcePath: o.sourcePath,
			}, "imported-v1")
			if err != nil {
				w.Rollback()
				return err
			}
			if err := w.ClearArtifactSearchDocs(id); err != nil {
				w.Rollback()
				return err
			}
			if err := w.InsertSearchDoc(0, id, string(spec.kind), 0, o.name, o.content); err != nil {
				w.Rollback()
				return err
			}
			if err := w.Commit(); err != nil {
				return err
			}
			report.OrphanArtifacts++
		}
	}
	return nil
}

// importIgnoreFlags carries the user's scan-ignore decisions over,
// TRANSLATING v1 finding identities into the keys the v2 scanner
// resolves — a verbatim copy would never re-attach, silently reviving
// every dismissed secret.
//
// v1 identities:
//   - message/command: source_id "<session-uuid>@<timestamp>", line = a
//     detector line inside that one message's content;
//   - file_history (and other sidecars): source_id names the artifact.
//
// v2 keys: "message/<agent>/<session>/<rule>/<seq>" for messages and
// "artifact/<agent>/<kind>/<name>/<rule>/<line>" for artifacts. The
// message translation resolves the v2 seq by matching the v1 timestamp
// against the imported/ingested messages (seconds precision — v2 stores
// RFC3339 without sub-second parts). Where no message matches, and for
// artifact findings whose v1 line numbering has no v2 equivalent, a
// rule-scoped wildcard key ("…/<rule>/*") preserves the user's intent:
// they dismissed this rule on this entity.
func importIgnoreFlags(ctx context.Context, store *db.Store, v1 *sql.DB, report *Report) error {
	rows, err := v1.QueryContext(ctx, `
		SELECT DISTINCT rule_id, source_type, source_id
		FROM scan_findings WHERE ignored = 1`)
	if err != nil {
		return nil // no scan_findings table or no ignored column: fine
	}
	defer rows.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	insert := func(key string) error {
		_, err := store.DB().ExecContext(ctx, `
			INSERT INTO user_annotations (entity_type, natural_key, kind, value_json, created_at)
			VALUES ('scan_finding', ?, 'scan_ignore', '{}', ?)
			ON CONFLICT(entity_type, natural_key, kind) DO NOTHING`, key, now)
		return err
	}
	for rows.Next() {
		var ruleID, sourceType, sourceID string
		if err := rows.Scan(&ruleID, &sourceType, &sourceID); err != nil {
			return err
		}
		var keys []string
		switch sourceType {
		case "message", "command":
			// v1 scanned commands out of the same transcript entries the
			// v2 scanner covers as messages.
			session, ts, ok := splitV1MessageID(sourceID)
			base := fmt.Sprintf("message/%s/%s/%s", claudeSlug, session, ruleID)
			if ok {
				for _, seq := range messageSeqsAt(ctx, store, session, ts) {
					keys = append(keys, fmt.Sprintf("%s/%d", base, seq))
				}
			}
			if len(keys) == 0 {
				keys = append(keys, base+"/*")
			}
		case "file_history":
			keys = append(keys, fmt.Sprintf("artifact/%s/file_history/%s/%s/*",
				claudeSlug, sourceID, ruleID))
		default:
			// Sidecar kinds map 1:1 onto v2 artifact kinds by name.
			keys = append(keys, fmt.Sprintf("artifact/%s/%s/%s/%s/*",
				claudeSlug, sourceType, sourceID, ruleID))
		}
		for _, key := range keys {
			if err := insert(key); err != nil {
				return fmt.Errorf("importing ignore flag: %w", err)
			}
		}
		report.IgnoreFlags++
	}
	return rows.Err()
}

// splitV1MessageID splits a v1 "<session-uuid>@<timestamp>" identity.
func splitV1MessageID(id string) (session, ts string, ok bool) {
	i := strings.LastIndexByte(id, '@')
	if i <= 0 || i == len(id)-1 {
		return id, "", false
	}
	return id[:i], id[i+1:], true
}

// messageSeqsAt resolves the v2 seq(s) of the message a v1 finding
// pointed at, matching created_at at seconds precision (several entries
// can share a second; ignoring each sibling errs on the user's side).
func messageSeqsAt(ctx context.Context, store *db.Store, session, v1ts string) []int {
	t, err := time.Parse(time.RFC3339Nano, v1ts)
	if err != nil {
		return nil
	}
	rows, err := store.DB().QueryContext(ctx, `
		SELECT m.seq FROM messages m
		JOIN sessions s ON s.id = m.session_id
		JOIN agents a ON a.id = s.agent_id
		WHERE a.slug = ? AND s.external_id = ? AND m.created_at = ?`,
		string(claudeSlug), session, t.UTC().Truncate(time.Second).Format(time.RFC3339))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var seqs []int
	for rows.Next() {
		var seq int
		if err := rows.Scan(&seq); err != nil {
			return seqs
		}
		seqs = append(seqs, seq)
	}
	return seqs
}

func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// textFromV1Content extracts searchable text from a v1 message payload
// (raw Anthropic message JSON: string content or content blocks).
func textFromV1Content(content string) string {
	var payload struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal([]byte(content), &payload) != nil || len(payload.Content) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(payload.Content, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(payload.Content, &blocks) != nil {
		return ""
	}
	out := ""
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			if out != "" {
				out += "\n"
			}
			out += b.Text
		}
	}
	return out
}
