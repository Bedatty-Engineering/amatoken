CREATE TABLE IF NOT EXISTS usage_records (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  message_id      TEXT NOT NULL UNIQUE,
  request_id      TEXT,
  session_id      TEXT NOT NULL,
  project_slug    TEXT NOT NULL,
  cwd             TEXT,
  git_branch      TEXT,
  model           TEXT NOT NULL,
  ts              DATETIME NOT NULL,
  input_tokens            INTEGER NOT NULL DEFAULT 0,
  output_tokens           INTEGER NOT NULL DEFAULT 0,
  cache_creation_tokens   INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens       INTEGER NOT NULL DEFAULT 0,
  source_file     TEXT NOT NULL,
  source_line     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_usage_ts       ON usage_records(ts);
CREATE INDEX IF NOT EXISTS idx_usage_session  ON usage_records(session_id);
CREATE INDEX IF NOT EXISTS idx_usage_model    ON usage_records(model);
CREATE INDEX IF NOT EXISTS idx_usage_project  ON usage_records(project_slug);

CREATE TABLE IF NOT EXISTS ingest_state (
  source_file  TEXT PRIMARY KEY,
  byte_offset  INTEGER NOT NULL,
  last_line    INTEGER NOT NULL,
  updated_at   DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS budgets (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT NOT NULL,
  amount_usd  REAL NOT NULL,
  created_at  DATETIME NOT NULL,
  updated_at  DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS app_settings (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS model_pricing (
  model                    TEXT PRIMARY KEY,
  input_per_mtok_usd       REAL NOT NULL,
  output_per_mtok_usd      REAL NOT NULL,
  cache_write_per_mtok_usd REAL NOT NULL,
  cache_read_per_mtok_usd  REAL NOT NULL,
  updated_at               DATETIME NOT NULL
);
