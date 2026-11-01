-- publish-events-through-outbox: the organizer server and the reception
-- server announce (SeriesImage.Create, Series.PublishEvents,
-- Organizer.AssociateArtist; Ticket.Admit), so they get INSERT on the outbox
-- and nothing else on it. The offset is an identity column, so no sequence
-- privilege is needed.

SET search_path TO app, public;

DO $$
DECLARE
  r TEXT;
BEGIN
  FOR r IN SELECT rolname FROM pg_roles WHERE rolname LIKE 'organizer-console-api@%.iam' OR rolname LIKE 'reception-api@%.iam'
  LOOP
    EXECUTE format('GRANT INSERT ON watermill_events_outbox TO %I', r);
  END LOOP;
END
$$;
