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
	"regexp"
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
	OrphanToolCalls int `json:"orphanToolCalls"`
	OrphanArtifacts int `json:"orphanArtifacts"`
	HistoryEntries  int `json:"historyEntries"`
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

// v1Schema records the tables and columns this particular v1 database
// actually has — the v1 schema evolved from v4 through v15, and any of
// those vintages can be sitting on disk. Import queries only name what
// exists: a missing TABLE means the feature postdates this database
// (nothing to import), a missing COLUMN narrows the SELECT, and every
// other error is real and propagates instead of silently skipping.
type v1Schema map[string]map[string]bool

func loadV1Schema(ctx context.Context, v1 *sql.DB) (v1Schema, error) {
	rows, err := v1.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		return nil, fmt.Errorf("reading v1 schema: %w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sch := v1Schema{}
	for _, name := range names {
		crows, err := v1.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%q)", name))
		if err != nil {
			return nil, fmt.Errorf("reading v1 columns of %s: %w", name, err)
		}
		cols := map[string]bool{}
		for crows.Next() {
			var cid, notNull, pk int
			var colName, colType string
			var dflt any
			if err := crows.Scan(&cid, &colName, &colType, &notNull, &dflt, &pk); err != nil {
				crows.Close()
				return nil, err
			}
			cols[colName] = true
		}
		crows.Close()
		if err := crows.Err(); err != nil {
			return nil, err
		}
		sch[name] = cols
	}
	return sch, nil
}

func (s v1Schema) table(name string) bool {
	_, ok := s[name]
	return ok
}

func (s v1Schema) has(table, column string) bool {
	return s[table][column]
}

// sel builds the SELECT expression for a column older vintages may
// lack: the COALESCEd column when present, ” otherwise. alias is the
// query's table alias including the dot ("s."), or "".
func (s v1Schema) sel(table, alias, column string) string {
	if s.has(table, column) {
		return "COALESCE(" + alias + column + ", '')"
	}
	return "''"
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
	sch, err := loadV1Schema(ctx, v1)
	if err != nil {
		return nil, err
	}
	// Every supported v1 vintage (v4+) has these two; their absence means
	// this is not a ccpeek v1 database at all.
	if !sch.table("sessions") || !sch.table("messages") {
		return nil, fmt.Errorf("%s is not a ccpeek v1 database (no sessions/messages tables)", v1Path)
	}

	report := &Report{}
	if err := importOrphanSessions(ctx, store, v1, sch, report); err != nil {
		return nil, err
	}
	if err := importOrphanArtifacts(ctx, store, v1, sch, report); err != nil {
		return nil, err
	}
	if err := importHistory(ctx, store, v1, sch, report); err != nil {
		return nil, err
	}
	if err := importIgnoreFlags(ctx, store, v1, sch, report); err != nil {
		return nil, err
	}
	return report, nil
}

// v2HasSession reports whether the store already holds the session —
// the only reliable skip test: an on-disk source outside the detected
// roots (or one that failed to parse) must still be rescued, and a
// re-ingested one must not be overwritten with v1's lossier shape.
func v2HasSession(ctx context.Context, store *db.Store, externalID string) bool {
	var one int
	err := store.DB().QueryRowContext(ctx, `
		SELECT 1 FROM sessions s JOIN agents a ON a.id = s.agent_id
		WHERE a.slug = ? AND s.external_id = ? AND s.origin <> 'imported-v1'`,
		string(claudeSlug), externalID).Scan(&one)
	return err == nil
}

func v2HasArtifact(ctx context.Context, store *db.Store, kind canon.ArtifactKind, name string) bool {
	var one int
	err := store.DB().QueryRowContext(ctx, `
		SELECT 1 FROM artifacts ar JOIN agents a ON a.id = ar.agent_id
		WHERE a.slug = ? AND ar.kind = ? AND ar.name = ? AND ar.content_hash <> 'imported-v1'`,
		string(claudeSlug), string(kind), name).Scan(&one)
	return err == nil
}

func importOrphanSessions(ctx context.Context, store *db.Store, v1 *sql.DB, sch v1Schema, report *Report) error {
	// sessions.source_path arrived in v5 and projects.canonical_path in a
	// later vintage; older databases import with those fields empty (they
	// never held them).
	rows, err := v1.QueryContext(ctx, fmt.Sprintf(`
		SELECT s.id, s.session_id, COALESCE(s.first_prompt, ''),
		       COALESCE(s.created_at, ''), COALESCE(s.modified_at, ''),
		       COALESCE(s.git_branch, ''), COALESCE(s.project_path, ''),
		       %s, %s
		FROM sessions s
		LEFT JOIN projects p ON p.id = s.project_id`,
		sch.sel("sessions", "s.", "source_path"),
		sch.sel("projects", "p.", "canonical_path")))
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
		if v2HasSession(ctx, store, s.externalID) {
			continue // the pipeline ingested it from disk; v2 owns it
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
		tc, err := importToolCalls(ctx, v1, sch, w, o.rowID, sessionID)
		if err != nil {
			w.Rollback()
			return fmt.Errorf("importing tool calls for %s: %w", o.externalID, err)
		}
		if err := w.Commit(); err != nil {
			return err
		}
		report.OrphanSessions++
		report.OrphanMessages += n
		report.OrphanToolCalls += tc
	}
	return nil
}

// v1ToolKind maps v1's tool taxonomy onto v2's; the two differ only in
// the discovery and subagent names.
func v1ToolKind(kind string) canon.ToolKind {
	switch kind {
	case "file_discovery":
		return canon.ToolDiscovery
	case "task":
		return canon.ToolSubagent
	case "shell", "file_read", "file_write", "file_edit", "search", "web":
		return canon.ToolKind(kind)
	default:
		return canon.ToolOther
	}
}

// importToolCalls copies an orphan session's tool calls — v2 derives
// tool calls (and the commands browser) from session sources, which for
// orphans no longer exist. When the tool_calls table exists, v1's
// commands table needs no separate pass: its rows are the shell subset
// of these tool calls. Vintages before tool_calls only have that
// commands table, so those rows import as shell tool calls instead.
func importToolCalls(ctx context.Context, v1 *sql.DB, sch v1Schema, w *db.Writer, v1SessionID, sessionID int64) (int, error) {
	if !sch.table("tool_calls") {
		return importLegacyCommands(ctx, v1, sch, w, v1SessionID, sessionID)
	}
	rows, err := v1.QueryContext(ctx, fmt.Sprintf(`
		SELECT seq, COALESCE(timestamp, ''), COALESCE(tool_name, ''),
		       COALESCE(tool_kind, ''), COALESCE(input_json, '{}'),
		       COALESCE(result_text, ''), %s
		FROM tool_calls WHERE session_id = ? ORDER BY seq`,
		sch.sel("tool_calls", "", "file_path")), v1SessionID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var seq int
		var ts, name, kind, input, result, filePath string
		if err := rows.Scan(&seq, &ts, &name, &kind, &input, &result, &filePath); err != nil {
			return n, err
		}
		// Rune-safe, and the same bound every adapter applies.
		result = canon.TruncateBytes(result, canon.ToolResultExcerptLimit)
		if err := w.InsertToolCall(sessionID, canon.ToolCall{
			Seq:           seq,
			Name:          name,
			Kind:          v1ToolKind(kind),
			Input:         json.RawMessage(input),
			ResultExcerpt: result,
			FilePath:      filePath,
			StartedAt:     parseTime(ts),
		}); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

// importLegacyCommands rescues the commands table of vintages that
// predate tool_calls. v1 stored the command as plain text, which is
// exactly what canon.ToolCall.Command holds — the input JSON is
// reconstructed alongside it only so the row's native shape is not empty.
func importLegacyCommands(ctx context.Context, v1 *sql.DB, sch v1Schema, w *db.Writer, v1SessionID, sessionID int64) (int, error) {
	if !sch.table("commands") {
		return 0, nil
	}
	rows, err := v1.QueryContext(ctx, `
		SELECT seq, COALESCE(timestamp, ''), COALESCE(command, '')
		FROM commands WHERE session_id = ? ORDER BY seq`, v1SessionID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var seq int
		var ts, command string
		if err := rows.Scan(&seq, &ts, &command); err != nil {
			return n, err
		}
		if command == "" {
			continue
		}
		input, _ := json.Marshal(map[string]string{"command": command})
		if err := w.InsertToolCall(sessionID, canon.ToolCall{
			Seq:       seq,
			Name:      "Bash",
			Kind:      canon.ToolShell,
			Input:     input,
			Command:   command,
			StartedAt: parseTime(ts),
		}); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

// importHistory copies prompt-history rows whose entries v2 does not
// already hold (the live history.jsonl re-ingests natively; v1 retains
// entries from files that are gone). The v1 source path is preserved so
// the history replacement logic never collides with live sources.
func importHistory(ctx context.Context, store *db.Store, v1 *sql.DB, sch v1Schema, report *Report) error {
	if !sch.table("history") {
		return nil // this vintage predates prompt-history retention
	}
	rows, err := v1.QueryContext(ctx, fmt.Sprintf(`
		SELECT COALESCE(display, ''), COALESCE(timestamp, 0), %s
		FROM history WHERE display <> ''`,
		sch.sel("history", "", "source_path")))
	if err != nil {
		return err
	}
	defer rows.Close()
	type v1History struct {
		display, sourcePath string
		ts                  int64
	}
	var entries []v1History
	for rows.Next() {
		var h v1History
		if err := rows.Scan(&h.display, &h.ts, &h.sourcePath); err != nil {
			return err
		}
		entries = append(entries, h)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}

	w, err := store.BeginWrite(ctx)
	if err != nil {
		return err
	}
	defer w.Rollback()
	// Existence checks go through the read pool: the open write
	// transaction holds the store's only writer connection, so querying
	// the writer here would deadlock. The seen set additionally collapses
	// duplicates within the v1 rows themselves.
	seen := map[string]bool{}
	for _, h := range entries {
		dupeKey := fmt.Sprintf("%s\x00%d", h.display, h.ts)
		if seen[dupeKey] {
			continue
		}
		seen[dupeKey] = true
		var one int
		err := store.ReadDB().QueryRowContext(ctx, `
			SELECT 1 FROM history h JOIN agents a ON a.id = h.agent_id
			WHERE a.slug = ? AND h.display = ? AND h.timestamp = ?`,
			string(claudeSlug), h.display, h.ts).Scan(&one)
		if err == nil {
			continue // v2 already has this entry (re-ingested or re-imported)
		}
		src := h.sourcePath
		if src == "" {
			src = "imported-v1"
		}
		if err := w.InsertHistory(canon.HistoryEntry{
			Agent:     claudeSlug,
			Display:   h.display,
			Timestamp: time.UnixMilli(h.ts),
		}, src); err != nil {
			return err
		}
		report.HistoryEntries++
	}
	return w.Commit()
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
		if err := w.WriteMessage(sessionID, claudeSlug, msg); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

func importOrphanArtifacts(ctx context.Context, store *db.Store, v1 *sql.DB, sch v1Schema, report *Report) error {
	for _, spec := range v1ArtifactTables {
		if !sch.table(spec.table) {
			continue // this vintage predates the sidecar
		}
		// source_path is absent from some vintages (v14 plans, for one).
		query := fmt.Sprintf(`
			SELECT %s, COALESCE(%s, ''), %s
			FROM %s`, spec.nameCol, spec.content,
			sch.sel(spec.table, "", "source_path"), spec.table)
		rows, err := v1.QueryContext(ctx, query)
		if err != nil {
			return fmt.Errorf("reading v1 %s: %w", spec.table, err)
		}
		type v1Artifact struct{ name, content, sourcePath string }
		var orphans []v1Artifact
		for rows.Next() {
			var a v1Artifact
			if err := rows.Scan(&a.name, &a.content, &a.sourcePath); err != nil {
				rows.Close()
				return err
			}
			if v2HasArtifact(ctx, store, spec.kind, a.name) {
				continue // ingested from disk; v2 owns it
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
			if _, _, err := w.WriteArtifact(canon.Artifact{
				Agent:      claudeSlug,
				Kind:       spec.kind,
				Name:       o.name,
				Content:    o.content,
				Metadata:   meta,
				SourcePath: o.sourcePath,
			}, "imported-v1"); err != nil {
				w.Rollback()
				return err
			}
			if err := w.Commit(); err != nil {
				return err
			}
			report.OrphanArtifacts++
		}
	}
	return importStructuredArtifacts(ctx, store, v1, sch, report)
}

// structuredArtifact is one assembled v1 sidecar row of a kind whose v2
// artifact carries structured metadata and (usually) a session link.
type structuredArtifact struct {
	kind     canon.ArtifactKind
	name     string
	content  string
	metadata []byte
	linkTo   string // session external id, "" for none
	relation canon.LinkRelation
	evidence canon.LinkEvidence
}

// importStructuredArtifacts covers the retained v1 sidecars beyond the
// content-only tables: todos, task groups, file history, usage facets,
// the usage report, and memories — each rebuilt into the exact shape the
// v2 adapter produces, so the UI and resolvers treat imported and
// ingested rows identically. A vintage that predates a table skips it;
// any other query error is real and aborts the import.
func importStructuredArtifacts(ctx context.Context, store *db.Store, v1 *sql.DB, sch v1Schema, report *Report) error {
	collectors := []func(context.Context, *sql.DB, v1Schema) ([]structuredArtifact, error){
		collectV1Todos,
		collectV1TaskGroups,
		collectV1FileHistory,
		collectV1UsageFacets,
		collectV1UsageReport,
		collectV1Memories,
	}
	for _, collect := range collectors {
		arts, err := collect(ctx, v1, sch)
		if err != nil {
			return err
		}
		for _, a := range arts {
			if v2HasArtifact(ctx, store, a.kind, a.name) {
				continue
			}
			w, err := store.BeginWrite(ctx)
			if err != nil {
				return err
			}
			id, _, err := w.WriteArtifact(canon.Artifact{
				Agent:    claudeSlug,
				Kind:     a.kind,
				Name:     a.name,
				Content:  a.content,
				Metadata: a.metadata,
			}, "imported-v1")
			if err != nil {
				w.Rollback()
				return err
			}
			if a.linkTo != "" {
				// Unresolvable targets park as pending links, exactly like
				// the ingest sink.
				if _, err := w.LinkArtifact(id, canon.ArtifactLink{
					Agent:             claudeSlug,
					ArtifactKind:      a.kind,
					ArtifactName:      a.name,
					SessionExternalID: a.linkTo,
					Relation:          a.relation,
					Evidence:          a.evidence,
				}); err != nil {
					w.Rollback()
					return err
				}
			}
			if err := w.Commit(); err != nil {
				return err
			}
			report.OrphanArtifacts++
		}
	}
	return nil
}

func collectV1Todos(ctx context.Context, v1 *sql.DB, sch v1Schema) ([]structuredArtifact, error) {
	if !sch.table("todos") || !sch.table("todo_items") {
		return nil, nil
	}
	rows, err := v1.QueryContext(ctx, `
		SELECT t.id, t.file_name FROM todos t ORDER BY t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type todo struct {
		id   int64
		name string
	}
	var todos []todo
	for rows.Next() {
		var t todo
		if err := rows.Scan(&t.id, &t.name); err != nil {
			return nil, err
		}
		todos = append(todos, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []structuredArtifact
	for _, t := range todos {
		irows, err := v1.QueryContext(ctx, `
			SELECT COALESCE(content,''), COALESCE(status,''), COALESCE(active_form,'')
			FROM todo_items WHERE todo_id = ? ORDER BY seq`, t.id)
		if err != nil {
			return nil, err
		}
		type item struct {
			Content    string `json:"content"`
			Status     string `json:"status"`
			ActiveForm string `json:"activeForm,omitempty"`
		}
		var items []item
		var texts []string
		for irows.Next() {
			var it item
			if err := irows.Scan(&it.Content, &it.Status, &it.ActiveForm); err != nil {
				irows.Close()
				return nil, err
			}
			items = append(items, it)
			if it.Content != "" {
				texts = append(texts, it.Content)
			}
		}
		irows.Close()
		if err := irows.Err(); err != nil {
			return nil, err
		}
		if len(items) == 0 {
			continue // empty todo lists are noise, matching ingest
		}
		meta, _ := json.Marshal(items)
		a := structuredArtifact{
			kind: canon.ArtifactTodoList, name: t.name,
			content: strings.Join(texts, "\n"), metadata: meta,
		}
		if m := todoFileRe.FindStringSubmatch(t.name); m != nil {
			a.linkTo = m[1]
			a.relation, a.evidence = canon.LinkProducedBy, canon.EvidenceFilenameUUID
		}
		out = append(out, a)
	}
	return out, nil
}

func collectV1TaskGroups(ctx context.Context, v1 *sql.DB, sch v1Schema) ([]structuredArtifact, error) {
	if !sch.table("task_groups") || !sch.table("task_items") {
		return nil, nil
	}
	rows, err := v1.QueryContext(ctx, `
		SELECT g.id, g.dir_name FROM task_groups g ORDER BY g.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type group struct {
		id   int64
		name string
	}
	var groups []group
	for rows.Next() {
		var g group
		if err := rows.Scan(&g.id, &g.name); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []structuredArtifact
	for _, g := range groups {
		irows, err := v1.QueryContext(ctx, `
			SELECT COALESCE(item_id,''), COALESCE(subject,''), COALESCE(description,''),
			       COALESCE(active_form,''), COALESCE(status,''),
			       COALESCE(blocks,'[]'), COALESCE(blocked_by,'[]')
			FROM task_items WHERE task_group_id = ? ORDER BY seq`, g.id)
		if err != nil {
			return nil, err
		}
		var items []json.RawMessage
		var texts []string
		for irows.Next() {
			var itemID, subject, desc, active, status, blocks, blockedBy string
			if err := irows.Scan(&itemID, &subject, &desc, &active, &status, &blocks, &blockedBy); err != nil {
				irows.Close()
				return nil, err
			}
			raw, _ := json.Marshal(map[string]any{
				"id": itemID, "subject": subject, "description": desc,
				"activeForm": active, "status": status,
				"blocks": json.RawMessage(blocks), "blockedBy": json.RawMessage(blockedBy),
			})
			items = append(items, raw)
			texts = append(texts, strings.TrimSpace(subject+" "+desc))
		}
		irows.Close()
		if err := irows.Err(); err != nil {
			return nil, err
		}
		if len(items) == 0 {
			continue
		}
		meta, _ := json.Marshal(map[string]any{"items": items})
		out = append(out, structuredArtifact{
			kind: canon.ArtifactTaskGroup, name: g.name,
			content: strings.Join(texts, "\n"), metadata: meta,
			linkTo:   g.name, // task dirs are named by the spawning session
			relation: canon.LinkProducedBy, evidence: canon.EvidenceIDMatch,
		})
	}
	return out, nil
}

func collectV1FileHistory(ctx context.Context, v1 *sql.DB, sch v1Schema) ([]structuredArtifact, error) {
	if !sch.table("file_history") || !sch.table("file_versions") {
		return nil, nil
	}
	rows, err := v1.QueryContext(ctx, `
		SELECT h.id, h.conversation_id FROM file_history h ORDER BY h.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type hist struct {
		id   int64
		name string
	}
	var hists []hist
	for rows.Next() {
		var h hist
		if err := rows.Scan(&h.id, &h.name); err != nil {
			return nil, err
		}
		hists = append(hists, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []structuredArtifact
	for _, h := range hists {
		vrows, err := v1.QueryContext(ctx, `
			SELECT COALESCE(hash,''), version, COALESCE(content,'')
			FROM file_versions WHERE file_history_id = ? ORDER BY version`, h.id)
		if err != nil {
			return nil, err
		}
		type version struct {
			Hash    string `json:"hash"`
			Version string `json:"version"`
			Content string `json:"content"`
		}
		var versions []version
		for vrows.Next() {
			var hash, content string
			var vnum int64
			if err := vrows.Scan(&hash, &vnum, &content); err != nil {
				vrows.Close()
				return nil, err
			}
			versions = append(versions, version{Hash: hash, Version: fmt.Sprint(vnum), Content: content})
		}
		vrows.Close()
		if err := vrows.Err(); err != nil {
			return nil, err
		}
		if len(versions) == 0 {
			continue
		}
		meta, _ := json.Marshal(map[string]any{"versions": versions})
		out = append(out, structuredArtifact{
			kind: canon.ArtifactFileHistory, name: h.name, metadata: meta,
			linkTo:   h.name, // file-history dirs are named by the owning session
			relation: canon.LinkProducedBy, evidence: canon.EvidenceIDMatch,
		})
	}
	return out, nil
}

func collectV1UsageFacets(ctx context.Context, v1 *sql.DB, sch v1Schema) ([]structuredArtifact, error) {
	if !sch.table("usage_facets") {
		return nil, nil
	}
	rows, err := v1.QueryContext(ctx, `
		SELECT session_id_text, COALESCE(underlying_goal,''), COALESCE(outcome,''),
		       COALESCE(helpfulness,''), COALESCE(session_type,''),
		       COALESCE(primary_success,''), COALESCE(brief_summary,''),
		       COALESCE(friction_detail,''), COALESCE(goal_categories,'{}'),
		       COALESCE(satisfaction,'{}'), COALESCE(friction_counts,'{}')
		FROM usage_facets`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []structuredArtifact
	for rows.Next() {
		var sid, goal, outcome, helpfulness, sessionType, success,
			summary, friction, goalCats, satisfaction, frictionCounts string
		if err := rows.Scan(&sid, &goal, &outcome, &helpfulness, &sessionType,
			&success, &summary, &friction, &goalCats, &satisfaction, &frictionCounts); err != nil {
			return nil, err
		}
		meta, _ := json.Marshal(map[string]any{
			"session_id": sid, "underlying_goal": goal, "outcome": outcome,
			"helpfulness": helpfulness, "session_type": sessionType,
			"primary_success": success, "brief_summary": summary,
			"friction_detail": friction,
			"goal_categories": json.RawMessage(goalCats),
			"satisfaction":    json.RawMessage(satisfaction),
			"friction_counts": json.RawMessage(frictionCounts),
		})
		out = append(out, structuredArtifact{
			kind: canon.ArtifactUsageFacet, name: sid,
			content: summary, metadata: meta,
			linkTo:   sid,
			relation: canon.LinkAppliesTo, evidence: canon.EvidenceIDMatch,
		})
	}
	return out, rows.Err()
}

func collectV1UsageReport(ctx context.Context, v1 *sql.DB, sch v1Schema) ([]structuredArtifact, error) {
	if !sch.table("usage_report") {
		return nil, nil
	}
	var content string
	err := v1.QueryRowContext(ctx,
		`SELECT COALESCE(content,'') FROM usage_report LIMIT 1`).Scan(&content)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if content == "" {
		return nil, nil
	}
	return []structuredArtifact{{
		kind: canon.ArtifactUsageReport, name: "report.html", content: content,
	}}, nil
}

func collectV1Memories(ctx context.Context, v1 *sql.DB, sch v1Schema) ([]structuredArtifact, error) {
	if !sch.table("memories") {
		return nil, nil
	}
	// memories.file_name arrived in v15; the v14→v15 migration backfilled
	// existing rows with 'MEMORY.md', so pre-v15 vintages import under the
	// same default (one memory file per project was the only shape then).
	fileExpr := `COALESCE(file_name,'')`
	if !sch.has("memories", "file_name") {
		fileExpr = `'MEMORY.md'`
	}
	rows, err := v1.QueryContext(ctx, fmt.Sprintf(`
		SELECT COALESCE(project_dir,''), %s, COALESCE(content,'')
		FROM memories`, fileExpr))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []structuredArtifact
	for rows.Next() {
		var dir, file, content string
		if err := rows.Scan(&dir, &file, &content); err != nil {
			return nil, err
		}
		if dir == "" || file == "" {
			continue
		}
		meta, _ := json.Marshal(map[string]string{"projectDir": dir})
		out = append(out, structuredArtifact{
			kind: canon.ArtifactMemory, name: dir + "/" + file,
			content: content, metadata: meta,
		})
	}
	return out, rows.Err()
}

// todoFileRe extracts the session uuid from todo file names, matching
// the ingest-side rule.
var todoFileRe = regexp.MustCompile(`^([0-9a-f-]{36})-agent-`)

// importIgnoreFlags carries the user's scan-ignore decisions over,
// TRANSLATING v1 finding identities into the keys the v2 scanner
// resolves — a verbatim copy would never re-attach, silently reviving
// every dismissed secret.
//
// v2 keys: "message/<agent>/<session>/<rule>/<seq>" for messages and
// "artifact/<agent>/<kind>/<name>/<rule>/<line>" for artifacts. The
// message translation resolves the v2 seq by matching the v1 timestamp
// against the imported/ingested messages (seconds precision — v2 stores
// RFC3339 without sub-second parts). Where no message matches, and for
// artifact findings whose v1 line numbering has no v2 equivalent, a
// rule-scoped wildcard key ("…/<rule>/*") preserves the user's intent:
// they dismissed this rule on this entity.
//
// The v1 scanner emitted exactly eleven source types; each one maps
// explicitly (v1 source_type/source_id → v2 key entity):
//
//	message   <session>@<ts>      → message/<agent>/<session>  (seq via ts)
//	command   <session>@<ts>      → message/<agent>/<session>  (v1 scanned
//	          commands out of the same transcript entries v2 covers as
//	          messages, so the ignore collapses into the containing one)
//	plan      <file_name>         → artifact plan/<file_name>
//	shell_snapshot <file_name>    → artifact shell_snapshot/<file_name>
//	paste_cache <file_name>       → artifact paste/<file_name>
//	memory    <dir>/<file>.md     → artifact memory/<dir>/<file>.md
//	todo      <file>#item-<seq>   → artifact todo_list/<file>
//	task      <dir>[#task-<id>]   → artifact task_group/<dir>
//	file_history <conversation>   → artifact file_history/<conversation>
//	usage_facet <session>         → artifact usage_facet/<session>
//	usage_report report           → artifact usage_report/report.html
//
// The per-item todo/task identities deliberately coarsen to the whole
// artifact: sibling items' findings of the same rule are ignored too,
// erring on the user's side. A v1 memory row with an empty file name
// yields a bare-projectDir id no v2 artifact name can equal ("<dir>/
// <file>" always has the slash); its annotation imports anyway and
// stays inert — an orphan, not a mismatch.
func importIgnoreFlags(ctx context.Context, store *db.Store, v1 *sql.DB, sch v1Schema, report *Report) error {
	if !sch.table("scan_findings") || !sch.has("scan_findings", "ignored") {
		return nil // this vintage predates scanning, or the ignore feature
	}
	rows, err := v1.QueryContext(ctx, `
		SELECT DISTINCT rule_id, source_type, source_id
		FROM scan_findings WHERE ignored = 1`)
	if err != nil {
		return err
	}
	defer rows.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	insert := func(key string) error {
		_, err := store.DB().ExecContext(ctx, `
			INSERT INTO user_annotations (entity_type, natural_key, kind, value_json, created_at)
			VALUES (?, ?, ?, '{}', ?)
			ON CONFLICT(entity_type, natural_key, kind) DO NOTHING`,
			db.ScanFindingEntity, key, db.ScanIgnoreKind, now)
		return err
	}
	for rows.Next() {
		var ruleID, sourceType, sourceID string
		if err := rows.Scan(&ruleID, &sourceType, &sourceID); err != nil {
			return err
		}
		// The key formats live in db, which every surface reading them
		// shares; the importer only decides WHICH entity each v1 row maps
		// to. Artifacts always take the wildcard form: v1's line numbering
		// has no v2 equivalent.
		artifactKey := func(kind canon.ArtifactKind, name string) string {
			return db.ScanIgnoreWildcardKey(
				fmt.Sprintf("artifact/%s/%s/%s", claudeSlug, kind, name), ruleID,
			)
		}
		var keys []string
		switch sourceType {
		case "message", "command":
			// v1 scanned commands out of the same transcript entries the
			// v2 scanner covers as messages.
			session, ts, ok := splitV1MessageID(sourceID)
			entity := fmt.Sprintf("message/%s/%s", claudeSlug, session)
			if ok {
				for _, seq := range messageSeqsAt(ctx, store, session, ts) {
					keys = append(keys, db.ScanIgnoreKey(entity, ruleID, seq))
				}
			}
			if len(keys) == 0 {
				keys = append(keys, db.ScanIgnoreWildcardKey(entity, ruleID))
			}
		case "plan":
			keys = append(keys, artifactKey(canon.ArtifactPlan, sourceID))
		case "shell_snapshot":
			keys = append(keys, artifactKey(canon.ArtifactShellSnapshot, sourceID))
		case "memory":
			keys = append(keys, artifactKey(canon.ArtifactMemory, sourceID))
		case "paste_cache":
			keys = append(keys, artifactKey(canon.ArtifactPaste, sourceID))
		case "todo":
			name := sourceID
			if i := strings.LastIndex(name, "#item-"); i >= 0 {
				name = name[:i]
			}
			keys = append(keys, artifactKey(canon.ArtifactTodoList, name))
		case "task":
			name := sourceID
			if i := strings.LastIndex(name, "#task-"); i >= 0 {
				name = name[:i]
			}
			keys = append(keys, artifactKey(canon.ArtifactTaskGroup, name))
		case "file_history":
			keys = append(keys, artifactKey(canon.ArtifactFileHistory, sourceID))
		case "usage_facet":
			keys = append(keys, artifactKey(canon.ArtifactUsageFacet, sourceID))
		case "usage_report":
			// v1 stored the single report under the fixed id "report"; v2
			// names the artifact by its on-disk file.
			keys = append(keys, artifactKey(canon.ArtifactUsageReport, "report.html"))
		default:
			// A source type this importer does not know (a v1 newer than
			// its final release shape). Preserve the identity verbatim so
			// the decision is at least kept, even if it cannot re-attach.
			keys = append(keys, db.ScanIgnoreWildcardKey(
				fmt.Sprintf("artifact/%s/%s/%s", claudeSlug, sourceType, sourceID), ruleID,
			))
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
