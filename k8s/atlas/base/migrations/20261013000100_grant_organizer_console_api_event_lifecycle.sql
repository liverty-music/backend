-- Grant the `organizer-console-api` Cloud SQL IAM service-account user the
-- writes the series event lifecycle needs once drafts are events in the DRAFT
-- state (restructure-event-publishing, design D5). The role is read-only by
-- default (20261008020000); grants stay as narrow as the paths:
--
--   events          — Update changes a DRAFT event's venue, listed venue name,
--                     date and times and a PUBLISHED event's times; Publish
--                     sets publish_state and, when it claims an event, its
--                     series_id; Cancel sets publish_state. Update and Cancel
--                     remove DRAFT events, Publish removes the draft of a held
--                     slot, and Update removes a discovered event it merges
--                     (DELETE). The column privileges also cover the event
--                     row locks (SELECT ... FOR UPDATE).
--   concert_artists — Update replaces the performers of a concert (DELETE).
--   ticket_journeys — Update moves the fans of a merged discovered event onto
--                     the published event (INSERT).
--   series          — Cancel removes a series left with no event (DELETE).
--
-- 20261013000000 dropped series.publish_state, published_at and cancelled_at,
-- and the column privileges on them went with the columns.
--
-- Pattern mirrors 20261010010000: loop over pg_roles so the migration is
-- idempotent and skips the role where it does not exist yet.
SET search_path TO app, public;

DO $$
DECLARE
  iam_role TEXT;
BEGIN
  FOR iam_role IN
    SELECT rolname FROM pg_roles WHERE rolname LIKE 'organizer-console-api@%.iam'
  LOOP
    EXECUTE format('GRANT UPDATE (publish_state, venue_id, listed_venue_name, local_event_date, start_at, open_at) ON app.events TO %I', iam_role);
    EXECUTE format('GRANT DELETE ON app.events TO %I', iam_role);
    EXECUTE format('GRANT DELETE ON app.concert_artists TO %I', iam_role);
    EXECUTE format('GRANT INSERT ON app.ticket_journeys TO %I', iam_role);
    EXECUTE format('GRANT DELETE ON app.series TO %I', iam_role);
  END LOOP;
END
$$;
