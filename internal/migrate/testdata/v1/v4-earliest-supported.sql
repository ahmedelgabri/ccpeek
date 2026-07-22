PRAGMA foreign_keys = ON;

CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
INSERT INTO meta (key, value) VALUES ('schema_version', '4');

CREATE TABLE projects (
  id           INTEGER PRIMARY KEY,
  dir_name     TEXT NOT NULL UNIQUE,
  display_name TEXT NOT NULL
);

CREATE TABLE sessions (
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
CREATE INDEX idx_sessions_project ON sessions(project_id);
CREATE INDEX idx_sessions_modified ON sessions(modified_at DESC);

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

CREATE TABLE source_files (
  path      TEXT PRIMARY KEY,
  mtime_ns  INTEGER NOT NULL DEFAULT 0,
  indexed_at TEXT NOT NULL DEFAULT ''
);

INSERT INTO projects (id, dir_name, display_name)
VALUES (1, '-Users-me-earliest-project', '-Users-me-earliest-project');

INSERT INTO sessions (
  id, session_id, project_id, first_prompt, message_count, created_at, modified_at,
  git_branch, project_path, bash_command_count, tool_use_counts, estimated_tokens
) VALUES (
  1, 'earliest-session', 1, 'Earliest supported fixture', 2,
  '2024-01-01T00:00:00Z', '2024-01-01T00:00:05Z', 'main',
  '/Users/me/earliest-project', 1, '{"Bash":1}', 10
);

INSERT INTO messages (id, session_id, seq, type, role, timestamp, uuid, content, cwd, git_branch)
VALUES
  (
    1,
    1,
    0,
    'assistant',
    'assistant',
    '2024-01-01T00:00:01Z',
    'earliest-msg-1',
    '[{"type":"tool_use","id":"earliest-tu1","name":"Bash","input":{"command":"echo earliest"}},{"type":"text","text":"Running earliest command."}]',
    '/Users/me/earliest-project',
    'main'
  ),
  (
    2,
    1,
    1,
    'user',
    'user',
    '2024-01-01T00:00:02Z',
    'earliest-msg-2',
    '[{"type":"tool_result","tool_use_id":"earliest-tu1","content":"earliest ok"}]',
    '/Users/me/earliest-project',
    'main'
  );

INSERT INTO messages_fts (rowid, text_content)
VALUES
  (1, 'Running earliest command.'),
  (2, 'earliest ok');

INSERT INTO source_files (path, mtime_ns, indexed_at)
VALUES ('/src/earliest-session.jsonl', 123456789, '2024-01-01T00:00:10Z');
