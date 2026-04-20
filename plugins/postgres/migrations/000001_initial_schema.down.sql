-- Reverse of the consolidated initial schema. Drops in reverse dependency
-- order; CASCADE handles the FK from search_job_results → search_jobs and
-- any RLS policy left behind.

DROP INDEX IF EXISTS model_schema_extensions_lookup;
DROP TABLE IF EXISTS model_schema_extensions CASCADE;

DROP TABLE IF EXISTS search_job_results CASCADE;
DROP TABLE IF EXISTS search_jobs CASCADE;
DROP TABLE IF EXISTS messages CASCADE;
DROP TABLE IF EXISTS kv_store CASCADE;
DROP TABLE IF EXISTS models CASCADE;
DROP TABLE IF EXISTS sm_audit_events CASCADE;
DROP TABLE IF EXISTS entity_versions CASCADE;
DROP TABLE IF EXISTS entities CASCADE;
