-- Delete orphaned artists with NULL MBID (dead data from no-MBID INSERT path)
-- Cascade through dependent tables first to avoid FK violations
DELETE FROM "concerts" WHERE "artist_id" IN (SELECT "id" FROM "artists" WHERE "mbid" IS NULL);
DELETE FROM "followed_artists" WHERE "artist_id" IN (SELECT "id" FROM "artists" WHERE "mbid" IS NULL);
DELETE FROM "latest_search_logs" WHERE "artist_id" IN (SELECT "id" FROM "artists" WHERE "mbid" IS NULL);
DELETE FROM "artist_official_site" WHERE "artist_id" IN (SELECT "id" FROM "artists" WHERE "mbid" IS NULL);
DELETE FROM "artists" WHERE "mbid" IS NULL;
-- Modify "artist_official_site" table
ALTER TABLE "artist_official_site" ADD CONSTRAINT "chk_artist_official_site_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7'::text), ALTER COLUMN "id" DROP DEFAULT;
-- Set comment to column: "id" on table: "artist_official_site"
COMMENT ON COLUMN "artist_official_site"."id" IS 'Unique identifier (UUIDv7, application-generated)';
-- Drop index "idx_artists_mbid" from table: "artists"
DROP INDEX "idx_artists_mbid";
-- Modify "artists" table
ALTER TABLE "artists" DROP CONSTRAINT "chk_artists_mbid_format", ADD CONSTRAINT "chk_artists_mbid_format" CHECK (char_length(mbid) = 36), ADD CONSTRAINT "chk_artists_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7'::text), ALTER COLUMN "id" DROP DEFAULT, ALTER COLUMN "mbid" SET NOT NULL;
-- Create index "idx_artists_mbid" to table: "artists"
CREATE UNIQUE INDEX "idx_artists_mbid" ON "artists" ("mbid");
-- Set comment to column: "id" on table: "artists"
COMMENT ON COLUMN "artists"."id" IS 'Unique artist identifier (UUIDv7, application-generated)';
-- Modify "events" table
ALTER TABLE "events" ADD CONSTRAINT "chk_events_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7'::text), ALTER COLUMN "id" DROP DEFAULT;
-- Set comment to column: "id" on table: "events"
COMMENT ON COLUMN "events"."id" IS 'Unique event identifier (UUIDv7, application-generated)';
-- Modify "homes" table
ALTER TABLE "homes" ADD CONSTRAINT "chk_homes_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7'::text), ALTER COLUMN "id" DROP DEFAULT;
-- Set comment to column: "id" on table: "homes"
COMMENT ON COLUMN "homes"."id" IS 'Unique home record identifier (UUIDv7, application-generated)';
-- Drop index "idx_nullifiers_event_hash" from table: "nullifiers"
DROP INDEX "idx_nullifiers_event_hash";
-- Modify "nullifiers" table
ALTER TABLE "nullifiers" DROP CONSTRAINT "nullifiers_pkey", DROP COLUMN "id", ADD PRIMARY KEY ("event_id", "nullifier_hash");
-- Modify "push_subscriptions" table
ALTER TABLE "push_subscriptions" ADD CONSTRAINT "chk_push_subscriptions_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7'::text), ALTER COLUMN "id" DROP DEFAULT;
-- Set comment to column: "id" on table: "push_subscriptions"
COMMENT ON COLUMN "push_subscriptions"."id" IS 'Unique identifier (UUIDv7, application-generated)';
-- Modify "tickets" table
ALTER TABLE "tickets" ADD CONSTRAINT "chk_tickets_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7'::text), ALTER COLUMN "id" DROP DEFAULT;
-- Set comment to column: "id" on table: "tickets"
COMMENT ON COLUMN "tickets"."id" IS 'Unique ticket identifier (UUIDv7, application-generated)';
-- Modify "users" table
ALTER TABLE "users" ADD CONSTRAINT "chk_users_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7'::text), ALTER COLUMN "id" DROP DEFAULT;
-- Set comment to column: "id" on table: "users"
COMMENT ON COLUMN "users"."id" IS 'Unique user identifier (UUIDv7, application-generated)';
-- Modify "venues" table
ALTER TABLE "venues" ADD CONSTRAINT "chk_venues_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7'::text), ALTER COLUMN "id" DROP DEFAULT;
-- Set comment to column: "id" on table: "venues"
COMMENT ON COLUMN "venues"."id" IS 'Unique venue identifier (UUIDv7, application-generated)';
