-- Cyoda-Go SQLite Storage Backend — Initial Schema

-- entities: current state, one row per entity
CREATE TABLE entities (
    tenant_id     TEXT    NOT NULL,
    entity_id     TEXT    NOT NULL,
    model_name    TEXT    NOT NULL,
    model_version TEXT    NOT NULL,
    version       INTEGER NOT NULL,
    data          BLOB    NOT NULL,
    meta          BLOB,
    deleted       INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    PRIMARY KEY (tenant_id, entity_id)
) STRICT;

CREATE INDEX idx_entities_model
    ON entities (tenant_id, model_name, model_version)
    WHERE NOT deleted;

-- entity_versions: append-only version chain
CREATE TABLE entity_versions (
    tenant_id      TEXT    NOT NULL,
    entity_id      TEXT    NOT NULL,
    model_name     TEXT    NOT NULL,
    model_version  TEXT    NOT NULL,
    version        INTEGER NOT NULL,
    data           BLOB,
    meta           BLOB,
    change_type    TEXT    NOT NULL,
    transaction_id TEXT    NOT NULL,
    submit_time    INTEGER NOT NULL,
    user_id        TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (tenant_id, entity_id, version)
) STRICT, WITHOUT ROWID;

CREATE INDEX idx_entity_versions_submit_time
    ON entity_versions (tenant_id, entity_id, submit_time);

CREATE INDEX idx_entity_versions_model
    ON entity_versions (tenant_id, model_name, model_version);

-- models: model descriptors
CREATE TABLE models (
    tenant_id     TEXT NOT NULL,
    model_name    TEXT NOT NULL,
    model_version TEXT NOT NULL,
    doc           BLOB NOT NULL,
    PRIMARY KEY (tenant_id, model_name, model_version)
) STRICT, WITHOUT ROWID;

-- kv_store: key-value store (workflows, configs)
CREATE TABLE kv_store (
    tenant_id TEXT NOT NULL,
    namespace TEXT NOT NULL,
    key       TEXT NOT NULL,
    value     BLOB NOT NULL,
    PRIMARY KEY (tenant_id, namespace, key)
) STRICT, WITHOUT ROWID;

-- messages: edge messages
CREATE TABLE messages (
    tenant_id  TEXT NOT NULL,
    message_id TEXT NOT NULL,
    header     BLOB NOT NULL,
    metadata   BLOB NOT NULL,
    payload    BLOB NOT NULL,
    PRIMARY KEY (tenant_id, message_id)
) STRICT, WITHOUT ROWID;

-- sm_audit_events: state machine audit trail
CREATE TABLE sm_audit_events (
    tenant_id      TEXT    NOT NULL,
    entity_id      TEXT    NOT NULL,
    event_id       TEXT    NOT NULL,
    transaction_id TEXT,
    timestamp      INTEGER NOT NULL,
    doc            BLOB    NOT NULL,
    PRIMARY KEY (tenant_id, entity_id, event_id)
) STRICT, WITHOUT ROWID;

CREATE INDEX idx_sm_events_tx
    ON sm_audit_events (tenant_id, entity_id, transaction_id);

-- search_jobs: async search job metadata
CREATE TABLE search_jobs (
    tenant_id     TEXT    NOT NULL,
    job_id        TEXT    NOT NULL,
    status        TEXT    NOT NULL DEFAULT 'RUNNING',
    model_name    TEXT    NOT NULL,
    model_version TEXT    NOT NULL,
    condition     BLOB,
    point_in_time INTEGER,
    search_opts   BLOB,
    result_count  INTEGER NOT NULL DEFAULT 0,
    error         TEXT    NOT NULL DEFAULT '',
    create_time   INTEGER NOT NULL,
    finish_time   INTEGER,
    calc_time_ms  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, job_id)
) STRICT, WITHOUT ROWID;

-- search_job_results: entity IDs matched by a search job
CREATE TABLE search_job_results (
    tenant_id TEXT    NOT NULL,
    job_id    TEXT    NOT NULL,
    seq       INTEGER NOT NULL,
    entity_id TEXT    NOT NULL,
    PRIMARY KEY (tenant_id, job_id, seq)
) STRICT, WITHOUT ROWID;

-- submit_times: transaction submit timestamps (1h TTL, pruned at commit)
CREATE TABLE submit_times (
    tx_id       TEXT    NOT NULL PRIMARY KEY,
    submit_time INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE INDEX idx_submit_times_ttl ON submit_times (submit_time);

-- Readable views for CLI inspection of JSONB columns
CREATE VIEW entities_readable AS
SELECT tenant_id, entity_id, model_name, model_version, version,
       json(data) AS data, json(meta) AS meta,
       deleted, created_at, updated_at
FROM entities;

CREATE VIEW entity_versions_readable AS
SELECT tenant_id, entity_id, model_name, model_version, version,
       json(data) AS data, json(meta) AS meta,
       change_type, transaction_id, submit_time, user_id
FROM entity_versions;
