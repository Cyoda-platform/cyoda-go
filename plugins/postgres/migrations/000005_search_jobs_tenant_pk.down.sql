-- Reverse migration 000005: restore single-column PK on search_jobs.
ALTER TABLE search_job_results DROP CONSTRAINT IF EXISTS search_job_results_job_id_tenant_fkey;
ALTER TABLE search_jobs DROP CONSTRAINT IF EXISTS search_jobs_pkey;
ALTER TABLE search_jobs ADD PRIMARY KEY (id);
ALTER TABLE search_job_results
    ADD CONSTRAINT search_job_results_job_id_fkey
    FOREIGN KEY (job_id) REFERENCES search_jobs(id) ON DELETE CASCADE;
