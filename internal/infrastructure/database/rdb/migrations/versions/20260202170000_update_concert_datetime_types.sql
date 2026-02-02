-- Rename 'date' column to 'local_event_date'
ALTER TABLE concerts RENAME COLUMN date TO local_event_date;

-- Convert start_time and open_time to TIMESTAMPTZ
-- We use local_event_date to construct a full timestamp for existing data.
ALTER TABLE concerts ALTER COLUMN start_time TYPE TIMESTAMPTZ USING (local_event_date + start_time)::TIMESTAMPTZ;
ALTER TABLE concerts ALTER COLUMN open_time TYPE TIMESTAMPTZ USING (local_event_date + open_time)::TIMESTAMPTZ;

-- Update comments
COMMENT ON COLUMN concerts.local_event_date IS 'Logical date of the concert event (local time)';
COMMENT ON COLUMN concerts.start_time IS 'Concert start time (timestamp with time zone)';
COMMENT ON COLUMN concerts.open_time IS 'Doors open time (timestamp with time zone), if available';
