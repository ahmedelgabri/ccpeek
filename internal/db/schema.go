package db

import (
	"context"
	"database/sql"
)

// schemaVersion is the current v2 schema version. Unlike v1, initialSchema
// is ALWAYS the latest schema: fresh databases are created at
// schemaVersion directly and never replay migrations. Existing databases
// run only the pending entries of migrations. Open-time backfills are
// banned (docs/v2-plan.md §5.2, §8.1); backfill-style work ships as an
// explicit migration with a version bump.
const schemaVersion = 1

// derivedSchema holds everything rebuildable from agent sources. ResetDerived
// may drop and recreate all of it.
const derivedSchema = `
CREATE TABLE IF NOT EXISTS agents (
	id INTEGER PRIMARY KEY,
	slug TEXT NOT NULL UNIQUE,
	display_name TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS pricing (
	model_key TEXT NOT NULL,
	effective_from TEXT NOT NULL DEFAULT '',
	input_per_mtok REAL,
	output_per_mtok REAL,
	cache_write_per_mtok REAL,
	cache_read_per_mtok REAL,
	source TEXT NOT NULL DEFAULT '',
	fetched_at TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (model_key, effective_from)
);

-- The hub. (agent_id, external_id) is the natural key; cwd/repo/git are
-- context attributes, source_path is provenance — never hierarchy.
CREATE TABLE IF NOT EXISTS sessions (
	id INTEGER PRIMARY KEY,
	agent_id INTEGER NOT NULL REFERENCES agents(id),
	external_id TEXT NOT NULL,
	title TEXT NOT NULL DEFAULT '',
	created_at TEXT,
	modified_at TEXT,
	cwd TEXT NOT NULL DEFAULT '',
	repo_root TEXT NOT NULL DEFAULT '',
	git_branch TEXT NOT NULL DEFAULT '',
	origin TEXT NOT NULL DEFAULT 'ingest',
	source_path TEXT NOT NULL DEFAULT '',
	content_hash TEXT NOT NULL DEFAULT '',
	UNIQUE (agent_id, external_id)
);
CREATE INDEX IF NOT EXISTS idx_sessions_modified ON sessions(modified_at DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_created ON sessions(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_cwd ON sessions(cwd);
CREATE INDEX IF NOT EXISTS idx_sessions_source ON sessions(source_path);

CREATE TABLE IF NOT EXISTS session_relations (
	from_session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	to_session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	kind TEXT NOT NULL,
	evidence_json TEXT NOT NULL DEFAULT '{}',
	PRIMARY KEY (from_session_id, to_session_id, kind)
);
CREATE INDEX IF NOT EXISTS idx_session_relations_to ON session_relations(to_session_id);

-- Relation edges whose endpoint session hasn't been ingested yet; resolved
-- opportunistically at the end of each ingest run.
CREATE TABLE IF NOT EXISTS pending_relations (
	agent_id INTEGER NOT NULL REFERENCES agents(id),
	from_external_id TEXT NOT NULL,
	to_external_id TEXT NOT NULL,
	kind TEXT NOT NULL,
	evidence_json TEXT NOT NULL DEFAULT '{}',
	PRIMARY KEY (agent_id, from_external_id, to_external_id, kind)
);

CREATE TABLE IF NOT EXISTS messages (
	id INTEGER PRIMARY KEY,
	session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	seq INTEGER NOT NULL,
	external_id TEXT NOT NULL DEFAULT '',
	parent_external_id TEXT NOT NULL DEFAULT '',
	role TEXT NOT NULL,
	kind TEXT NOT NULL DEFAULT 'message',
	created_at TEXT,
	model TEXT NOT NULL DEFAULT '',
	cwd TEXT NOT NULL DEFAULT '',
	content TEXT NOT NULL DEFAULT '',
	UNIQUE (session_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_messages_created ON messages(created_at);
CREATE INDEX IF NOT EXISTS idx_messages_model ON messages(model) WHERE model <> '';

CREATE TABLE IF NOT EXISTS message_usage (
	message_id INTEGER PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	cache_read_tokens INTEGER NOT NULL DEFAULT 0,
	cache_write_tokens INTEGER NOT NULL DEFAULT 0,
	reasoning_tokens INTEGER NOT NULL DEFAULT 0,
	service_tier TEXT NOT NULL DEFAULT '',
	reported_cost_usd REAL,
	request_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_message_usage_request ON message_usage(request_id) WHERE request_id <> '';

CREATE TABLE IF NOT EXISTS tool_calls (
	id INTEGER PRIMARY KEY,
	session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	message_seq INTEGER NOT NULL DEFAULT 0,
	seq INTEGER NOT NULL,
	name TEXT NOT NULL,
	kind TEXT NOT NULL DEFAULT 'other',
	input_json TEXT NOT NULL DEFAULT '{}',
	result_status TEXT NOT NULL DEFAULT '',
	result_excerpt TEXT NOT NULL DEFAULT '',
	file_path TEXT NOT NULL DEFAULT '',
	started_at TEXT,
	UNIQUE (session_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_tool_calls_kind ON tool_calls(kind, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_tool_calls_name ON tool_calls(name);
CREATE INDEX IF NOT EXISTS idx_tool_calls_file ON tool_calls(file_path) WHERE file_path <> '';

-- Artifacts stand alone; sessions attach via artifact_sessions with
-- explicit relation + evidence.
CREATE TABLE IF NOT EXISTS artifacts (
	id INTEGER PRIMARY KEY,
	agent_id INTEGER NOT NULL REFERENCES agents(id),
	kind TEXT NOT NULL,
	name TEXT NOT NULL,
	content TEXT NOT NULL DEFAULT '',
	metadata_json TEXT NOT NULL DEFAULT '{}',
	source_path TEXT NOT NULL DEFAULT '',
	content_hash TEXT NOT NULL DEFAULT '',
	UNIQUE (agent_id, kind, name)
);
CREATE INDEX IF NOT EXISTS idx_artifacts_source ON artifacts(source_path);

CREATE TABLE IF NOT EXISTS artifact_sessions (
	artifact_id INTEGER NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE,
	session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	relation TEXT NOT NULL,
	evidence TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (artifact_id, session_id, relation)
);
CREATE INDEX IF NOT EXISTS idx_artifact_sessions_session ON artifact_sessions(session_id);

-- Derived grouping facet over sessions.cwd. Regenerated at ingest; powers
-- the Projects view but is never a parent container.
CREATE TABLE IF NOT EXISTS workspaces (
	id INTEGER PRIMARY KEY,
	canonical_path TEXT NOT NULL UNIQUE,
	display_name TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS session_workspaces (
	session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
	PRIMARY KEY (session_id, workspace_id)
);
CREATE INDEX IF NOT EXISTS idx_session_workspaces_ws ON session_workspaces(workspace_id);

CREATE TABLE IF NOT EXISTS history (
	id INTEGER PRIMARY KEY,
	agent_id INTEGER NOT NULL REFERENCES agents(id),
	display TEXT NOT NULL,
	timestamp INTEGER NOT NULL DEFAULT 0,
	session_id INTEGER REFERENCES sessions(id) ON DELETE SET NULL,
	source_path TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_history_ts ON history(timestamp DESC);

CREATE TABLE IF NOT EXISTS source_files (
	path TEXT PRIMARY KEY,
	agent_id INTEGER NOT NULL REFERENCES agents(id),
	content_hash TEXT NOT NULL,
	indexed_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS ingest_runs (
	id INTEGER PRIMARY KEY,
	mode TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT 'running',
	roots_json TEXT NOT NULL DEFAULT '[]',
	started_at TEXT NOT NULL,
	finished_at TEXT,
	duration_ms INTEGER NOT NULL DEFAULT 0,
	files_seen INTEGER NOT NULL DEFAULT 0,
	files_changed INTEGER NOT NULL DEFAULT 0,
	records_indexed INTEGER NOT NULL DEFAULT 0,
	skipped_rows INTEGER NOT NULL DEFAULT 0,
	parse_failures INTEGER NOT NULL DEFAULT 0,
	unresolved_links INTEGER NOT NULL DEFAULT 0,
	warning_count INTEGER NOT NULL DEFAULT 0,
	error_message TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_ingest_runs_started ON ingest_runs(started_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS ingest_issues (
	id INTEGER PRIMARY KEY,
	run_id INTEGER NOT NULL REFERENCES ingest_runs(id) ON DELETE CASCADE,
	severity TEXT NOT NULL,
	category TEXT NOT NULL,
	agent_slug TEXT NOT NULL DEFAULT '',
	source_path TEXT NOT NULL DEFAULT '',
	line_number INTEGER NOT NULL DEFAULT 0,
	detail TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ingest_issues_run ON ingest_issues(run_id, id);

CREATE TABLE IF NOT EXISTS rollup_usage_daily (
	day TEXT NOT NULL,
	agent_id INTEGER NOT NULL,
	workspace_id INTEGER NOT NULL DEFAULT 0,
	model TEXT NOT NULL DEFAULT '',
	sessions INTEGER NOT NULL DEFAULT 0,
	messages INTEGER NOT NULL DEFAULT 0,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	cache_read_tokens INTEGER NOT NULL DEFAULT 0,
	cache_write_tokens INTEGER NOT NULL DEFAULT 0,
	cost_usd REAL NOT NULL DEFAULT 0,
	PRIMARY KEY (day, agent_id, workspace_id, model)
);

-- Secret-scan findings are derived (rescannable); the user's ignore
-- decisions live in user_annotations keyed by natural key.
CREATE TABLE IF NOT EXISTS scan_findings (
	id INTEGER PRIMARY KEY,
	rule_id TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	entity_type TEXT NOT NULL,
	natural_key TEXT NOT NULL,
	match_redacted TEXT NOT NULL DEFAULT '',
	line_number INTEGER NOT NULL DEFAULT 0,
	scanned_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_scan_findings_rule ON scan_findings(rule_id);
CREATE INDEX IF NOT EXISTS idx_scan_findings_entity ON scan_findings(entity_type, natural_key);
`

// derivedVirtualSchema is created outside transactions where required by
// the driver; kept separate so ResetDerived can recreate it too.
const derivedVirtualSchema = `
CREATE VIRTUAL TABLE IF NOT EXISTS search_fts USING fts5(
	doc_type UNINDEXED,
	title UNINDEXED,
	url UNINDEXED,
	text_content
);
`

// userSchema holds user-created state. It is NEVER dropped by
// ResetDerived / --rebuild. Rows attach to entities via natural keys
// (e.g. "agent_slug/session_external_id") so they survive re-ingest.
const userSchema = `
CREATE TABLE IF NOT EXISTS user_annotations (
	id INTEGER PRIMARY KEY,
	entity_type TEXT NOT NULL,
	natural_key TEXT NOT NULL,
	kind TEXT NOT NULL,
	value_json TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL,
	UNIQUE (entity_type, natural_key, kind)
);
`

// derivedTables lists every table ResetDerived drops, in an order that
// respects foreign keys (children first). search_fts is handled separately.
var derivedTables = []string{
	"scan_findings",
	"rollup_usage_daily",
	"ingest_issues",
	"ingest_runs",
	"source_files",
	"history",
	"session_workspaces",
	"workspaces",
	"artifact_sessions",
	"artifacts",
	"tool_calls",
	"message_usage",
	"messages",
	"pending_relations",
	"session_relations",
	"sessions",
	"pricing",
	"agents",
}

// migration is a single schema upgrade step. migrations[i] upgrades from
// version i+1 to i+2. Empty at v2 launch by design: fresh databases are
// created at the latest schema and never replay history.
type migration func(ctx context.Context, tx *sql.Tx) error

var migrations []migration
