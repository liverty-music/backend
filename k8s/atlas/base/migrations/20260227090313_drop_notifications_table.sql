-- Set comment to table: "events"
COMMENT ON TABLE "events" IS 'Generic event data including time, location, and metadata';
-- Set comment to column: "id" on table: "events"
COMMENT ON COLUMN "events"."id" IS 'Unique event identifier (UUIDv7)';
-- Set comment to column: "venue_id" on table: "events"
COMMENT ON COLUMN "events"."venue_id" IS 'Reference to the venue hosting the event';
-- Set comment to column: "title" on table: "events"
COMMENT ON COLUMN "events"."title" IS 'Event title as displayed to users';
-- Set comment to column: "local_event_date" on table: "events"
COMMENT ON COLUMN "events"."local_event_date" IS 'Date of the event';
-- Set comment to column: "start_at" on table: "events"
COMMENT ON COLUMN "events"."start_at" IS 'Event start time (absolute)';
-- Set comment to column: "open_at" on table: "events"
COMMENT ON COLUMN "events"."open_at" IS 'Doors open time (absolute), if available';
-- Set comment to column: "source_url" on table: "events"
COMMENT ON COLUMN "events"."source_url" IS 'URL where the event information was found';
-- Set comment to column: "listed_venue_name" on table: "events"
COMMENT ON COLUMN "events"."listed_venue_name" IS 'Raw venue name as scraped from the source, preserved separately from the normalized venue record';
-- Set comment to index: "idx_events_local_event_date" on table: "events"
COMMENT ON INDEX "idx_events_local_event_date" IS 'Speeds up date-based event searches and calendar views';
-- Set comment to index: "idx_events_venue_id" on table: "events"
COMMENT ON INDEX "idx_events_venue_id" IS 'Optimizes listing events by venue';
-- Set comment to index: "idx_followed_artists_artist_id" on table: "followed_artists"
COMMENT ON INDEX "idx_followed_artists_artist_id" IS 'Optimizes finding all followers of an artist';
-- Set comment to index: "idx_followed_artists_user_id" on table: "followed_artists"
COMMENT ON INDEX "idx_followed_artists_user_id" IS 'Optimizes retrieval of all followed artists for a user';
-- Create index "idx_users_email" to table: "users"
CREATE INDEX "idx_users_email" ON "users" ("email");
-- Set comment to table: "users"
COMMENT ON TABLE "users" IS 'User profiles and authentication data';
-- Set comment to column: "name" on table: "users"
COMMENT ON COLUMN "users"."name" IS 'User display name from identity provider';
-- Set comment to column: "email" on table: "users"
COMMENT ON COLUMN "users"."email" IS 'Primary contact and login identifier';
-- Set comment to column: "preferred_language" on table: "users"
COMMENT ON COLUMN "users"."preferred_language" IS 'User preferred language code (e.g., en, ja)';
-- Set comment to column: "country" on table: "users"
COMMENT ON COLUMN "users"."country" IS 'User country code (ISO 3166-1 alpha-2)';
-- Set comment to column: "time_zone" on table: "users"
COMMENT ON COLUMN "users"."time_zone" IS 'User time zone (IANA time zone database)';
-- Set comment to column: "is_active" on table: "users"
COMMENT ON COLUMN "users"."is_active" IS 'Whether the user account is active';
-- Set comment to index: "idx_users_external_id" on table: "users"
COMMENT ON INDEX "idx_users_external_id" IS 'Speeds up user lookup by Zitadel identity (sub claim)';
-- Set comment to index: "idx_users_email" on table: "users"
COMMENT ON INDEX "idx_users_email" IS 'Speeds up user lookup by email during authentication';
-- Set comment to column: "admin_area" on table: "venues"
COMMENT ON COLUMN "venues"."admin_area" IS 'Administrative area (prefecture, state, province) where the venue is located; NULL when not determinable with confidence';
-- Set comment to index: "idx_venues_google_place_id" on table: "venues"
COMMENT ON INDEX "idx_venues_google_place_id" IS 'Ensures uniqueness of Google Maps Place ID across venue records';
-- Set comment to index: "idx_venues_mbid" on table: "venues"
COMMENT ON INDEX "idx_venues_mbid" IS 'Ensures uniqueness of MusicBrainz Place ID across venue records';
-- Set comment to index: "idx_venues_raw_name" on table: "venues"
COMMENT ON INDEX "idx_venues_raw_name" IS 'Speeds up venue lookup by raw (pre-enrichment) name as fallback in GetByName';
-- Modify "concerts" table
ALTER TABLE "concerts" DROP CONSTRAINT "fk_concerts_events", ALTER COLUMN "event_id" DROP DEFAULT, ADD CONSTRAINT "concerts_event_id_fkey" FOREIGN KEY ("event_id") REFERENCES "events" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;
-- Set comment to table: "concerts"
COMMENT ON TABLE "concerts" IS 'Music-specific event details, linked 1:1 with events table';
-- Set comment to column: "event_id" on table: "concerts"
COMMENT ON COLUMN "concerts"."event_id" IS 'Reference to the generic event (PK/FK)';
-- Drop "notifications" table
DROP TABLE "notifications";
