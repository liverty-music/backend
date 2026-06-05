-- Rework event identity onto a physical natural key and drop the series UUIDv5 carve-out.
--
-- Events now deduplicate on (venue_id, local_event_date, start_at) NULLS NOT DISTINCT
-- — physical identity, independent of series or performing artist — instead of
-- (series_id, local_event_date, venue_id). start_at joins the key so two shows at
-- one venue on one date with different start times (matinee/evening) stay distinct.
-- series.id becomes a pure UUIDv7; its cross-run identity is adopted from member
-- events at the application layer rather than from a deterministic content key.

-- 1. Collapse existing events that would violate the new physical key.
--    Keep the lowest id per (venue_id, local_event_date, start_at); NULLS NOT
--    DISTINCT means rows sharing venue+date with an unpublished start_at also
--    collapse. Repoint event_performers to the survivor first, then delete the
--    losers (concerts / event_sales_phases / ticket rows cascade via ON DELETE CASCADE).
WITH ranked AS (
  SELECT id,
         first_value(id) OVER (
           PARTITION BY venue_id, local_event_date, start_at
           ORDER BY id
         ) AS keep_id
  FROM events
), losers AS (
  SELECT id, keep_id FROM ranked WHERE id <> keep_id
)
INSERT INTO event_performers (event_id, artist_id)
SELECT l.keep_id, ep.artist_id
FROM event_performers ep
JOIN losers l ON ep.event_id = l.id
ON CONFLICT DO NOTHING;

WITH ranked AS (
  SELECT id,
         first_value(id) OVER (
           PARTITION BY venue_id, local_event_date, start_at
           ORDER BY id
         ) AS keep_id
  FROM events
)
DELETE FROM events e
USING ranked r
WHERE e.id = r.id AND r.id <> r.keep_id;

-- 2. Swap the events natural key to physical identity.
ALTER TABLE "events" DROP CONSTRAINT "uq_events_natural_key";
ALTER TABLE "events" ADD CONSTRAINT "uq_events_natural_key" UNIQUE NULLS NOT DISTINCT ("venue_id", "local_event_date", "start_at");

-- 3. Backfill any UUIDv5 series ids to UUIDv7, cascading FK referencers, so the
--    pure-v7 CHECK validates. No-op when no v5 rows exist (the common case
--    pre-launch). gen_random_uuid() yields a v4; force the version nibble to 7
--    (the CHECK only inspects that nibble) to avoid a PostgreSQL 18 uuidv7() dependency.
DO $$
DECLARE
  old_id uuid;
  new_id uuid;
BEGIN
  FOR old_id IN SELECT id FROM series WHERE substring(id::text, 15, 1) = '5' LOOP
    new_id := overlay(gen_random_uuid()::text placing '7' from 15 for 1)::uuid;
    INSERT INTO series (id, title, type, source_url, merch_url)
      SELECT new_id, title, type, source_url, merch_url FROM series WHERE id = old_id;
    UPDATE events SET series_id = new_id WHERE series_id = old_id;
    UPDATE sales_phases SET series_id = new_id WHERE series_id = old_id;
    DELETE FROM series WHERE id = old_id;
  END LOOP;
END $$;

-- 4. Drop the v5-or-v7 carve-out; require pure UUIDv7 (validates immediately
--    because step 3 left only v7 ids).
ALTER TABLE "series" DROP CONSTRAINT "chk_series_id_uuid_v5_or_v7";
ALTER TABLE "series" ADD CONSTRAINT "chk_series_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7');
