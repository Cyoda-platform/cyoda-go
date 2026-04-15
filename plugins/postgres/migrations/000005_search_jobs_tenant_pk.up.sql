-- Change search_jobs primary key to (tenant_id, id) to allow the same job ID
-- to be used across different tenants. The conformance harness uses fixed job
-- IDs ("j1", "shared-id") across subtests that each run in fresh tenants, so
-- a plain `id` PK causes false duplicate-key conflicts.
--
-- search_job_results references search_jobs(id); we must re-create that FK too.

-- 1. Drop the FK on search_job_results so we can change the PK.
ALTER TABLE search_job_results DROP CONSTRAINT IF EXISTS search_job_results_job_id_fkey;

-- 2. Drop the existing single-column PK.
ALTER TABLE search_jobs DROP CONSTRAINT search_jobs_pkey;

-- 3. Add composite PK.
ALTER TABLE search_jobs ADD PRIMARY KEY (tenant_id, id);

-- 4. Re-add the FK from results to jobs on the composite key.
ALTER TABLE search_job_results
    ADD CONSTRAINT search_job_results_job_id_tenant_fkey
    FOREIGN KEY (job_id, tenant_id) REFERENCES search_jobs(id, tenant_id) ON DELETE CASCADE;
