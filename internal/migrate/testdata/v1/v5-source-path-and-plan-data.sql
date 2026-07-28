PRAGMA foreign_keys = ON;

CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
INSERT INTO meta (key, value) VALUES ('schema_version', '5');

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
  estimated_tokens   INTEGER NOT NULL DEFAULT 0,
  source_path        TEXT NOT NULL DEFAULT ''
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

CREATE TABLE plans (
  id          INTEGER PRIMARY KEY,
  file_name   TEXT NOT NULL UNIQUE,
  title       TEXT NOT NULL,
  size_bytes  INTEGER NOT NULL DEFAULT 0,
  content     TEXT NOT NULL DEFAULT '',
  source_path TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_plans_source ON plans(source_path);

CREATE TABLE source_files (
  path         TEXT PRIMARY KEY,
  content_hash TEXT NOT NULL DEFAULT '',
  indexed_at   TEXT NOT NULL DEFAULT ''
);

INSERT INTO projects (id, dir_name, display_name)
VALUES (1, '-Users-me-v5-project', '-Users-me-v5-project');

INSERT INTO sessions (
  id, session_id, project_id, first_prompt, message_count, created_at, modified_at,
  git_branch, project_path, bash_command_count, tool_use_counts, estimated_tokens, source_path
) VALUES (
  1, 'v5-session', 1, 'V5 fixture should preserve source paths', 2,
  '2024-01-05T00:00:00Z', '2024-01-05T00:00:05Z', 'main',
  '/Users/me/v5-project', 1, '{"Bash":1}', 11, '/src/v5-session.jsonl'
);

INSERT INTO messages (id, session_id, seq, type, role, timestamp, uuid, content, cwd, git_branch)
VALUES
  (
    1,
    1,
    0,
    'assistant',
    'assistant',
    '2024-01-05T00:00:01Z',
    'v5-msg-1',
    '[{"type":"tool_use","id":"v5-tu1","name":"Bash","input":{"command":"echo v5"}},{"type":"text","text":"Running v5 command."}]',
    '/Users/me/v5-project',
    'main'
  ),
  (
    2,
    1,
    1,
    'user',
    'user',
    '2024-01-05T00:00:02Z',
    'v5-msg-2',
    '[{"type":"tool_result","tool_use_id":"v5-tu1","content":"v5 ok"}]',
    '/Users/me/v5-project',
    'main'
  );

INSERT INTO messages_fts (rowid, text_content)
VALUES
  (1, 'Running v5 command.'),
  (2, 'v5 ok');

INSERT INTO plans (id, file_name, title, size_bytes, content, source_path)
VALUES (1, 'v5-plan.md', 'V5 Plan', 23, 'Document migration-safe plan content.', '/src/v5-plan.md');

INSERT INTO source_files (path, content_hash, indexed_at)
VALUES ('/src/v5-session.jsonl', 'v5-hash', '2024-01-05T00:00:10Z');
