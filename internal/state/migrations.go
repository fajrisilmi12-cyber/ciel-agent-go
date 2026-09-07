package state

// advancedSchema contains migration SQL for OpenClaw-inspired patterns.
// Applied idempotently via IF NOT EXISTS.
const advancedSchema = `
-- 1. Memory Provenance: origin tracking + taint flags
CREATE TABLE IF NOT EXISTS memory_provenance (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    target        TEXT    NOT NULL,
    origin_class  TEXT    NOT NULL DEFAULT 'system',
    taint_flags   TEXT    DEFAULT '',
    content_hash  TEXT    NOT NULL,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_mp_target ON memory_provenance(target);

-- 2. Exec Approval Binding: hash-based command approval
CREATE TABLE IF NOT EXISTS exec_approvals (
    id              TEXT    PRIMARY KEY,
    command_hash    TEXT    NOT NULL UNIQUE,
    command_preview TEXT    NOT NULL,
    cwd             TEXT    NOT NULL DEFAULT '.',
    env_hash        TEXT    NOT NULL DEFAULT '',
    file_operands   TEXT    DEFAULT '[]',
    approved_by     TEXT    NOT NULL DEFAULT 'user',
    approved_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at      DATETIME,
    hit_count       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_ea_hash ON exec_approvals(command_hash);

-- 3. Session Recovery: checkpoints for interrupted turns
CREATE TABLE IF NOT EXISTS interrupted_turns (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id         TEXT    NOT NULL,
    iteration          INTEGER NOT NULL,
    messages_snapshot  TEXT    NOT NULL,
    tool_state         TEXT    DEFAULT '{}',
    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_it_session ON interrupted_turns(session_id);

-- 4. Cron/Automation: scheduled tasks
CREATE TABLE IF NOT EXISTS cron_jobs (
    id           TEXT    PRIMARY KEY,
    name         TEXT    NOT NULL,
    schedule     TEXT    NOT NULL,
    prompt       TEXT    NOT NULL,
    tool_policy  TEXT    DEFAULT '*',
    enabled      INTEGER NOT NULL DEFAULT 1,
    next_run     DATETIME NOT NULL,
    last_run     DATETIME,
    last_result  TEXT,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 5. Secret Management: SecretRef registry
CREATE TABLE IF NOT EXISTS secrets (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    key          TEXT    NOT NULL UNIQUE,
    kind         TEXT    NOT NULL DEFAULT 'env',
    value_source TEXT    NOT NULL,
    description  TEXT    DEFAULT '',
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_sk_key ON secrets(key);
`
