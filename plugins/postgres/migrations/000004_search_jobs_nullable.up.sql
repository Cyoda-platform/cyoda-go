-- Allow condition and point_in_time to be NULL in search_jobs.
-- These fields are optional in spi.SearchJob (json.RawMessage / time.Time) and
-- the SPI conformance harness creates jobs without setting them.
ALTER TABLE search_jobs ALTER COLUMN condition DROP NOT NULL;
ALTER TABLE search_jobs ALTER COLUMN point_in_time DROP NOT NULL;
