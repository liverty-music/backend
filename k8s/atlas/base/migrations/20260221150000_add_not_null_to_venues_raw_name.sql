-- Make raw_name NOT NULL. All existing rows already have a value from the
-- backfill in 20260221000000. New rows always set raw_name in Create().
ALTER TABLE venues ALTER COLUMN raw_name SET NOT NULL;
