PRAGMA foreign_keys = ON;

CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
INSERT INTO meta (key, value) VALUES ('schema_version', '10');

CREATE TABLE projects (
  id           INTEGER PRIMARY KEY,
  dir_name     TEXT NOT NULL UNIQUE,
  display_name TEXT NOT NULL
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

CREATE TABLE commands (
  id         INTEGER PRIMARY KEY,
  session_id INTEGER NOT NULL REFERENCES sessions(id),
  seq        INTEGER NOT NULL,
  command    TEXT NOT NULL,
  timestamp  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_commands_session ON commands(session_id, seq);
CREATE INDEX idx_commands_ts ON commands(timestamp DESC);

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

INSERT INTO projects (id, dir_name, display_name)
VALUES (1, '-Users-me-my-project', '-Users-me-my-project');

INSERT INTO sessions (
  id, session_id, project_id, first_prompt, message_count, created_at, modified_at,
  git_branch, project_path, bash_command_count, tool_use_counts, estimated_tokens, source_path
) VALUES (
  1, 'sess-1', 1, 'Run echo hi during migration coverage', 2,
  '2024-01-02T00:00:00Z', '2024-01-02T00:00:05Z', 'main',
  '/Users/me/my-project', 1, '{"Bash":1}', 42, '/src/sess-1.jsonl'
);

INSERT INTO messages (id, session_id, seq, type, role, timestamp, uuid, content, cwd, git_branch)
VALUES
  (
    1,
    1,
    0,
    'assistant',
    'assistant',
    '2024-01-02T00:00:01Z',
    'msg-1',
    '[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"echo hi"}},{"type":"text","text":"Running echo hi."}]',
    '/Users/me/my-project',
    'main'
  ),
  (
    2,
    1,
    1,
    'user',
    'user',
    '2024-01-02T00:00:02Z',
    'msg-2',
    '[{"type":"tool_result","tool_use_id":"tu1","content":"ok"}]',
    '/Users/me/my-project',
    'main'
  );

INSERT INTO messages_fts (rowid, text_content)
VALUES
  (1, 'Running echo hi.'),
  (2, 'ok');

INSERT INTO commands (id, session_id, seq, command, timestamp)
VALUES (1, 1, 0, 'echo hi', '2024-01-02T00:00:01Z');
