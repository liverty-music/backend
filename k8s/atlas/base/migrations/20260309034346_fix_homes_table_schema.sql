-- Drop index "idx_artists_mbid" from table: "artists"
DROP INDEX "idx_artists_mbid";
-- Modify "artists" table
ALTER TABLE "artists" ADD CONSTRAINT "chk_artists_mbid_format" CHECK ((mbid IS NULL) OR (char_length(mbid) = 36)), ALTER COLUMN "mbid" TYPE text;
-- Create index "idx_artists_mbid" to table: "artists"
CREATE UNIQUE INDEX "idx_artists_mbid" ON "artists" ("mbid") WHERE ((mbid IS NOT NULL) AND (mbid <> ''::text));
-- Modify "homes" table
ALTER TABLE "homes" ADD CONSTRAINT "chk_homes_country_code_length" CHECK (char_length(country_code) = 2), ADD CONSTRAINT "chk_homes_level_1_length" CHECK ((char_length(level_1) >= 2) AND (char_length(level_1) <= 6)), ADD CONSTRAINT "chk_homes_level_2_length" CHECK ((level_2 IS NULL) OR (char_length(level_2) <= 20)), ALTER COLUMN "country_code" TYPE text, ALTER COLUMN "level_1" TYPE text, ALTER COLUMN "level_2" TYPE text, DROP COLUMN "created_at", DROP COLUMN "updated_at";
-- Set comment to column: "id" on table: "homes"
COMMENT ON COLUMN "homes"."id" IS 'Unique home record identifier';
-- Set comment to column: "user_id" on table: "homes"
COMMENT ON COLUMN "homes"."user_id" IS 'Reference to the user who owns this home (1:1)';
-- Set comment to column: "country_code" on table: "homes"
COMMENT ON COLUMN "homes"."country_code" IS 'ISO 3166-1 alpha-2 country code (e.g., JP, US)';
-- Set comment to column: "level_1" on table: "homes"
COMMENT ON COLUMN "homes"."level_1" IS 'ISO 3166-2 subdivision code (e.g., JP-13 for Tokyo, US-NY for New York)';
-- Set comment to column: "level_2" on table: "homes"
COMMENT ON COLUMN "homes"."level_2" IS 'Optional finer-grained area code. Code system determined by country_code. NULL in Phase 1.';
-- Set comment to column: "event_id" on table: "merkle_tree"
COMMENT ON COLUMN "merkle_tree"."event_id" IS 'Reference to the event this Merkle tree belongs to';
-- Set comment to column: "id" on table: "nullifiers"
COMMENT ON COLUMN "nullifiers"."id" IS 'Unique nullifier record identifier (UUIDv7)';
-- Set comment to column: "event_id" on table: "nullifiers"
COMMENT ON COLUMN "nullifiers"."event_id" IS 'Reference to the event this nullifier was used at';
-- Set comment to column: "used_at" on table: "nullifiers"
COMMENT ON COLUMN "nullifiers"."used_at" IS 'Timestamp when the nullifier was consumed for event entry';
-- Set comment to column: "id" on table: "tickets"
COMMENT ON COLUMN "tickets"."id" IS 'Unique ticket identifier (UUIDv7)';
-- Set comment to column: "event_id" on table: "tickets"
COMMENT ON COLUMN "tickets"."event_id" IS 'Reference to the event this ticket grants entry to';
-- Set comment to column: "user_id" on table: "tickets"
COMMENT ON COLUMN "tickets"."user_id" IS 'Reference to the ticket holder';
-- Set comment to column: "minted_at" on table: "tickets"
COMMENT ON COLUMN "tickets"."minted_at" IS 'Timestamp when the ticket was minted on-chain';
-- Set comment to column: "admin_area" on table: "venues"
COMMENT ON COLUMN "venues"."admin_area" IS 'ISO 3166-2 subdivision code (e.g., JP-13) for the venue location; NULL when not determinable with confidence';
-- Modify "users" table
ALTER TABLE "users" DROP CONSTRAINT "users_home_id_fkey", ADD CONSTRAINT "fk_users_home_id" FOREIGN KEY ("home_id") REFERENCES "homes" ("id") ON UPDATE NO ACTION ON DELETE SET NULL;
