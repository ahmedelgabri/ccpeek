package store

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
)

const schemaVersion = 16

// initialSchema is migration 0 → 1: the baseline schema (v4 equivalent).
const initialSchema = `
CREATE TABLE IF NOT EXISTS meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS projects (
	id             INTEGER PRIMARY KEY,
	dir_name       TEXT NOT NULL UNIQUE,
	display_name   TEXT NOT NULL,
	canonical_path TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS sessions (
	id                 INTEGER PRIMARY KEY,
	session_id         TEXT NOT NULL,
	project_id         INTEGER NOT NULL REFERENCES projects(id),
	first_prompt       TEXT NOT NULL DEFAULT '',
	message_count      INTEGER NOT NULL DEFAULT 0,
	created_at         TEXT NOT NULL DEFAULT '',
	modified_at        TEXT NOT NULL DEFAULT '',
	git_branch         TEXT NOT NULL DEFAULT '',
	project_path       TEXT NOT NULL DEFAULT '',
	todo_file_name     TEXT NOT NULL DEFAULT '',
	has_file_history   INTEGER NOT NULL DEFAULT 0,
	bash_command_count INTEGER NOT NULL DEFAULT 0,
	tool_use_counts    TEXT NOT NULL DEFAULT '{}',
	estimated_tokens   INTEGER NOT NULL DEFAULT 0,
	UNIQUE(session_id, project_id)
);
CREATE INDEX IF NOT EXISTS idx_sessions_project ON sessions(project_id);
CREATE INDEX IF NOT EXISTS idx_sessions_modified ON sessions(modified_at DESC);

CREATE TABLE IF NOT EXISTS messages (
	id         INTEGER PRIMARY KEY,
	session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	seq        INTEGER NOT NULL,
	type       TEXT NOT NULL,
	role       TEXT NOT NULL DEFAULT '',
	timestamp  TEXT NOT NULL DEFAULT '',
	uuid       TEXT NOT NULL DEFAULT '',
	content    TEXT NOT NULL DEFAULT '',
	cwd        TEXT NOT NULL DEFAULT '',
	git_branch TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, seq);

CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(text_content);
CREATE VIRTUAL TABLE IF NOT EXISTS search_documents_fts USING fts5(
	group_type UNINDEXED,
	title UNINDEXED,
	subtitle UNINDEXED,
	url UNINDEXED,
	text_content
);

CREATE TABLE IF NOT EXISTS tool_calls (
	id              INTEGER PRIMARY KEY,
	session_id      INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	seq             INTEGER NOT NULL,
	timestamp       TEXT NOT NULL DEFAULT '',
	tool_name       TEXT NOT NULL DEFAULT '',
	tool_kind       TEXT NOT NULL DEFAULT '',
	input_json      TEXT NOT NULL DEFAULT '{}',
	result_text     TEXT NOT NULL DEFAULT '',
	file_path       TEXT NOT NULL DEFAULT '',
	searchable_text TEXT NOT NULL DEFAULT '',
	UNIQUE(session_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_tool_calls_session ON tool_calls(session_id, seq);
CREATE INDEX IF NOT EXISTS idx_tool_calls_kind ON tool_calls(tool_kind, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_tool_calls_name ON tool_calls(tool_name);

CREATE TABLE IF NOT EXISTS plans (
	id         INTEGER PRIMARY KEY,
	file_name  TEXT NOT NULL UNIQUE,
	title      TEXT NOT NULL,
	size_bytes INTEGER NOT NULL DEFAULT 0,
	content    TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS shell_snapshots (
	id         INTEGER PRIMARY KEY,
	file_name  TEXT NOT NULL UNIQUE,
	timestamp  INTEGER NOT NULL DEFAULT 0,
	size_bytes INTEGER NOT NULL DEFAULT 0,
	content    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_snapshots_ts ON shell_snapshots(timestamp DESC);

CREATE TABLE IF NOT EXISTS todos (
	id           INTEGER PRIMARY KEY,
	file_name    TEXT NOT NULL UNIQUE,
	session_id   INTEGER REFERENCES sessions(id) ON DELETE SET NULL,
	item_count   INTEGER NOT NULL DEFAULT 0,
	statuses     TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS todo_items (
	id          INTEGER PRIMARY KEY,
	todo_id     INTEGER NOT NULL REFERENCES todos(id) ON DELETE CASCADE,
	seq         INTEGER NOT NULL,
	content     TEXT NOT NULL DEFAULT '',
	status      TEXT NOT NULL DEFAULT '',
	active_form TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_todo_items_todo ON todo_items(todo_id, seq);

CREATE TABLE IF NOT EXISTS file_history (
	id              INTEGER PRIMARY KEY,
	conversation_id TEXT NOT NULL UNIQUE,
	session_id      INTEGER REFERENCES sessions(id) ON DELETE SET NULL,
	file_count      INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS file_versions (
	id              INTEGER PRIMARY KEY,
	file_history_id INTEGER NOT NULL REFERENCES file_history(id) ON DELETE CASCADE,
	hash            TEXT NOT NULL,
	version         INTEGER NOT NULL,
	content         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_file_versions_fh ON file_versions(file_history_id, hash, version);

CREATE TABLE IF NOT EXISTS history (
	id        INTEGER PRIMARY KEY,
	display   TEXT NOT NULL DEFAULT '',
	timestamp INTEGER NOT NULL DEFAULT 0,
	project   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_history_ts ON history(timestamp DESC);

CREATE TABLE IF NOT EXISTS task_groups (
	id         INTEGER PRIMARY KEY,
	dir_name   TEXT NOT NULL UNIQUE,
	session_id INTEGER REFERENCES sessions(id) ON DELETE SET NULL,
	item_count INTEGER NOT NULL DEFAULT 0,
	statuses   TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS task_items (
	id             INTEGER PRIMARY KEY,
	task_group_id  INTEGER NOT NULL REFERENCES task_groups(id) ON DELETE CASCADE,
	seq            INTEGER NOT NULL,
	item_id        TEXT NOT NULL DEFAULT '',
	subject        TEXT NOT NULL DEFAULT '',
	description    TEXT NOT NULL DEFAULT '',
	active_form    TEXT NOT NULL DEFAULT '',
	status         TEXT NOT NULL DEFAULT '',
	blocks         TEXT NOT NULL DEFAULT '[]',
	blocked_by     TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX IF NOT EXISTS idx_task_items_group ON task_items(task_group_id, seq);

CREATE TABLE IF NOT EXISTS paste_cache (
	id         INTEGER PRIMARY KEY,
	file_name  TEXT NOT NULL UNIQUE,
	size_bytes INTEGER NOT NULL DEFAULT 0,
	content    TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS usage_facets (
	id               INTEGER PRIMARY KEY,
	session_id_text  TEXT NOT NULL UNIQUE,
	db_session_id    INTEGER REFERENCES sessions(id) ON DELETE SET NULL,
	underlying_goal  TEXT NOT NULL DEFAULT '',
	outcome          TEXT NOT NULL DEFAULT '',
	helpfulness      TEXT NOT NULL DEFAULT '',
	session_type     TEXT NOT NULL DEFAULT '',
	primary_success  TEXT NOT NULL DEFAULT '',
	brief_summary    TEXT NOT NULL DEFAULT '',
	friction_detail  TEXT NOT NULL DEFAULT '',
	goal_categories  TEXT NOT NULL DEFAULT '{}',
	satisfaction     TEXT NOT NULL DEFAULT '{}',
	friction_counts  TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS usage_report (
	id      INTEGER PRIMARY KEY,
	content TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS memories (
	id          INTEGER PRIMARY KEY,
	project_dir TEXT NOT NULL,
	file_name   TEXT NOT NULL DEFAULT 'MEMORY.md',
	project_id  INTEGER REFERENCES projects(id) ON DELETE SET NULL,
	size_bytes  INTEGER NOT NULL DEFAULT 0,
	content     TEXT NOT NULL DEFAULT '',
	UNIQUE(project_dir, file_name)
);

CREATE TABLE IF NOT EXISTS commands (
	id         INTEGER PRIMARY KEY,
	session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	seq        INTEGER NOT NULL,
	command    TEXT NOT NULL,
	timestamp  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_commands_session ON commands(session_id, seq);
CREATE INDEX IF NOT EXISTS idx_commands_ts ON commands(timestamp DESC);

CREATE TABLE IF NOT EXISTS scan_findings (
	id             INTEGER PRIMARY KEY,
	rule_id        TEXT NOT NULL DEFAULT '',
	description    TEXT NOT NULL DEFAULT '',
	source_type    TEXT NOT NULL DEFAULT '',
	source_id      TEXT NOT NULL DEFAULT '',
	match_redacted TEXT NOT NULL DEFAULT '',
	line_number    INTEGER NOT NULL DEFAULT 0,
	scanned_at     TEXT NOT NULL DEFAULT '',
	ignored        INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_scan_findings_rule ON scan_findings(rule_id);
CREATE INDEX IF NOT EXISTS idx_scan_findings_type ON scan_findings(source_type);

CREATE TABLE IF NOT EXISTS source_files (
	path         TEXT PRIMARY KEY,
	content_hash TEXT NOT NULL DEFAULT '',
	indexed_at   TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS ingest_runs (
	id               INTEGER PRIMARY KEY,
	mode             TEXT NOT NULL DEFAULT '',
	status           TEXT NOT NULL DEFAULT '',
	claude_dir       TEXT NOT NULL DEFAULT '',
	started_at       TEXT NOT NULL DEFAULT '',
	finished_at      TEXT NOT NULL DEFAULT '',
	duration_ms      INTEGER NOT NULL DEFAULT 0,
	files_seen       INTEGER NOT NULL DEFAULT 0,
	files_changed    INTEGER NOT NULL DEFAULT 0,
	records_indexed  INTEGER NOT NULL DEFAULT 0,
	skipped_files    INTEGER NOT NULL DEFAULT 0,
	skipped_rows     INTEGER NOT NULL DEFAULT 0,
	parse_failures   INTEGER NOT NULL DEFAULT 0,
	unresolved_links INTEGER NOT NULL DEFAULT 0,
	warning_count    INTEGER NOT NULL DEFAULT 0,
	error_message    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_ingest_runs_started ON ingest_runs(started_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS ingest_issues (
	id          INTEGER PRIMARY KEY,
	run_id      INTEGER NOT NULL REFERENCES ingest_runs(id) ON DELETE CASCADE,
	severity    TEXT NOT NULL DEFAULT '',
	category    TEXT NOT NULL DEFAULT '',
	source_type TEXT NOT NULL DEFAULT '',
	source_path TEXT NOT NULL DEFAULT '',
	line_number INTEGER NOT NULL DEFAULT 0,
	detail      TEXT NOT NULL DEFAULT '',
	created_at  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_ingest_issues_run ON ingest_issues(run_id, id);
`

// migrations is a list of migration functions applied sequentially.
// Index 0 = v4→v5: add source_path + content_hash for incremental indexing.
var migrations = []func(ctx context.Context, tx *sqlx.Tx) error{
	migrateV4ToV5,
	migrateV5ToV6,
	migrateV6ToV7,
	migrateV7ToV8,
	migrateV8ToV9,
	migrateV9ToV10,
	migrateV10ToV11,
	migrateV11ToV12,
	migrateV12ToV13,
	migrateV13ToV14,
	migrateV14ToV15,
	migrateV15ToV16,
}

// migrateV4ToV5 adds source_path columns to entity tables and replaces
// mtime_ns with content_hash in source_files.
func migrateV4ToV5(ctx context.Context, tx *sqlx.Tx) error {
	// Tables that get source_path for per-file tracking
	tables := []string{
		"plans", "shell_snapshots", "sessions", "todos",
		"file_history", "history", "task_groups", "paste_cache",
		"usage_facets", "usage_report", "memories",
	}
	for _, t := range tables {
		col := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN source_path TEXT NOT NULL DEFAULT ''`, t)
		if _, err := tx.ExecContext(ctx, col); err != nil {
			return fmt.Errorf("adding source_path to %s: %w", t, err)
		}
		idx := fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%s_source ON %s(source_path)`, t, t)
		if _, err := tx.ExecContext(ctx, idx); err != nil {
			return fmt.Errorf("creating source_path index on %s: %w", t, err)
		}
	}

	// Replace mtime_ns with content_hash in source_files.
	// SQLite doesn't support DROP COLUMN before 3.35.0 and doesn't support
	// ALTER COLUMN, so recreate the table.
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS source_files_new (
			path         TEXT PRIMARY KEY,
			content_hash TEXT NOT NULL DEFAULT '',
			indexed_at   TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT OR IGNORE INTO source_files_new (path, indexed_at)
			SELECT path, indexed_at FROM source_files`,
		`DROP TABLE source_files`,
		`ALTER TABLE source_files_new RENAME TO source_files`,
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrating source_files: %w", err)
		}
	}

	return nil
}

// migrateV7ToV8 adds the ignored column to scan_findings.
func migrateV7ToV8(ctx context.Context, tx *sqlx.Tx) error {
	// Column may already exist if the table was created with the full initial schema
	var hasCol int
	err := tx.GetContext(ctx, &hasCol, `SELECT COUNT(*) FROM pragma_table_info('scan_findings') WHERE name = 'ignored'`)
	if err != nil || hasCol > 0 {
		return nil
	}
	_, err = tx.ExecContext(ctx, `ALTER TABLE scan_findings ADD COLUMN ignored INTEGER NOT NULL DEFAULT 0`)
	return err
}

// migrateV6ToV7 adds the scan_findings table for secret scanning.
func migrateV6ToV7(ctx context.Context, tx *sqlx.Tx) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS scan_findings (
			id             INTEGER PRIMARY KEY,
			rule_id        TEXT NOT NULL DEFAULT '',
			description    TEXT NOT NULL DEFAULT '',
			source_type    TEXT NOT NULL DEFAULT '',
			source_id      TEXT NOT NULL DEFAULT '',
			match_redacted TEXT NOT NULL DEFAULT '',
			line_number    INTEGER NOT NULL DEFAULT 0,
			scanned_at     TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_scan_findings_rule ON scan_findings(rule_id)`,
		`CREATE INDEX IF NOT EXISTS idx_scan_findings_type ON scan_findings(source_type)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("creating scan_findings table: %w", err)
		}
	}
	return nil
}

// migrateV8ToV9 changes the sessions UNIQUE constraint from session_id alone
// to (session_id, project_id), allowing the same session to appear in multiple projects.
func migrateV8ToV9(ctx context.Context, tx *sqlx.Tx) error {
	stmts := []string{
		`CREATE TABLE sessions_new (
			id                 INTEGER PRIMARY KEY,
			session_id         TEXT NOT NULL,
			project_id         INTEGER NOT NULL REFERENCES projects(id),
			first_prompt       TEXT NOT NULL DEFAULT '',
			message_count      INTEGER NOT NULL DEFAULT 0,
			created_at         TEXT NOT NULL DEFAULT '',
			modified_at        TEXT NOT NULL DEFAULT '',
			git_branch         TEXT NOT NULL DEFAULT '',
			project_path       TEXT NOT NULL DEFAULT '',
			todo_file_name     TEXT NOT NULL DEFAULT '',
			has_file_history   INTEGER NOT NULL DEFAULT 0,
			bash_command_count INTEGER NOT NULL DEFAULT 0,
			tool_use_counts    TEXT NOT NULL DEFAULT '{}',
			estimated_tokens   INTEGER NOT NULL DEFAULT 0,
			source_path        TEXT NOT NULL DEFAULT '',
			UNIQUE(session_id, project_id)
		)`,
		`INSERT OR IGNORE INTO sessions_new
			SELECT id, session_id, project_id, first_prompt, message_count,
			       created_at, modified_at, git_branch, project_path,
			       todo_file_name, has_file_history, bash_command_count,
			       tool_use_counts, estimated_tokens, source_path
			FROM sessions`,
		`DROP TABLE sessions`,
		`ALTER TABLE sessions_new RENAME TO sessions`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_project ON sessions(project_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_modified ON sessions(modified_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_source ON sessions(source_path)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrating sessions unique constraint: %w", err)
		}
	}
	return nil
}

// migrateV5ToV6 adds the commands table for global bash command browsing.
func migrateV5ToV6(ctx context.Context, tx *sqlx.Tx) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS commands (
			id         INTEGER PRIMARY KEY,
			session_id INTEGER NOT NULL REFERENCES sessions(id),
			seq        INTEGER NOT NULL,
			command    TEXT NOT NULL,
			timestamp  TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_commands_session ON commands(session_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_commands_ts ON commands(timestamp DESC)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("creating commands table: %w", err)
		}
	}
	return nil
}

// migrateV9ToV10 adds ingest run history and per-run diagnostics.
func migrateV9ToV10(ctx context.Context, tx *sqlx.Tx) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS ingest_runs (
			id               INTEGER PRIMARY KEY,
			mode             TEXT NOT NULL DEFAULT '',
			status           TEXT NOT NULL DEFAULT '',
			claude_dir       TEXT NOT NULL DEFAULT '',
			started_at       TEXT NOT NULL DEFAULT '',
			finished_at      TEXT NOT NULL DEFAULT '',
			duration_ms      INTEGER NOT NULL DEFAULT 0,
			files_seen       INTEGER NOT NULL DEFAULT 0,
			files_changed    INTEGER NOT NULL DEFAULT 0,
			records_indexed  INTEGER NOT NULL DEFAULT 0,
			skipped_files    INTEGER NOT NULL DEFAULT 0,
			skipped_rows     INTEGER NOT NULL DEFAULT 0,
			parse_failures   INTEGER NOT NULL DEFAULT 0,
			unresolved_links INTEGER NOT NULL DEFAULT 0,
			warning_count    INTEGER NOT NULL DEFAULT 0,
			error_message    TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ingest_runs_started ON ingest_runs(started_at DESC, id DESC)`,
		`CREATE TABLE IF NOT EXISTS ingest_issues (
			id          INTEGER PRIMARY KEY,
			run_id      INTEGER NOT NULL REFERENCES ingest_runs(id),
			severity    TEXT NOT NULL DEFAULT '',
			category    TEXT NOT NULL DEFAULT '',
			source_type TEXT NOT NULL DEFAULT '',
			source_path TEXT NOT NULL DEFAULT '',
			line_number INTEGER NOT NULL DEFAULT 0,
			detail      TEXT NOT NULL DEFAULT '',
			created_at  TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_ingest_issues_run ON ingest_issues(run_id, id)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("creating ingest diagnostics tables: %w", err)
		}
	}
	return nil
}

// migrateV10ToV11 adds normalized tool call rows.
func migrateV10ToV11(ctx context.Context, tx *sqlx.Tx) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS tool_calls (
			id              INTEGER PRIMARY KEY,
			session_id      INTEGER NOT NULL REFERENCES sessions(id),
			seq             INTEGER NOT NULL,
			timestamp       TEXT NOT NULL DEFAULT '',
			tool_name       TEXT NOT NULL DEFAULT '',
			tool_kind       TEXT NOT NULL DEFAULT '',
			input_json      TEXT NOT NULL DEFAULT '{}',
			result_text     TEXT NOT NULL DEFAULT '',
			file_path       TEXT NOT NULL DEFAULT '',
			searchable_text TEXT NOT NULL DEFAULT '',
			UNIQUE(session_id, seq)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_calls_session ON tool_calls(session_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_calls_kind ON tool_calls(tool_kind, timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_calls_name ON tool_calls(tool_name)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("creating tool_calls table: %w", err)
		}
	}
	return nil
}

// migrateV11ToV12 adds a canonical project path field and backfills it from sessions.
func migrateV11ToV12(ctx context.Context, tx *sqlx.Tx) error {
	var hasCol int
	if err := tx.GetContext(ctx, &hasCol, `SELECT COUNT(*) FROM pragma_table_info('projects') WHERE name = 'canonical_path'`); err != nil {
		return fmt.Errorf("checking projects.canonical_path: %w", err)
	}
	if hasCol == 0 {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN canonical_path TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("adding projects.canonical_path: %w", err)
		}
	}

	_, err := tx.ExecContext(ctx, `
		UPDATE projects
		SET canonical_path = COALESCE(
			NULLIF((
				SELECT s.project_path
				FROM sessions s
				WHERE s.project_id = projects.id AND s.project_path <> ''
				ORDER BY s.modified_at DESC, s.id DESC
				LIMIT 1
			), ''),
			CASE WHEN display_name <> dir_name THEN display_name ELSE '' END
		)
		WHERE canonical_path = ''`)
	if err != nil {
		return fmt.Errorf("backfilling projects.canonical_path: %w", err)
	}
	return nil
}

// migrateV12ToV13 adds a unified cross-domain FTS search index.
func migrateV12ToV13(ctx context.Context, tx *sqlx.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE VIRTUAL TABLE IF NOT EXISTS search_documents_fts USING fts5(
		group_type UNINDEXED,
		title UNINDEXED,
		subtitle UNINDEXED,
		url UNINDEXED,
		text_content
	)`)
	if err != nil {
		return fmt.Errorf("creating search_documents_fts: %w", err)
	}
	return nil
}

// migrateV13ToV14 adds ON DELETE actions so cleanup can rely on foreign keys.
func migrateV13ToV14(ctx context.Context, tx *sqlx.Tx) error {
	ensureSourcePath := func(table string) error {
		var hasCol int
		if err := tx.GetContext(ctx, &hasCol, fmt.Sprintf(`SELECT COUNT(*) FROM pragma_table_info('%s') WHERE name = 'source_path'`, table)); err != nil {
			return fmt.Errorf("checking %s.source_path: %w", table, err)
		}
		if hasCol > 0 {
			return nil
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN source_path TEXT NOT NULL DEFAULT ''`, table)); err != nil {
			return fmt.Errorf("adding %s.source_path: %w", table, err)
		}
		return nil
	}
	for _, table := range []string{"todos", "file_history", "task_groups", "usage_facets", "memories"} {
		if err := ensureSourcePath(table); err != nil {
			return err
		}
	}

	stmts := []string{
		`CREATE TABLE messages_new (
			id         INTEGER PRIMARY KEY,
			session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			seq        INTEGER NOT NULL,
			type       TEXT NOT NULL,
			role       TEXT NOT NULL DEFAULT '',
			timestamp  TEXT NOT NULL DEFAULT '',
			uuid       TEXT NOT NULL DEFAULT '',
			content    TEXT NOT NULL DEFAULT '',
			cwd        TEXT NOT NULL DEFAULT '',
			git_branch TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO messages_new SELECT id, session_id, seq, type, role, timestamp, uuid, content, cwd, git_branch FROM messages`,
		`DROP TABLE messages`,
		`ALTER TABLE messages_new RENAME TO messages`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, seq)`,

		`CREATE TABLE tool_calls_new (
			id              INTEGER PRIMARY KEY,
			session_id      INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			seq             INTEGER NOT NULL,
			timestamp       TEXT NOT NULL DEFAULT '',
			tool_name       TEXT NOT NULL DEFAULT '',
			tool_kind       TEXT NOT NULL DEFAULT '',
			input_json      TEXT NOT NULL DEFAULT '{}',
			result_text     TEXT NOT NULL DEFAULT '',
			file_path       TEXT NOT NULL DEFAULT '',
			searchable_text TEXT NOT NULL DEFAULT '',
			UNIQUE(session_id, seq)
		)`,
		`INSERT INTO tool_calls_new SELECT id, session_id, seq, timestamp, tool_name, tool_kind, input_json, result_text, file_path, searchable_text FROM tool_calls`,
		`DROP TABLE tool_calls`,
		`ALTER TABLE tool_calls_new RENAME TO tool_calls`,
		`CREATE INDEX IF NOT EXISTS idx_tool_calls_session ON tool_calls(session_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_calls_kind ON tool_calls(tool_kind, timestamp DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_calls_name ON tool_calls(tool_name)`,

		`CREATE TABLE commands_new (
			id         INTEGER PRIMARY KEY,
			session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			seq        INTEGER NOT NULL,
			command    TEXT NOT NULL,
			timestamp  TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO commands_new SELECT id, session_id, seq, command, timestamp FROM commands`,
		`DROP TABLE commands`,
		`ALTER TABLE commands_new RENAME TO commands`,
		`CREATE INDEX IF NOT EXISTS idx_commands_session ON commands(session_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_commands_ts ON commands(timestamp DESC)`,

		`CREATE TABLE todos_new (
			id           INTEGER PRIMARY KEY,
			file_name    TEXT NOT NULL UNIQUE,
			session_id   INTEGER REFERENCES sessions(id) ON DELETE SET NULL,
			item_count   INTEGER NOT NULL DEFAULT 0,
			statuses     TEXT NOT NULL DEFAULT '{}',
			source_path  TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO todos_new SELECT id, file_name, session_id, item_count, statuses, source_path FROM todos`,
		`DROP TABLE todos`,
		`ALTER TABLE todos_new RENAME TO todos`,
		`CREATE INDEX IF NOT EXISTS idx_todos_source ON todos(source_path)`,

		`CREATE TABLE todo_items_new (
			id          INTEGER PRIMARY KEY,
			todo_id     INTEGER NOT NULL REFERENCES todos(id) ON DELETE CASCADE,
			seq         INTEGER NOT NULL,
			content     TEXT NOT NULL DEFAULT '',
			status      TEXT NOT NULL DEFAULT '',
			active_form TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO todo_items_new SELECT id, todo_id, seq, content, status, active_form FROM todo_items`,
		`DROP TABLE todo_items`,
		`ALTER TABLE todo_items_new RENAME TO todo_items`,
		`CREATE INDEX IF NOT EXISTS idx_todo_items_todo ON todo_items(todo_id, seq)`,

		`CREATE TABLE file_history_new (
			id              INTEGER PRIMARY KEY,
			conversation_id TEXT NOT NULL UNIQUE,
			session_id      INTEGER REFERENCES sessions(id) ON DELETE SET NULL,
			file_count      INTEGER NOT NULL DEFAULT 0,
			source_path     TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO file_history_new SELECT id, conversation_id, session_id, file_count, source_path FROM file_history`,
		`DROP TABLE file_history`,
		`ALTER TABLE file_history_new RENAME TO file_history`,
		`CREATE INDEX IF NOT EXISTS idx_file_history_source ON file_history(source_path)`,

		`CREATE TABLE file_versions_new (
			id              INTEGER PRIMARY KEY,
			file_history_id INTEGER NOT NULL REFERENCES file_history(id) ON DELETE CASCADE,
			hash            TEXT NOT NULL,
			version         INTEGER NOT NULL,
			content         TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO file_versions_new SELECT id, file_history_id, hash, version, content FROM file_versions`,
		`DROP TABLE file_versions`,
		`ALTER TABLE file_versions_new RENAME TO file_versions`,
		`CREATE INDEX IF NOT EXISTS idx_file_versions_fh ON file_versions(file_history_id, hash, version)`,

		`CREATE TABLE task_groups_new (
			id         INTEGER PRIMARY KEY,
			dir_name   TEXT NOT NULL UNIQUE,
			session_id INTEGER REFERENCES sessions(id) ON DELETE SET NULL,
			item_count INTEGER NOT NULL DEFAULT 0,
			statuses   TEXT NOT NULL DEFAULT '{}',
			source_path TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO task_groups_new SELECT id, dir_name, session_id, item_count, statuses, source_path FROM task_groups`,
		`DROP TABLE task_groups`,
		`ALTER TABLE task_groups_new RENAME TO task_groups`,
		`CREATE INDEX IF NOT EXISTS idx_task_groups_source ON task_groups(source_path)`,

		`CREATE TABLE task_items_new (
			id             INTEGER PRIMARY KEY,
			task_group_id  INTEGER NOT NULL REFERENCES task_groups(id) ON DELETE CASCADE,
			seq            INTEGER NOT NULL,
			item_id        TEXT NOT NULL DEFAULT '',
			subject        TEXT NOT NULL DEFAULT '',
			description    TEXT NOT NULL DEFAULT '',
			active_form    TEXT NOT NULL DEFAULT '',
			status         TEXT NOT NULL DEFAULT '',
			blocks         TEXT NOT NULL DEFAULT '[]',
			blocked_by     TEXT NOT NULL DEFAULT '[]'
		)`,
		`INSERT INTO task_items_new SELECT id, task_group_id, seq, item_id, subject, description, active_form, status, blocks, blocked_by FROM task_items`,
		`DROP TABLE task_items`,
		`ALTER TABLE task_items_new RENAME TO task_items`,
		`CREATE INDEX IF NOT EXISTS idx_task_items_group ON task_items(task_group_id, seq)`,

		`CREATE TABLE usage_facets_new (
			id               INTEGER PRIMARY KEY,
			session_id_text  TEXT NOT NULL UNIQUE,
			db_session_id    INTEGER REFERENCES sessions(id) ON DELETE SET NULL,
			underlying_goal  TEXT NOT NULL DEFAULT '',
			outcome          TEXT NOT NULL DEFAULT '',
			helpfulness      TEXT NOT NULL DEFAULT '',
			session_type     TEXT NOT NULL DEFAULT '',
			primary_success  TEXT NOT NULL DEFAULT '',
			brief_summary    TEXT NOT NULL DEFAULT '',
			friction_detail  TEXT NOT NULL DEFAULT '',
			goal_categories  TEXT NOT NULL DEFAULT '{}',
			satisfaction     TEXT NOT NULL DEFAULT '{}',
			friction_counts  TEXT NOT NULL DEFAULT '{}',
			source_path      TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO usage_facets_new SELECT id, session_id_text, db_session_id, underlying_goal, outcome, helpfulness, session_type, primary_success, brief_summary, friction_detail, goal_categories, satisfaction, friction_counts, source_path FROM usage_facets`,
		`DROP TABLE usage_facets`,
		`ALTER TABLE usage_facets_new RENAME TO usage_facets`,
		`CREATE INDEX IF NOT EXISTS idx_usage_facets_source ON usage_facets(source_path)`,

		`CREATE TABLE memories_new (
			id          INTEGER PRIMARY KEY,
			project_dir TEXT NOT NULL UNIQUE,
			project_id  INTEGER REFERENCES projects(id) ON DELETE SET NULL,
			size_bytes  INTEGER NOT NULL DEFAULT 0,
			content     TEXT NOT NULL DEFAULT '',
			source_path TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO memories_new SELECT id, project_dir, project_id, size_bytes, content, source_path FROM memories`,
		`DROP TABLE memories`,
		`ALTER TABLE memories_new RENAME TO memories`,
		`CREATE INDEX IF NOT EXISTS idx_memories_source ON memories(source_path)`,

		`CREATE TABLE ingest_issues_new (
			id          INTEGER PRIMARY KEY,
			run_id      INTEGER NOT NULL REFERENCES ingest_runs(id) ON DELETE CASCADE,
			severity    TEXT NOT NULL DEFAULT '',
			category    TEXT NOT NULL DEFAULT '',
			source_type TEXT NOT NULL DEFAULT '',
			source_path TEXT NOT NULL DEFAULT '',
			line_number INTEGER NOT NULL DEFAULT 0,
			detail      TEXT NOT NULL DEFAULT '',
			created_at  TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO ingest_issues_new SELECT id, run_id, severity, category, source_type, source_path, line_number, detail, created_at FROM ingest_issues`,
		`DROP TABLE ingest_issues`,
		`ALTER TABLE ingest_issues_new RENAME TO ingest_issues`,
		`CREATE INDEX IF NOT EXISTS idx_ingest_issues_run ON ingest_issues(run_id, id)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrating foreign key delete actions: %w", err)
		}
	}
	return nil
}

// migrateV14ToV15 adds file_name column to memories and changes UNIQUE
// constraint from project_dir to (project_dir, file_name) to support
// multiple .md files per project memory folder.
func migrateV14ToV15(ctx context.Context, tx *sqlx.Tx) error {
	var hasFileName, hasSourcePath bool
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(memories)`)
	if err != nil {
		return fmt.Errorf("checking memories schema: %w", err)
	}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dfltValue *string
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			rows.Close()
			return err
		}
		switch name {
		case "file_name":
			hasFileName = true
		case "source_path":
			hasSourcePath = true
		}
	}
	rows.Close()

	if hasFileName && hasSourcePath {
		return nil
	}

	insertSelect := `SELECT id, project_dir, 'MEMORY.md', project_id, size_bytes, content, '' FROM memories`
	if hasFileName && hasSourcePath {
		insertSelect = `SELECT id, project_dir, file_name, project_id, size_bytes, content, source_path FROM memories`
	} else if hasFileName {
		insertSelect = `SELECT id, project_dir, file_name, project_id, size_bytes, content, '' FROM memories`
	} else if hasSourcePath {
		insertSelect = `SELECT id, project_dir, 'MEMORY.md', project_id, size_bytes, content, source_path FROM memories`
	}

	stmts := []string{
		`CREATE TABLE memories_new (
			id          INTEGER PRIMARY KEY,
			project_dir TEXT NOT NULL,
			file_name   TEXT NOT NULL DEFAULT 'MEMORY.md',
			project_id  INTEGER REFERENCES projects(id) ON DELETE SET NULL,
			size_bytes  INTEGER NOT NULL DEFAULT 0,
			content     TEXT NOT NULL DEFAULT '',
			source_path TEXT NOT NULL DEFAULT '',
			UNIQUE(project_dir, file_name)
		)`,
		`INSERT INTO memories_new (id, project_dir, file_name, project_id, size_bytes, content, source_path) ` + insertSelect,
		`DROP TABLE memories`,
		`ALTER TABLE memories_new RENAME TO memories`,
		`CREATE INDEX IF NOT EXISTS idx_memories_source ON memories(source_path)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrating memories file_name: %w", err)
		}
	}
	return nil
}

// migrateV15ToV16 adds mixed-source/Cursor metadata columns.
func migrateV15ToV16(ctx context.Context, tx *sqlx.Tx) error {
	type col struct {
		table string
		name  string
		def   string
	}
	cols := []col{
		{"projects", "source", "TEXT NOT NULL DEFAULT 'claude-code'"},
		{"projects", "updated_at_ms", "INTEGER NOT NULL DEFAULT 0"},
		{"plans", "updated_at_ms", "INTEGER NOT NULL DEFAULT 0"},
		{"plans", "source", "TEXT NOT NULL DEFAULT 'claude-code'"},
		{"shell_snapshots", "kind", "TEXT NOT NULL DEFAULT 'shell'"},
		{"shell_snapshots", "project_path", "TEXT NOT NULL DEFAULT ''"},
		{"shell_snapshots", "commit_hash", "TEXT NOT NULL DEFAULT ''"},
		{"shell_snapshots", "detail_file", "TEXT NOT NULL DEFAULT ''"},
		{"shell_snapshots", "source", "TEXT NOT NULL DEFAULT 'claude-code'"},
		{"todos", "updated_at_ms", "INTEGER NOT NULL DEFAULT 0"},
		{"todos", "source", "TEXT NOT NULL DEFAULT 'claude-code'"},
		{"file_history", "updated_at_ms", "INTEGER NOT NULL DEFAULT 0"},
		{"file_history", "source", "TEXT NOT NULL DEFAULT 'claude-code'"},
		{"file_versions", "file_path", "TEXT NOT NULL DEFAULT ''"},
		{"file_versions", "change_kind", "TEXT NOT NULL DEFAULT ''"},
		{"file_versions", "patch", "TEXT NOT NULL DEFAULT ''"},
		{"file_versions", "timestamp", "TEXT NOT NULL DEFAULT ''"},
		{"history", "project_dir", "TEXT NOT NULL DEFAULT ''"},
		{"history", "source", "TEXT NOT NULL DEFAULT 'claude-code'"},
		{"sessions", "metadata_only", "INTEGER NOT NULL DEFAULT 0"},
		{"sessions", "model_name", "TEXT NOT NULL DEFAULT ''"},
		{"sessions", "source", "TEXT NOT NULL DEFAULT 'claude-code'"},
		{"commands", "source", "TEXT NOT NULL DEFAULT 'claude-code'"},
		{"memories", "source", "TEXT NOT NULL DEFAULT 'claude-code'"},
	}
	for _, c := range cols {
		var count int
		q := fmt.Sprintf(`SELECT COUNT(*) FROM pragma_table_info('%s') WHERE name = ?`, c.table)
		if err := tx.GetContext(ctx, &count, q, c.name); err != nil {
			return fmt.Errorf("checking %s.%s: %w", c.table, c.name, err)
		}
		if count > 0 {
			continue
		}
		stmt := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, c.table, c.name, c.def)
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("adding %s.%s: %w", c.table, c.name, err)
		}
	}

	// Best-effort normalization for pre-existing rows.
	stmts := []string{
		`UPDATE projects SET source = 'claude-code' WHERE TRIM(source) = ''`,
		`UPDATE plans SET source = 'claude-code' WHERE TRIM(source) = ''`,
		`UPDATE shell_snapshots SET source = 'claude-code' WHERE TRIM(source) = ''`,
		`UPDATE todos SET source = 'claude-code' WHERE TRIM(source) = ''`,
		`UPDATE file_history SET source = 'claude-code' WHERE TRIM(source) = ''`,
		`UPDATE sessions SET source = 'claude-code' WHERE TRIM(source) = ''`,
		`UPDATE commands SET source = 'claude-code' WHERE TRIM(source) = ''`,
		`UPDATE memories SET source = 'claude-code' WHERE TRIM(source) = ''`,
		`UPDATE history SET source = 'claude-code' WHERE TRIM(source) = ''`,
		`UPDATE history SET project_dir = project WHERE project_dir = '' AND project <> ''`,
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("backfilling v16 metadata: %w", err)
		}
	}
	return nil
}
