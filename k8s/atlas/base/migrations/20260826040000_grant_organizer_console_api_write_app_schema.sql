-- Grant INSERT/UPDATE/DELETE on the tables the organizer-concert authoring
-- path writes to the `organizer-console-api` Cloud SQL IAM service-account
-- user. The previous migrations (20260825000000 / 20260825120000) granted
-- only SELECT; authoring requires write access to a scoped subset of tables.
--
-- Tables granted write access:
--   series               — CreateDraft/UpdateDraft/Publish state transitions
--   events               — materialized on Publish (PublishDraft)
--   event_performers     — performer links materialized on Publish
--   venues               — get-or-create during draft venue resolution
--   draft_events         — temporary draft rows (create/update/delete)
--   draft_series_performers — temporary draft performer rows
--   staged_concerts      — dropped on Publish (supersede)
--   suppressed_concerts  — checked on Publish (read only needed, kept for completeness)
--
-- Pattern mirrors 20260825120000: loop over pg_roles to be idempotent when
-- the IAM user has not yet been created in a given environment.
DO $$
DECLARE
  iam_role TEXT;
BEGIN
  FOR iam_role IN
    SELECT rolname FROM pg_roles WHERE rolname LIKE 'organizer-console-api@%.iam'
  LOOP
    EXECUTE format('GRANT INSERT, UPDATE, DELETE ON app.series TO %I', iam_role);
    EXECUTE format('GRANT INSERT, UPDATE, DELETE ON app.events TO %I', iam_role);
    EXECUTE format('GRANT INSERT, UPDATE, DELETE ON app.event_performers TO %I', iam_role);
    EXECUTE format('GRANT INSERT, UPDATE ON app.venues TO %I', iam_role);
    EXECUTE format('GRANT INSERT, UPDATE, DELETE ON app.draft_events TO %I', iam_role);
    EXECUTE format('GRANT INSERT, UPDATE, DELETE ON app.draft_series_performers TO %I', iam_role);
    EXECUTE format('GRANT INSERT, UPDATE, DELETE ON app.staged_concerts TO %I', iam_role);
    EXECUTE format('GRANT INSERT, UPDATE, DELETE ON app.concerts TO %I', iam_role);
  END LOOP;
END
$$;
