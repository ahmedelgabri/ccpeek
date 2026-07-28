PRAGMA foreign_keys = ON;

CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
INSERT INTO meta (key, value) VALUES ('schema_version', '14');

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
CREATE INDEX idx_messages_session ON messages(session_id, seq);

CREATE VIRTUAL TABLE messages_fts USING fts5(text_content);

CREATE TABLE plans (
  id         INTEGER PRIMARY KEY,
  file_name  TEXT NOT NULL UNIQUE,
  title      TEXT NOT NULL,
  size_bytes INTEGER NOT NULL DEFAULT 0,
  content    TEXT NOT NULL DEFAULT ''
);

CREATE TABLE source_files (
  path         TEXT PRIMARY KEY,
  content_hash TEXT NOT NULL DEFAULT '',
  indexed_at   TEXT NOT NULL DEFAULT ''
);

INSERT INTO projects (id, dir_name, display_name, canonical_path)
VALUES (1, '-Users-me-damaged-project', '-Users-me-damaged-project', '/Users/me/damaged-project');

INSERT INTO sessions (
  id, session_id, project_id, first_prompt, message_count, created_at, modified_at,
  git_branch, project_path, bash_command_count, tool_use_counts, estimated_tokens, source_path
) VALUES (
  1, 'damaged-session', 1, 'Recover missing derived tables', 2,
  '2024-01-06T00:00:00Z', '2024-01-06T00:00:05Z', 'main',
  '/Users/me/damaged-project', 1, '{"Bash":1}', 13, '/src/damaged-session.jsonl'
);

INSERT INTO messages (id, session_id, seq, type, role, timestamp, uuid, content, cwd, git_branch)
VALUES
  (
    1,
    1,
    0,
    'assistant',
    'assistant',
    '2024-01-06T00:00:01Z',
    'damaged-msg-1',
    '[{"type":"tool_use","id":"damaged-tu1","name":"Bash","input":{"command":"echo damaged"}},{"type":"text","text":"Running damaged command."}]',
    '/Users/me/damaged-project',
    'main'
  ),
  (
    2,
    1,
    1,
    'user',
    'user',
    '2024-01-06T00:00:02Z',
    'damaged-msg-2',
    '[{"type":"tool_result","tool_use_id":"damaged-tu1","content":"damaged ok"}]',
    '/Users/me/damaged-project',
    'main'
  );

INSERT INTO messages_fts (rowid, text_content)
VALUES
  (1, 'Running damaged command.'),
  (2, 'damaged ok');

INSERT INTO plans (id, file_name, title, size_bytes, content)
VALUES (1, 'damaged-plan.md', 'Damaged Fixture Plan', 28, 'Plan content used to rebuild search documents.');

INSERT INTO source_files (path, content_hash, indexed_at)
VALUES ('/src/damaged-session.jsonl', 'damaged-hash', '2024-01-06T00:00:10Z');

-- The v14 memories shape: source_path but no file_name column (the
-- v14->v15 migration adds file_name, backfilling 'MEMORY.md').
CREATE TABLE memories (
  id          INTEGER PRIMARY KEY,
  project_dir TEXT NOT NULL UNIQUE,
  project_id  INTEGER REFERENCES projects(id) ON DELETE SET NULL,
  size_bytes  INTEGER NOT NULL DEFAULT 0,
  content     TEXT NOT NULL DEFAULT '',
  source_path TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_memories_source ON memories(source_path);

INSERT INTO memories (id, project_dir, project_id, size_bytes, content, source_path)
VALUES (1, '-Users-me-damaged-project', 1, 24, 'Remember the damaged fix.', '/gone/memory/MEMORY.md');
