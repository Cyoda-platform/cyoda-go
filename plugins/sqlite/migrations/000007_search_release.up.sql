-- Graceful async-job handoff (spi.AsyncSearchStore.Release) and the
-- stale-claim attempt counter (SearchJob.StaleClaims). released is 0 until
-- Release marks it and is reset by the claim that takes the job; stale_claims
-- counts only staleness claims (never a claim of a released job) and bounds
-- the engine's attempt cap.
ALTER TABLE search_jobs ADD COLUMN released INTEGER NOT NULL DEFAULT 0;
ALTER TABLE search_jobs ADD COLUMN stale_claims INTEGER NOT NULL DEFAULT 0;
