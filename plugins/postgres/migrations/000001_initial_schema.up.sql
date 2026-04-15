-- Cyoda-Go PostgreSQL Storage Backend — Initial Schema
-- See: docs/plans/2026-03-27-postgresql-storage-design.md

-- entities: current state, one row per entity
CREATE TABLE IF NOT EXISTS entities (
    tenant_id       TEXT        NOT NULL,
    entity_id       TEXT        NOT NULL,
    model_name      TEXT        NOT NULL,
    model_version   TEXT        NOT NULL,
    version         BIGINT      NOT NULL,
    deleted         BOOLEAN     NOT NULL DEFAULT false,
    doc             JSONB       NOT NULL,
    PRIMARY KEY (tenant_id, entity_id)
);

CREATE INDEX IF NOT EXISTS idx_entities_model
    ON entities (tenant_id, model_name, model_version)
    WHERE NOT deleted;

-- entity_versions: append-only bi-temporal history
CREATE TABLE IF NOT EXISTS entity_versions (
    tenant_id        TEXT        NOT NULL,
    entity_id        TEXT        NOT NULL,
    model_name       TEXT        NOT NULL,
    model_version    TEXT        NOT NULL,
    version          BIGINT      NOT NULL,
    valid_time       TIMESTAMPTZ NOT NULL,
    transaction_time TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    wall_clock_time  TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    doc              JSONB       NOT NULL,
    PRIMARY KEY (tenant_id, entity_id, version)
);

CREATE INDEX IF NOT EXISTS idx_ev_bitemporal
    ON entity_versions (tenant_id, entity_id, valid_time DESC, transaction_time DESC);

CREATE INDEX IF NOT EXISTS idx_ev_model
    ON entity_versions (tenant_id, model_name, model_version);

-- sm_audit_events: StateMachine audit trail
CREATE TABLE IF NOT EXISTS sm_audit_events (
    tenant_id       TEXT        NOT NULL,
    entity_id       TEXT        NOT NULL,
    event_id        TEXT        NOT NULL,
    transaction_id  TEXT,
    timestamp       TIMESTAMPTZ NOT NULL,
    doc             JSONB       NOT NULL,
    PRIMARY KEY (tenant_id, entity_id, event_id)
);

CREATE INDEX IF NOT EXISTS idx_sm_events_tx
    ON sm_audit_events (tenant_id, entity_id, transaction_id);

-- models: model descriptors
CREATE TABLE IF NOT EXISTS models (
    tenant_id       TEXT        NOT NULL,
    model_name      TEXT        NOT NULL,
    model_version   TEXT        NOT NULL,
    doc             JSONB       NOT NULL,
    PRIMARY KEY (tenant_id, model_name, model_version)
);

-- kv_store: key-value store (workflows, configs)
CREATE TABLE IF NOT EXISTS kv_store (
    tenant_id       TEXT        NOT NULL,
    namespace       TEXT        NOT NULL,
    key             TEXT        NOT NULL,
    value           BYTEA       NOT NULL,
    PRIMARY KEY (tenant_id, namespace, key)
);

-- messages: edge messages with binary payload
CREATE TABLE IF NOT EXISTS messages (
    tenant_id       TEXT        NOT NULL,
    message_id      TEXT        NOT NULL,
    header          JSONB       NOT NULL,
    metadata        JSONB       NOT NULL,
    payload         BYTEA       NOT NULL,
    PRIMARY KEY (tenant_id, message_id)
);

-- Row-Level Security (defense-in-depth)
ALTER TABLE entities ENABLE ROW LEVEL SECURITY;
ALTER TABLE entities FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_entities ON entities
    USING (tenant_id = current_setting('app.current_tenant', true));

ALTER TABLE entity_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE entity_versions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_entity_versions ON entity_versions
    USING (tenant_id = current_setting('app.current_tenant', true));

ALTER TABLE sm_audit_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE sm_audit_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_sm_audit ON sm_audit_events
    USING (tenant_id = current_setting('app.current_tenant', true));

ALTER TABLE models ENABLE ROW LEVEL SECURITY;
ALTER TABLE models FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_models ON models
    USING (tenant_id = current_setting('app.current_tenant', true));

ALTER TABLE kv_store ENABLE ROW LEVEL SECURITY;
ALTER TABLE kv_store FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_kv ON kv_store
    USING (tenant_id = current_setting('app.current_tenant', true));

ALTER TABLE messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE messages FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_messages ON messages
    USING (tenant_id = current_setting('app.current_tenant', true));
