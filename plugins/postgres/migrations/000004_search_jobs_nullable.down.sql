-- Reverse: restore NOT NULL constraints on condition and point_in_time.
-- Note: this will fail if any rows have NULL values in those columns.
ALTER TABLE search_jobs ALTER COLUMN condition SET NOT NULL;
ALTER TABLE search_jobs ALTER COLUMN point_in_time SET NOT NULL;
