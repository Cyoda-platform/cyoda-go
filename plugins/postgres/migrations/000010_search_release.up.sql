-- Graceful async-job handoff (spi.AsyncSearchStore.Release) and the
-- stale-claim attempt counter (SearchJob.StaleClaims). released is false
-- until Release marks it and is reset by the claim that takes the job;
-- stale_claims counts only staleness claims (never a claim of a released
-- job) and bounds the engine's attempt cap.
ALTER TABLE search_jobs ADD COLUMN released BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE search_jobs ADD COLUMN stale_claims BIGINT NOT NULL DEFAULT 0;
