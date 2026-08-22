package db

import (
	"context"
	"database/sql"
)

// schemaVersion is the current schema version. initialSchema is ALWAYS
// the latest schema: fresh databases are created at schemaVersion
// directly and never replay migrations; existing databases run only the
// pending entries of migrations. Open-time backfills stay banned.
//
// The baseline FROZE at v13 with the v2.0.0 release. Every schema change
// from here ships as an entry in migrations and bumps this constant —
// never an edit to initialSchema alone, and never a re-create: the store
// is an archive, not a cache — it retains sessions whose source files
// were cleaned up (prune is opt-in), v1-imported orphans, and user
// annotations, none of which a rebuild-from-sources could restore.
// Migrating in place also keeps startup instant instead of re-ingesting
// the corpus. (initialSchema still moves WITH each migration so fresh
// databases are born at the latest version; the migration entry is what
// carries existing archives forward.)
const schemaVersion = 15

// baseVersion is the oldest schema version this build can upgrade from:
// migrations[i] upgrades baseVersion+i to baseVersion+i+1, so
// len(migrations) == schemaVersion - baseVersion always holds. It froze
// at the v2.0.0 baseline (v13) and never moves again; only databases
// from pre-release builds (stamped below it) are refused with re-create
// instructions.
const baseVersion = 13

// Two partial indexes are PINNED with INDEXED BY by the queries they
// exist for — the planner will not choose either on its own. Their names
// are constants rather than literals so those pins are compile-checked:
// internal/query is a different package, and a rename there would
// otherwise fail only at runtime, with "no such index", and only if a
// test happened to exercise that query.
const (
	IdxToolCallsRecentFiles = "idx_tool_calls_recent_files"
	IdxToolCallsFileWrites  = "idx_tool_calls_file_writes"
)

// derivedSchema holds everything rebuildable from agent sources. ResetDerived
// may drop and recreate all of it.
const derivedSchema = `
CREATE TABLE IF NOT EXISTS agents (
	id INTEGER PRIMARY KEY,
	slug TEXT NOT NULL UNIQUE,
	display_name TEXT NOT NULL DEFAULT ''
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
--
-- attempts counts the resolution passes a row has survived. A link whose
-- endpoint is simply not indexed YET (a session about to be discovered in
-- the same run, a transcript restored later) resolves within a pass or
-- two; one that will NEVER resolve would otherwise sit here for the life
-- of the database, be re-scanned every pass, and inflate the
-- unresolved_links health figure. See pendingLinkAttemptLimit.
CREATE TABLE IF NOT EXISTS pending_relations (
	agent_id INTEGER NOT NULL REFERENCES agents(id),
	from_external_id TEXT NOT NULL,
	to_external_id TEXT NOT NULL,
	kind TEXT NOT NULL,
	evidence_json TEXT NOT NULL DEFAULT '{}',
	attempts INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (agent_id, from_external_id, to_external_id, kind)
);

CREATE TABLE IF NOT EXISTS messages (
	id INTEGER PRIMARY KEY,
	session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	seq INTEGER NOT NULL,
	external_id TEXT NOT NULL DEFAULT '',
	parent_external_id TEXT NOT NULL DEFAULT '',
	content_id TEXT NOT NULL DEFAULT '',
	role TEXT NOT NULL,
	kind TEXT NOT NULL DEFAULT 'message',
	created_at TEXT,
	provider TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	cwd TEXT NOT NULL DEFAULT '',
	is_sidechain INTEGER NOT NULL DEFAULT 0,
	content TEXT NOT NULL DEFAULT '',
	UNIQUE (session_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_messages_created ON messages(created_at);
CREATE INDEX IF NOT EXISTS idx_messages_model ON messages(model) WHERE model <> '';
CREATE INDEX IF NOT EXISTS idx_messages_content_id ON messages(content_id) WHERE content_id <> '';

CREATE TABLE IF NOT EXISTS message_usage (
	message_id INTEGER PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	cache_read_tokens INTEGER NOT NULL DEFAULT 0,
	cache_write_tokens INTEGER NOT NULL DEFAULT 0,
	-- One-hour-TTL subset of cache_write_tokens. Zero on legacy rows whose
	-- source did not expose (or no longer exists to recover) the split.
	cache_write_1h_tokens INTEGER NOT NULL DEFAULT 0,
	reasoning_tokens INTEGER NOT NULL DEFAULT 0,
	service_tier TEXT NOT NULL DEFAULT '',
	reported_cost_usd REAL,
	-- Exact fixed-point mirror used for all arithmetic. REAL remains as a
	-- compatibility/provenance column for existing tooling.
	reported_cost_nanos INTEGER,
	request_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_message_usage_request ON message_usage(request_id) WHERE request_id <> '';

CREATE TABLE IF NOT EXISTS tool_calls (
	id INTEGER PRIMARY KEY,
	session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	message_seq INTEGER NOT NULL DEFAULT 0,
	seq INTEGER NOT NULL,
	-- Agent-native call id (e.g. a tool_use block id): lets a result that
	-- arrives in later-appended source bytes attach to its call.
	external_id TEXT NOT NULL DEFAULT '',
	name TEXT NOT NULL,
	kind TEXT NOT NULL DEFAULT 'other',
	input_json TEXT NOT NULL DEFAULT '{}',
	result_status TEXT NOT NULL DEFAULT '',
	result_excerpt TEXT NOT NULL DEFAULT '',
	-- Normalized arguments, lifted out of input_json by the adapter that
	-- knows the agent's shape (see canon.ToolCall). Cross-agent surfaces
	-- read these; input_json keeps the native form verbatim.
	file_path TEXT NOT NULL DEFAULT '',
	command TEXT NOT NULL DEFAULT '',
	old_text TEXT NOT NULL DEFAULT '',
	new_text TEXT NOT NULL DEFAULT '',
	started_at TEXT,
	UNIQUE (session_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_tool_calls_kind ON tool_calls(kind, started_at DESC);
-- Result pairing: streaming adapters attach every tool result through
-- UPDATE ... WHERE session_id = ? AND external_id = ?; without this the
-- unique (session_id, seq) prefix makes each update scan the session.
CREATE INDEX IF NOT EXISTS idx_tool_calls_external ON tool_calls(session_id, external_id) WHERE external_id <> '';
CREATE INDEX IF NOT EXISTS idx_tool_calls_name ON tool_calls(name);
CREATE INDEX IF NOT EXISTS idx_tool_calls_file ON tool_calls(file_path) WHERE file_path <> '';
-- The Overview's recent-file-edits feed: newest write/edit first, capped
-- at 120. Its predicate is spelled here literally so the planner uses the
-- index for both the filter AND the order, letting the LIMIT stop early
-- instead of sorting every file-touching call in the corpus.
CREATE INDEX IF NOT EXISTS ` + IdxToolCallsRecentFiles + `
	ON tool_calls(started_at DESC)
	WHERE kind IN ('file_write', 'file_edit') AND file_path <> '';
-- A COVERING index over file-touching calls, for the link rules that match
-- on a path substring (canon.ToolCallSelector.FilePathContains). A
-- leading-wildcard LIKE cannot seek, so the substring test has to scan —
-- but over index pages rather than faulting in each matching row's table
-- page, which carries input_json and the 16 KiB-capped old/new text. Every
-- column the rule engine reads is here.
--
-- It used to bake one agent's memory-directory layout into its WHERE
-- clause, which made the store's schema depend on Claude Code's on-disk
-- format.
CREATE INDEX IF NOT EXISTS ` + IdxToolCallsFileWrites + `
	ON tool_calls(file_path, session_id, message_seq, name, kind, input_json)
	WHERE kind IN ('file_write', 'file_edit');

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
	-- The message seq of the tool call that produced this link, recorded by
	-- the resolver that matched it. NULL for links from other evidence
	-- (id_match, filename_uuid, adapter emits), where the read path falls
	-- back to the generic last-producer heuristic.
	anchor_seq INTEGER,
	PRIMARY KEY (artifact_id, session_id, relation)
);
CREATE INDEX IF NOT EXISTS idx_artifact_sessions_session ON artifact_sessions(session_id);
-- The link resolvers reconcile by (relation, evidence): without this they
-- SCAN every link row in the database — one per paste, snapshot, todo list
-- and file-history link — and probe artifacts for each, twice per ingest
-- pass, just to find the content_ref ones.
CREATE INDEX IF NOT EXISTS idx_artifact_sessions_resolver
	ON artifact_sessions(relation, evidence, artifact_id);

-- Artifact→session links whose session hasn't been ingested yet; resolved
-- opportunistically at the end of each ingest run. attempts ages out rows
-- that will never resolve — see pending_relations.
CREATE TABLE IF NOT EXISTS pending_artifact_links (
	artifact_id INTEGER NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE,
	agent_id INTEGER NOT NULL REFERENCES agents(id),
	session_external_id TEXT NOT NULL,
	relation TEXT NOT NULL,
	evidence TEXT NOT NULL DEFAULT '',
	attempts INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (artifact_id, session_external_id, relation)
);

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

-- stat_sig is a cheap size+mtime fingerprint checked before content
-- hashing: matching stat means "unchanged" without reading a byte, so
-- warm startups don't re-read multi-GB histories. Content hash stays the
-- source of truth when the stat differs.
CREATE TABLE IF NOT EXISTS source_files (
	path TEXT PRIMARY KEY,
	agent_id INTEGER NOT NULL REFERENCES agents(id),
	content_hash TEXT NOT NULL,
	stat_sig TEXT NOT NULL DEFAULT '',
	-- Append cursor (agent.TailState JSON) for adapters that can resume
	-- parsing at the byte where the last pass stopped; '' means the next
	-- change re-parses the whole source.
	parse_state TEXT NOT NULL DEFAULT '',
	-- Adapter-owned format version. A bump forces a full parse even when
	-- the source bytes did not change, recovering newly captured fields.
	parse_version INTEGER NOT NULL DEFAULT 1,
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
	-- cost_usd = reported + estimated: what agents said they paid vs what
	-- the pricing table computed for rows with no reported figure.
	cost_reported_usd REAL NOT NULL DEFAULT 0,
	cost_estimated_usd REAL NOT NULL DEFAULT 0,
	-- Exact automatic-cost materialization and provenance.
	cost_nanos INTEGER NOT NULL DEFAULT 0,
	cost_reported_nanos INTEGER NOT NULL DEFAULT 0,
	cost_estimated_nanos INTEGER NOT NULL DEFAULT 0,
	unpriced_input_tokens INTEGER NOT NULL DEFAULT 0,
	unpriced_output_tokens INTEGER NOT NULL DEFAULT 0,
	unpriced_cache_read_tokens INTEGER NOT NULL DEFAULT 0,
	unpriced_cache_write_tokens INTEGER NOT NULL DEFAULT 0,
	priced INTEGER NOT NULL DEFAULT 1, -- 0: at least one non-zero auto bucket has no rate
	PRIMARY KEY (day, agent_id, workspace_id, model)
);

-- Which sessions contributed usage on which day, per rollup dimension.
-- Session counts are NOT additive across rollup_usage_daily rows (one
-- session spanning two models on one day appears in both), so the Usage
-- op used to recompute them with a full message_usage scan on every
-- call — including the usage read the Overview page issues on mount.
-- Regenerating this alongside the rollups turns that into a COUNT over
-- pre-aggregated rows.
CREATE TABLE IF NOT EXISTS rollup_session_days (
	day TEXT NOT NULL,
	agent_id INTEGER NOT NULL,
	workspace_id INTEGER NOT NULL DEFAULT 0,
	model TEXT NOT NULL DEFAULT '',
	session_id INTEGER NOT NULL,
	-- No secondary index: the only reader groups/filters on day, which the
	-- primary key's leading column already covers. A session_id index was
	-- never seeked and was rebuilt over every row on every regeneration.
	PRIMARY KEY (day, agent_id, workspace_id, model, session_id)
);

-- Secret-scan findings are derived (rescannable); the user's ignore
-- decisions live in user_annotations keyed by natural key.
-- scan_state records the content hash each entity carried when it was
-- last scanned, making rescans incremental: only entities whose hash
-- moved (or that were never scanned) are re-examined.
CREATE TABLE IF NOT EXISTS scan_state (
	entity_type TEXT NOT NULL,
	entity_key TEXT NOT NULL,
	content_hash TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (entity_type, entity_key)
);

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

// derivedVirtualSchema holds the search index: search_docs is the content
// table (locator fields, no presentation URLs — the query layer builds
// links), search_fts is an external-content FTS5 index kept in sync by
// triggers. Explicit deletes in the write layer keep the pair consistent
// without relying on FK-cascade trigger semantics.
const derivedVirtualSchema = `
CREATE TABLE IF NOT EXISTS search_docs (
	id INTEGER PRIMARY KEY,
	session_id INTEGER REFERENCES sessions(id) ON DELETE CASCADE,
	artifact_id INTEGER REFERENCES artifacts(id) ON DELETE CASCADE,
	doc_type TEXT NOT NULL,
	seq INTEGER NOT NULL DEFAULT 0,
	title TEXT NOT NULL DEFAULT '',
	text_content TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_search_docs_artifact ON search_docs(artifact_id);
-- The transcript is not a search query, but it reads through this table:
-- messages has no text column, so query.Transcript LEFT JOINs a message's
-- search doc on (session_id, doc_type, seq). Indexed on session_id alone,
-- that join seeks the session and then SCANS all of its docs for every
-- message in the page — quadratic in session length, and a 6000-message
-- session took 2.5s to return one 1000-message page. All three columns
-- make it a seek. This index also serves the session_id-only lookups the
-- old idx_search_docs_session covered, so that one is gone.
CREATE INDEX IF NOT EXISTS idx_search_docs_msg ON search_docs(session_id, doc_type, seq);

CREATE VIRTUAL TABLE IF NOT EXISTS search_fts USING fts5(
	text_content,
	content='search_docs',
	content_rowid='id'
);

CREATE TRIGGER IF NOT EXISTS search_docs_ai AFTER INSERT ON search_docs BEGIN
	INSERT INTO search_fts(rowid, text_content) VALUES (new.id, new.text_content);
END;
CREATE TRIGGER IF NOT EXISTS search_docs_ad AFTER DELETE ON search_docs BEGIN
	INSERT INTO search_fts(search_fts, rowid, text_content) VALUES ('delete', old.id, old.text_content);
END;
CREATE TRIGGER IF NOT EXISTS search_docs_au AFTER UPDATE ON search_docs BEGIN
	INSERT INTO search_fts(search_fts, rowid, text_content) VALUES ('delete', old.id, old.text_content);
	INSERT INTO search_fts(rowid, text_content) VALUES (new.id, new.text_content);
END;
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
	"scan_state",
	"rollup_session_days",
	"rollup_usage_daily",
	"ingest_issues",
	"ingest_runs",
	"source_files",
	"history",
	"session_workspaces",
	"workspaces",
	"pending_artifact_links",
	"artifact_sessions",
	"artifacts",
	"tool_calls",
	"message_usage",
	"messages",
	"pending_relations",
	"session_relations",
	"sessions",
	"agents",
}

// migration is a single schema upgrade step. migrations[i] upgrades from
// version baseVersion+i to baseVersion+i+1. Fresh databases are created
// at the latest schema and never replay these.
type migration func(ctx context.Context, tx *sql.Tx) error

// migrations carries every post-v2.0 schema change. Existing archives are
// upgraded in place; fresh databases are born directly from initialSchema.
var migrations = []migration{
	func(ctx context.Context, tx *sql.Tx) error {
		for _, q := range []string{
			`ALTER TABLE messages ADD COLUMN provider TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE message_usage ADD COLUMN cache_write_1h_tokens INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE source_files ADD COLUMN parse_version INTEGER NOT NULL DEFAULT 1`,
			`ALTER TABLE rollup_usage_daily ADD COLUMN unpriced_input_tokens INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE rollup_usage_daily ADD COLUMN unpriced_output_tokens INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE rollup_usage_daily ADD COLUMN unpriced_cache_read_tokens INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE rollup_usage_daily ADD COLUMN unpriced_cache_write_tokens INTEGER NOT NULL DEFAULT 0`,
			// Pricing semantics and the rollup input shape changed. Emptying
			// both materializations lets the existing ingest self-heal rebuild
			// them with the new algorithm while preserving the archive.
			`DELETE FROM rollup_session_days`,
			`DELETE FROM rollup_usage_daily`,
		} {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		return nil
	},
	func(ctx context.Context, tx *sql.Tx) error {
		for _, q := range []string{
			`ALTER TABLE message_usage ADD COLUMN reported_cost_nanos INTEGER`,
			`UPDATE message_usage SET reported_cost_nanos = CAST(ROUND(reported_cost_usd * 1000000000) AS INTEGER) WHERE reported_cost_usd IS NOT NULL`,
			`ALTER TABLE rollup_usage_daily ADD COLUMN cost_nanos INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE rollup_usage_daily ADD COLUMN cost_reported_nanos INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE rollup_usage_daily ADD COLUMN cost_estimated_nanos INTEGER NOT NULL DEFAULT 0`,
			`DELETE FROM rollup_session_days`,
			`DELETE FROM rollup_usage_daily`,
		} {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		return nil
	},
}
