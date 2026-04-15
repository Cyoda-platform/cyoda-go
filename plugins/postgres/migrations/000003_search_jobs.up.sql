CREATE TABLE search_jobs (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'RUNNING',
    model_name    TEXT NOT NULL,
    model_ver     TEXT NOT NULL,
    condition     JSONB NOT NULL,
    point_in_time TIMESTAMPTZ NOT NULL,
    search_opts   JSONB,
    result_count  INTEGER DEFAULT 0,
    error         TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at   TIMESTAMPTZ,
    calc_ms       BIGINT DEFAULT 0
);

CREATE INDEX idx_search_jobs_tenant ON search_jobs(tenant_id);
CREATE INDEX idx_search_jobs_created ON search_jobs(created_at);

ALTER TABLE search_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE search_jobs NO FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_search_jobs ON search_jobs
    USING (tenant_id = current_setting('app.current_tenant', true));

CREATE TABLE search_job_results (
    job_id    TEXT NOT NULL REFERENCES search_jobs(id) ON DELETE CASCADE,
    tenant_id TEXT NOT NULL,
    seq       INTEGER NOT NULL,
    entity_id TEXT NOT NULL,
    PRIMARY KEY (job_id, seq)
);

CREATE INDEX idx_search_job_results_tenant ON search_job_results(tenant_id, job_id);

ALTER TABLE search_job_results ENABLE ROW LEVEL SECURITY;
ALTER TABLE search_job_results NO FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_search_job_results ON search_job_results
    USING (tenant_id = current_setting('app.current_tenant', true));
