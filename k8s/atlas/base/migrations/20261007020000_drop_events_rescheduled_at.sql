-- Drop events.rescheduled_at (remove-event-postponement). Postponement was never
-- built: nothing ever stamped this column, so every row is NULL and no data is
-- lost. A postponed show is handled as a cancellation followed by a new event.

SET search_path TO app, public;

ALTER TABLE events DROP COLUMN IF EXISTS rescheduled_at;
