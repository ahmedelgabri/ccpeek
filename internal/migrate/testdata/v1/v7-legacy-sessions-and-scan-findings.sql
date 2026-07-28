PRAGMA foreign_keys = ON;

CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
INSERT INTO meta (key, value) VALUES ('schema_version', '7');

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

CREATE TABLE commands (
  id         INTEGER PRIMARY KEY,
  session_id INTEGER NOT NULL REFERENCES sessions(id),
  seq        INTEGER NOT NULL,
  command    TEXT NOT NULL,
  timestamp  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_commands_session ON commands(session_id, seq);
CREATE INDEX idx_commands_ts ON commands(timestamp DESC);

CREATE TABLE scan_findings (
  id             INTEGER PRIMARY KEY,
  rule_id        TEXT NOT NULL DEFAULT '',
  description    TEXT NOT NULL DEFAULT '',
  source_type    TEXT NOT NULL DEFAULT '',
  source_id      TEXT NOT NULL DEFAULT '',
  match_redacted TEXT NOT NULL DEFAULT '',
  line_number    INTEGER NOT NULL DEFAULT 0,
  scanned_at     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_scan_findings_rule ON scan_findings(rule_id);
CREATE INDEX idx_scan_findings_type ON scan_findings(source_type);

INSERT INTO projects (id, dir_name, display_name)
VALUES (1, '-Users-me-legacy-project', '-Users-me-legacy-project');

INSERT INTO sessions (
  id, session_id, project_id, first_prompt, message_count, created_at, modified_at,
  git_branch, project_path, bash_command_count, tool_use_counts, estimated_tokens, source_path
) VALUES (
  1, 'legacy-session', 1, 'Legacy fixture should survive migrations', 2,
  '2024-01-03T00:00:00Z', '2024-01-03T00:00:05Z', 'main',
  '/Users/me/legacy-project', 1, '{"Bash":1}', 21, '/src/legacy-session.jsonl'
);

INSERT INTO messages (id, session_id, seq, type, role, timestamp, uuid, content, cwd, git_branch)
VALUES
  (
    1,
    1,
    0,
    'assistant',
    'assistant',
    '2024-01-03T00:00:01Z',
    'legacy-msg-1',
    '[{"type":"tool_use","id":"legacy-tu1","name":"Bash","input":{"command":"echo legacy"}},{"type":"text","text":"Running legacy command."}]',
    '/Users/me/legacy-project',
    'main'
  ),
  (
    2,
    1,
    1,
    'user',
    'user',
    '2024-01-03T00:00:02Z',
    'legacy-msg-2',
    '[{"type":"tool_result","tool_use_id":"legacy-tu1","content":"legacy ok"}]',
    '/Users/me/legacy-project',
    'main'
  );

INSERT INTO messages_fts (rowid, text_content)
VALUES
  (1, 'Running legacy command.'),
  (2, 'legacy ok');

INSERT INTO commands (id, session_id, seq, command, timestamp)
VALUES (1, 1, 0, 'echo legacy', '2024-01-03T00:00:01Z');

INSERT INTO scan_findings (id, rule_id, description, source_type, source_id, match_redacted, line_number, scanned_at)
VALUES (1, 'legacy-secret', 'Legacy secret finding', 'plan', 'legacy-plan.md', 'sk-****', 7, '2024-01-03T00:00:03Z');
