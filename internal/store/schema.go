package store

const schemaVersion = 2

const schema = `
CREATE TABLE IF NOT EXISTS meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS projects (
	id           INTEGER PRIMARY KEY,
	dir_name     TEXT NOT NULL UNIQUE,
	display_name TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
	id                 INTEGER PRIMARY KEY,
	session_id         TEXT NOT NULL UNIQUE,
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
	estimated_tokens   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_sessions_project ON sessions(project_id);
CREATE INDEX IF NOT EXISTS idx_sessions_modified ON sessions(modified_at DESC);

CREATE TABLE IF NOT EXISTS messages (
	id         INTEGER PRIMARY KEY,
	session_id INTEGER NOT NULL REFERENCES sessions(id),
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
	session_id   INTEGER REFERENCES sessions(id),
	item_count   INTEGER NOT NULL DEFAULT 0,
	statuses     TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS todo_items (
	id          INTEGER PRIMARY KEY,
	todo_id     INTEGER NOT NULL REFERENCES todos(id),
	seq         INTEGER NOT NULL,
	content     TEXT NOT NULL DEFAULT '',
	status      TEXT NOT NULL DEFAULT '',
	active_form TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_todo_items_todo ON todo_items(todo_id, seq);

CREATE TABLE IF NOT EXISTS file_history (
	id              INTEGER PRIMARY KEY,
	conversation_id TEXT NOT NULL UNIQUE,
	session_id      INTEGER REFERENCES sessions(id),
	file_count      INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS file_versions (
	id              INTEGER PRIMARY KEY,
	file_history_id INTEGER NOT NULL REFERENCES file_history(id),
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

CREATE TABLE IF NOT EXISTS source_files (
	path       TEXT PRIMARY KEY,
	mtime_ns   INTEGER NOT NULL DEFAULT 0,
	indexed_at TEXT NOT NULL DEFAULT ''
);
`
