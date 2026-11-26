-- Migration D of OpenSpec change globalize-venues (design Decisions 12 and 16
-- step 5): a time that people announce as a wall-clock time at a place is
-- stored as that wall-clock time. An Event's start and open times become times
-- of day read in its Venue's time zone; a TicketSale gets a time zone and its
-- window becomes local date-times in it; a candidate's and a rejected
-- concert's times become times of day. Each is filled from the old instant in
-- its zone and cut to the minute. The old columns stay, no longer written,
-- until the migration that follows release 2 drops them.

-- Add the new columns, empty.
ALTER TABLE events
  ADD COLUMN local_start_time time NULL,
  ADD COLUMN local_open_time time NULL;
ALTER TABLE concert_candidates
  ADD COLUMN local_start_time time NULL,
  ADD COLUMN local_open_time time NULL;
ALTER TABLE rejected_concerts
  ADD COLUMN local_start_time time NULL,
  ADD COLUMN local_open_time time NULL;
DROP INDEX uq_ticket_sales_discovered_convergence;
ALTER TABLE ticket_sales
  DROP CONSTRAINT chk_ticket_sales_result_date,
  DROP CONSTRAINT chk_ticket_sales_window,
  ALTER COLUMN sale_start_time DROP NOT NULL,
  ALTER COLUMN sale_end_time DROP NOT NULL,
  DROP COLUMN sale_start_date_jst,
  ADD COLUMN time_zone text NULL,
  ADD COLUMN local_start_time timestamp NULL,
  ADD COLUMN local_end_time timestamp NULL;

-- An Event is read in its Venue's time zone.
UPDATE events AS e SET
  local_start_time = date_trunc('minute', e.start_time AT TIME ZONE v.time_zone)::time,
  local_open_time = date_trunc('minute', e.open_time AT TIME ZONE v.time_zone)::time
FROM venues AS v
WHERE v.id = e.venue_id;

-- A candidate and a rejected concert are read in their own zone, Japan time
-- where they have none (every instant stored before this was read in it).
UPDATE concert_candidates AS c SET
  local_start_time = date_trunc('minute', c.start_time AT TIME ZONE COALESCE((SELECT z.name FROM time_zones AS z WHERE z.name = c.time_zone), 'Asia/Tokyo'))::time,
  local_open_time = date_trunc('minute', c.open_time AT TIME ZONE COALESCE((SELECT z.name FROM time_zones AS z WHERE z.name = c.time_zone), 'Asia/Tokyo'))::time;
UPDATE rejected_concerts AS c SET
  local_start_time = date_trunc('minute', c.start_time AT TIME ZONE COALESCE((SELECT z.name FROM time_zones AS z WHERE z.name = c.time_zone), 'Asia/Tokyo'))::time,
  local_open_time = date_trunc('minute', c.open_time AT TIME ZONE COALESCE((SELECT z.name FROM time_zones AS z WHERE z.name = c.time_zone), 'Asia/Tokyo'))::time;

-- A sale's zone is the zone its events' Venues share (the Events its series
-- has, for a discovered sale), Japan time when they differ or there are none.
UPDATE ticket_sales AS ts SET time_zone = COALESCE((
  SELECT min(v.time_zone)
  FROM events AS e JOIN venues AS v ON v.id = e.venue_id
  WHERE e.series_id = ts.series_id
    AND (ts.find_time IS NOT NULL OR e.id IN (SELECT x.event_id FROM ticket_sale_events AS x WHERE x.ticket_sale_id = ts.id))
  HAVING count(DISTINCT v.time_zone) = 1
), 'Asia/Tokyo');
UPDATE ticket_sales SET
  local_start_time = date_trunc('minute', sale_start_time AT TIME ZONE time_zone),
  local_end_time = date_trunc('minute', sale_end_time AT TIME ZONE time_zone);

-- A result date earlier than the day its sale ends in its new zone can no
-- longer be kept, as when a later end time arrives.
UPDATE ticket_sales SET result_date = NULL WHERE result_date < local_end_time::date;

-- Require and constrain what is filled.
ALTER TABLE ticket_sales
  ALTER COLUMN time_zone SET NOT NULL,
  ALTER COLUMN local_start_time SET NOT NULL,
  ALTER COLUMN local_end_time SET NOT NULL,
  ADD COLUMN sale_start_local_date date NULL GENERATED ALWAYS AS ((local_start_time)::date) STORED,
  ADD CONSTRAINT chk_ticket_sales_window CHECK (local_end_time > local_start_time),
  ADD CONSTRAINT chk_ticket_sales_whole_minutes CHECK ((EXTRACT(second FROM local_start_time) = (0)::numeric) AND (EXTRACT(second FROM local_end_time) = (0)::numeric)),
  ADD CONSTRAINT chk_ticket_sales_result_date CHECK ((result_date IS NULL) OR ((find_time IS NOT NULL) AND (method = 2) AND (result_date >= (local_end_time)::date))),
  ADD CONSTRAINT fk_ticket_sales_time_zone FOREIGN KEY (time_zone) REFERENCES time_zones (name);
CREATE UNIQUE INDEX uq_ticket_sales_discovered_convergence ON ticket_sales (series_id, method, sale_start_local_date) WHERE (find_time IS NOT NULL);

-- An Event's identity is its Venue, date and announced start.
DROP INDEX uq_events_natural_key;
ALTER TABLE events ADD CONSTRAINT chk_events_local_times_whole_minutes CHECK ((EXTRACT(second FROM local_start_time) = (0)::numeric) AND (EXTRACT(second FROM local_open_time) = (0)::numeric));
CREATE UNIQUE INDEX uq_events_natural_key ON events (venue_id, local_event_date, local_start_time) NULLS NOT DISTINCT WHERE (publish_state IS DISTINCT FROM 'DRAFT'::event_publish_state);
COMMENT ON INDEX uq_events_natural_key IS 'Physical identity of a performance: one row per (venue, local date, local start time) among events that are not DRAFT, independent of series or performing artist. local_start_time is part of the key so two shows at one venue on one date with different start times (matinee/evening) are distinct; NULLS NOT DISTINCT collapses two shows whose start time is not yet published. The same physical show discovered via different artists/series resolves to one row. DRAFT events are excluded, so any number of drafts and one live or cancelled event can share a slot. Upserts infer it with ON CONFLICT (venue_id, local_event_date, local_start_time) WHERE publish_state IS DISTINCT FROM ''DRAFT''.';

COMMENT ON COLUMN "concert_candidates"."start_time" IS 'Replaced by local_start_time and no longer written; dropped by the migration that follows release 2';

COMMENT ON COLUMN "concert_candidates"."open_time" IS 'Replaced by local_open_time and no longer written; dropped by the migration that follows release 2';

COMMENT ON COLUMN "concert_candidates"."local_start_time" IS 'Wall-clock start time at the venue, in whole minutes, as the source states it; read in time_zone. NULL when the source did not state one.';

COMMENT ON COLUMN "concert_candidates"."local_open_time" IS 'Wall-clock doors-open time at the venue, read like local_start_time. NULL when not announced.';

COMMENT ON COLUMN "events"."start_time" IS 'Replaced by local_start_time and no longer written; dropped by the migration that follows release 2';

COMMENT ON COLUMN "events"."open_time" IS 'Replaced by local_open_time and no longer written; dropped by the migration that follows release 2';

COMMENT ON COLUMN "events"."local_start_time" IS 'Wall-clock time the event starts at its venue, a time of day in whole minutes on local_event_date, read in the venue''s time_zone; NULL when not announced. The instant is computed by the application from the date, this time and the zone';

COMMENT ON COLUMN "events"."local_open_time" IS 'Wall-clock time the doors open at the venue, read like local_start_time; NULL when not announced';

COMMENT ON COLUMN "rejected_concerts"."start_time" IS 'Replaced by local_start_time and no longer written; dropped by the migration that follows release 2';

COMMENT ON COLUMN "rejected_concerts"."open_time" IS 'Replaced by local_open_time and no longer written; dropped by the migration that follows release 2';

COMMENT ON COLUMN "rejected_concerts"."local_start_time" IS 'Wall-clock start time of the rejected concert at its venue, in whole minutes, read in time_zone. NULL when unknown.';

COMMENT ON COLUMN "rejected_concerts"."local_open_time" IS 'Wall-clock doors-open time of the rejected concert, read like local_start_time. NULL when unknown.';

COMMENT ON COLUMN "ticket_sales"."sale_start_time" IS 'Replaced by local_start_time and time_zone and no longer written; dropped by the migration that follows release 2';

COMMENT ON COLUMN "ticket_sales"."sale_end_time" IS 'Replaced by local_end_time and time_zone and no longer written; dropped by the migration that follows release 2';

COMMENT ON COLUMN "ticket_sales"."result_date" IS 'Calendar day (a date without a zone) on which a discovered lottery''s results are announced, as its pages state it; no earlier than the local date of local_end_time. NULL when the pages do not state it, and always NULL on a first-come sale and on an Organizer''s sale. Anchors the RESULT_DAY reminder.';

COMMENT ON COLUMN "ticket_sales"."time_zone" IS 'IANA time zone the sale''s window is read in: on an Organizer''s sale one of the time zones of its events'' venues, on a discovered sale the zone its page states or its series'' venues share';

COMMENT ON COLUMN "ticket_sales"."local_start_time" IS 'Wall-clock time the sale opens, in whole minutes, read in time_zone; the instant is computed by the application';

COMMENT ON COLUMN "ticket_sales"."local_end_time" IS 'Wall-clock time the sale closes, read in time_zone; after local_start_time. On an Organizer''s sale 1 to 14 days after the start for a lottery, and no later than the start of every event the sale covers (checked by the application)';

COMMENT ON COLUMN "ticket_sales"."sale_start_local_date" IS 'Calendar date of local_start_time in the sale''s time zone; part of the discovered-sale convergence key';

COMMENT ON INDEX "uq_ticket_sales_discovered_convergence" IS 'One discovered sale per (series, method, start date in the sale''s time zone): re-discovery updates it in place. Partial, so an Organizer may open two sales of one Series on the same day for different events. Upserts infer it with ON CONFLICT (series_id, method, sale_start_local_date) WHERE find_time IS NOT NULL.';

-- Column and table privileges the restricted roles need for the new reads and
-- writes: the Organizer console writes the new columns, and reception reads an
-- Event's Venue time zone. Each loop skips a role that does not exist here.
DO $$
DECLARE
  iam_role TEXT;
BEGIN
  FOR iam_role IN
    SELECT rolname FROM pg_roles WHERE rolname LIKE 'organizer-console-api@%.iam'
  LOOP
    EXECUTE format('GRANT UPDATE (local_start_time, local_open_time) ON events TO %I', iam_role);
    EXECUTE format('GRANT UPDATE (time_zone, local_start_time, local_end_time) ON ticket_sales TO %I', iam_role);
  END LOOP;

  FOR iam_role IN
    SELECT rolname FROM pg_roles WHERE rolname LIKE 'reception-api@%.iam'
  LOOP
    EXECUTE format('GRANT SELECT ON venues TO %I', iam_role);
  END LOOP;
END
$$;
