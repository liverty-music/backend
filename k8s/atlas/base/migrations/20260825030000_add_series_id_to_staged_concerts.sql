-- Add a real-FK series_id to staged_concerts.
--
-- Under the series-grouped discovery model the parent series row is created at
-- discovery time (when the group series_id is resolved), so a staged event
-- carries its series_id as a real foreign key; approval inserts the event under
-- that existing series without minting a new one.
--
-- staged_concerts is a transient approval queue (approve/reject both delete the
-- row). Existing pending rows predate this column and cannot be assigned a valid
-- parent series in SQL — series ids are application-generated UUIDv7, which the
-- chk_series_id_uuidv7 constraint enforces and gen_random_uuid() (v4) would
-- violate. They are re-discovered and re-staged (now series-grouped) on the next
-- discovery run, so clear the queue before adding the NOT NULL foreign key.
DELETE FROM staged_concerts;

ALTER TABLE staged_concerts
    ADD COLUMN series_id UUID NOT NULL REFERENCES series(id) ON DELETE CASCADE;

COMMENT ON COLUMN staged_concerts.series_id IS 'Parent series this staged event belongs to. The series row is created at discovery time (when the group series_id is resolved), so this is a real foreign key by staging time; approval inserts the event under it without minting a new series. type/title/source_url live on the series row.';
