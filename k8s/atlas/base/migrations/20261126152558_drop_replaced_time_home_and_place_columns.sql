-- Migration E of OpenSpec change globalize-venues (design Decisions 8, 9 and
-- 12, Migration Plan step 9): drop what releases 1 and 2 replaced. An Event's,
-- a candidate's, a rejected concert's and a TicketSale's times are read from
-- the local_* columns (and, for a sale, time_zone); a user's home is
-- user_home_areas; a candidate and a rejected concert keep only the resolved
-- place id. No code of release 2 or later reads or writes anything dropped
-- here, so the pods still running when this applies are not affected. The
-- dropped values are not restored by rolling an image back.

-- Modify "concert_candidates" table
ALTER TABLE "concert_candidates" DROP COLUMN "start_time", DROP COLUMN "open_time", DROP COLUMN "resolved_venue_name", DROP COLUMN "resolved_admin_area", DROP COLUMN "resolved_latitude", DROP COLUMN "resolved_longitude";
-- Modify "events" table
ALTER TABLE "events" DROP COLUMN "start_time", DROP COLUMN "open_time";
-- Modify "rejected_concerts" table
ALTER TABLE "rejected_concerts" DROP COLUMN "start_time", DROP COLUMN "open_time", DROP COLUMN "resolved_venue_name", DROP COLUMN "resolved_admin_area";
-- Set comment to table: "settlements"
COMMENT ON TABLE "settlements" IS 'Payout record for one Order. Holds the captured platform charge until the release gate passes (the event''s start + dispute buffer), then tracks the Transfer(s) that pay out the Organizer net share. Platform fee = un-transferred remainder (no fee split row). status: 1=Held, 2=Released, 3=Reversed.';
-- Set comment to column: "event_id" on table: "settlements"
COMMENT ON COLUMN "settlements"."event_id" IS 'The event this settlement gates on. Stored denormalized so the release-gate check finds the event''s start without extra joins.';
-- Modify "ticket_sales" table
ALTER TABLE "ticket_sales" DROP COLUMN "sale_start_time", DROP COLUMN "sale_end_time";
-- Modify "users" table
ALTER TABLE "users" DROP COLUMN "home_id";
-- Drop "homes" table
DROP TABLE "homes";
