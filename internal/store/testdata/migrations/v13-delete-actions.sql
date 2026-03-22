PRAGMA foreign_keys = ON;

CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
INSERT INTO meta (key, value) VALUES ('schema_version', '13');

CREATE TABLE projects (
  id             INTEGER PRIMARY KEY,
  dir_name       TEXT NOT NULL UNIQUE,
  display_name   TEXT NOT NULL,
  canonical_path TEXT NOT NULL DEFAULT ''
);

CREATE TABLE sessions (
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
);
CREATE INDEX idx_sessions_project ON sessions(project_id);
CREATE INDEX idx_sessions_modified ON sessions(modified_at DESC);
CREATE INDEX idx_sessions_source ON sessions(source_path);

CREATE TABLE messages (
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
CREATE INDEX idx_messages_session ON messages(session_id, seq);

CREATE VIRTUAL TABLE messages_fts USING fts5(text_content);
CREATE VIRTUAL TABLE search_documents_fts USING fts5(
  group_type UNINDEXED,
  title UNINDEXED,
  subtitle UNINDEXED,
  url UNINDEXED,
  text_content
);

CREATE TABLE tool_calls (
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
);
CREATE INDEX idx_tool_calls_session ON tool_calls(session_id, seq);
CREATE INDEX idx_tool_calls_kind ON tool_calls(tool_kind, timestamp DESC);
CREATE INDEX idx_tool_calls_name ON tool_calls(tool_name);

CREATE TABLE commands (
  id         INTEGER PRIMARY KEY,
  session_id INTEGER NOT NULL REFERENCES sessions(id),
  seq        INTEGER NOT NULL,
  command    TEXT NOT NULL,
  timestamp  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_commands_session ON commands(session_id, seq);
CREATE INDEX idx_commands_ts ON commands(timestamp DESC);

CREATE TABLE todos (
  id          INTEGER PRIMARY KEY,
  file_name   TEXT NOT NULL UNIQUE,
  session_id  INTEGER REFERENCES sessions(id),
  item_count  INTEGER NOT NULL DEFAULT 0,
  statuses    TEXT NOT NULL DEFAULT '{}',
  source_path TEXT NOT NULL DEFAULT ''
);

CREATE TABLE todo_items (
  id          INTEGER PRIMARY KEY,
  todo_id     INTEGER NOT NULL REFERENCES todos(id),
  seq         INTEGER NOT NULL,
  content     TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT '',
  active_form TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_todo_items_todo ON todo_items(todo_id, seq);

CREATE TABLE ingest_runs (
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
CREATE INDEX idx_ingest_runs_started ON ingest_runs(started_at DESC, id DESC);

CREATE TABLE ingest_issues (
  id          INTEGER PRIMARY KEY,
  run_id      INTEGER NOT NULL REFERENCES ingest_runs(id),
  severity    TEXT NOT NULL DEFAULT '',
  category    TEXT NOT NULL DEFAULT '',
  source_type TEXT NOT NULL DEFAULT '',
  source_path TEXT NOT NULL DEFAULT '',
  line_number INTEGER NOT NULL DEFAULT 0,
  detail      TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_ingest_issues_run ON ingest_issues(run_id, id);

INSERT INTO projects (id, dir_name, display_name, canonical_path)
VALUES (1, 'proj', 'Project', '/Users/me/proj');

INSERT INTO sessions (id, session_id, project_id, source_path)
VALUES (1, 'sess-1', 1, '/src/sess-1.jsonl');

INSERT INTO messages (id, session_id, seq, type, role, timestamp, uuid, content)
VALUES (1, 1, 0, 'assistant', 'assistant', '2024-01-02T00:00:01Z', 'msg-1', '"hello"');

INSERT INTO messages_fts (rowid, text_content)
VALUES (1, 'hello');

INSERT INTO tool_calls (id, session_id, seq, timestamp, tool_name, tool_kind, input_json, result_text, searchable_text)
VALUES (1, 1, 0, '2024-01-02T00:00:01Z', 'Bash', 'shell', '{"command":"ls"}', 'ok', 'Bash\nls\nok');

INSERT INTO commands (id, session_id, seq, command, timestamp)
VALUES (1, 1, 0, 'ls', '2024-01-02T00:00:01Z');

INSERT INTO todos (id, file_name, session_id, item_count, statuses, source_path)
VALUES (1, 'todo.json', 1, 1, '{}', '/src/todo.json');

INSERT INTO todo_items (id, todo_id, seq, content, status, active_form)
VALUES (1, 1, 0, 'fix bug', 'pending', 'Fix bug');

INSERT INTO ingest_runs (id, mode, status, started_at)
VALUES (1, 'full', 'warning', '2024-01-02T00:00:00Z');

INSERT INTO ingest_issues (id, run_id, severity, category, source_type, source_path, detail, created_at)
VALUES (1, 1, 'warning', 'parse_failure', 'session', '/src/sess-1.jsonl', 'bad row', '2024-01-02T00:00:03Z');
