-- Modify "artist_media" table
ALTER TABLE "artist_media" ALTER COLUMN "id" SET DEFAULT uuidv7(), ALTER COLUMN "created_at" SET NOT NULL, ALTER COLUMN "updated_at" SET NOT NULL;
-- Set comment to table: "artist_media"
COMMENT ON TABLE "artist_media" IS 'Social media links and web presence for artists (Twitter, Instagram, official websites)';
-- Set comment to column: "id" on table: "artist_media"
COMMENT ON COLUMN "artist_media"."id" IS 'Unique media link identifier (UUIDv7)';
-- Set comment to column: "artist_id" on table: "artist_media"
COMMENT ON COLUMN "artist_media"."artist_id" IS 'Reference to the artist this media belongs to';
-- Set comment to column: "type" on table: "artist_media"
COMMENT ON COLUMN "artist_media"."type" IS 'Type of media link (WEB, TWITTER, INSTAGRAM)';
-- Set comment to column: "url" on table: "artist_media"
COMMENT ON COLUMN "artist_media"."url" IS 'Full URL to the artist social media profile or website';
-- Set comment to column: "created_at" on table: "artist_media"
COMMENT ON COLUMN "artist_media"."created_at" IS 'Timestamp when the media link was added';
-- Set comment to column: "updated_at" on table: "artist_media"
COMMENT ON COLUMN "artist_media"."updated_at" IS 'Timestamp when the media link was last updated';
-- Set comment to index: "idx_artist_media_artist_id" on table: "artist_media"
COMMENT ON INDEX "idx_artist_media_artist_id" IS 'Optimizes retrieval of all media for a specific artist';
-- Modify "artists" table
ALTER TABLE "artists" ALTER COLUMN "id" SET DEFAULT uuidv7(), ALTER COLUMN "spotify_id" TYPE text, ALTER COLUMN "musicbrainz_id" TYPE text, ALTER COLUMN "country" TYPE text, ALTER COLUMN "created_at" SET NOT NULL, ALTER COLUMN "updated_at" SET NOT NULL;
-- Set comment to table: "artists"
COMMENT ON TABLE "artists" IS 'Musical artists or groups that users can subscribe to for concert notifications';
-- Set comment to column: "id" on table: "artists"
COMMENT ON COLUMN "artists"."id" IS 'Unique artist identifier (UUIDv7)';
-- Set comment to column: "name" on table: "artists"
COMMENT ON COLUMN "artists"."name" IS 'Artist or band name as displayed to users';
-- Set comment to column: "spotify_id" on table: "artists"
COMMENT ON COLUMN "artists"."spotify_id" IS 'Spotify unique identifier for cross-platform data enrichment';
-- Set comment to column: "musicbrainz_id" on table: "artists"
COMMENT ON COLUMN "artists"."musicbrainz_id" IS 'MusicBrainz unique identifier for authoritative music metadata';
-- Set comment to column: "genres" on table: "artists"
COMMENT ON COLUMN "artists"."genres" IS 'Array of music genres associated with the artist';
-- Set comment to column: "country" on table: "artists"
COMMENT ON COLUMN "artists"."country" IS 'ISO 3166-1 alpha-3 country code of artist origin';
-- Set comment to column: "image_url" on table: "artists"
COMMENT ON COLUMN "artists"."image_url" IS 'URL to artist profile image or album art';
-- Set comment to column: "created_at" on table: "artists"
COMMENT ON COLUMN "artists"."created_at" IS 'Timestamp when the artist was added to the system';
-- Set comment to column: "updated_at" on table: "artists"
COMMENT ON COLUMN "artists"."updated_at" IS 'Timestamp when artist data was last updated';
-- Set comment to index: "idx_artists_name" on table: "artists"
COMMENT ON INDEX "idx_artists_name" IS 'Speeds up artist search by name';
-- Modify "venues" table
ALTER TABLE "venues" ALTER COLUMN "id" SET DEFAULT uuidv7(), ALTER COLUMN "created_at" SET NOT NULL, ALTER COLUMN "updated_at" SET NOT NULL;
-- Set comment to table: "venues"
COMMENT ON TABLE "venues" IS 'Physical locations where concerts and live events are hosted';
-- Set comment to column: "id" on table: "venues"
COMMENT ON COLUMN "venues"."id" IS 'Unique venue identifier (UUIDv7)';
-- Set comment to column: "name" on table: "venues"
COMMENT ON COLUMN "venues"."name" IS 'Venue name as displayed to users';
-- Set comment to column: "created_at" on table: "venues"
COMMENT ON COLUMN "venues"."created_at" IS 'Timestamp when the venue was added to the system';
-- Set comment to column: "updated_at" on table: "venues"
COMMENT ON COLUMN "venues"."updated_at" IS 'Timestamp when venue data was last updated';
-- Modify "concerts" table
ALTER TABLE "concerts" ALTER COLUMN "id" SET DEFAULT uuidv7(), ALTER COLUMN "status" TYPE text, ALTER COLUMN "status" SET NOT NULL, ALTER COLUMN "created_at" SET NOT NULL, ALTER COLUMN "updated_at" SET NOT NULL;
-- Create index "idx_concerts_date" to table: "concerts"
CREATE INDEX "idx_concerts_date" ON "concerts" ("date");
-- Set comment to table: "concerts"
COMMENT ON TABLE "concerts" IS 'Scheduled live music events with artist, venue, and timing information';
-- Set comment to column: "id" on table: "concerts"
COMMENT ON COLUMN "concerts"."id" IS 'Unique concert identifier (UUIDv7)';
-- Set comment to column: "artist_id" on table: "concerts"
COMMENT ON COLUMN "concerts"."artist_id" IS 'Reference to the performing artist';
-- Set comment to column: "venue_id" on table: "concerts"
COMMENT ON COLUMN "concerts"."venue_id" IS 'Reference to the venue hosting the concert';
-- Set comment to column: "title" on table: "concerts"
COMMENT ON COLUMN "concerts"."title" IS 'Concert or tour title as displayed to users';
-- Set comment to column: "date" on table: "concerts"
COMMENT ON COLUMN "concerts"."date" IS 'Date of the concert event';
-- Set comment to column: "start_time" on table: "concerts"
COMMENT ON COLUMN "concerts"."start_time" IS 'Concert start time (local to venue)';
-- Set comment to column: "open_time" on table: "concerts"
COMMENT ON COLUMN "concerts"."open_time" IS 'Doors open time (local to venue), if available';
-- Set comment to column: "status" on table: "concerts"
COMMENT ON COLUMN "concerts"."status" IS 'Concert status: scheduled, canceled, or completed';
-- Set comment to column: "created_at" on table: "concerts"
COMMENT ON COLUMN "concerts"."created_at" IS 'Timestamp when the concert was added to the system';
-- Set comment to column: "updated_at" on table: "concerts"
COMMENT ON COLUMN "concerts"."updated_at" IS 'Timestamp when concert data was last updated';
-- Set comment to index: "idx_concerts_artist_id" on table: "concerts"
COMMENT ON INDEX "idx_concerts_artist_id" IS 'Optimizes listing concerts by artist';
-- Set comment to index: "idx_concerts_venue_id" on table: "concerts"
COMMENT ON INDEX "idx_concerts_venue_id" IS 'Optimizes listing concerts by venue';
-- Set comment to index: "idx_concerts_date" on table: "concerts"
COMMENT ON INDEX "idx_concerts_date" IS 'Optimizes date-based concert queries and upcoming event searches';
-- Modify "users" table
ALTER TABLE "users" ALTER COLUMN "id" SET DEFAULT uuidv7(), ALTER COLUMN "name" TYPE text, ALTER COLUMN "email" TYPE text, ALTER COLUMN "created_at" SET DEFAULT now(), ALTER COLUMN "updated_at" SET DEFAULT now(), ADD COLUMN "preferred_language" text NULL DEFAULT 'en', ADD COLUMN "country" text NULL, ADD COLUMN "time_zone" text NULL, ADD COLUMN "is_active" boolean NOT NULL DEFAULT true;
-- Set comment to table: "users"
COMMENT ON TABLE "users" IS 'Registered users who subscribe to concert notifications for their favorite artists';
-- Set comment to column: "id" on table: "users"
COMMENT ON COLUMN "users"."id" IS 'Unique user identifier (UUIDv7)';
-- Set comment to column: "name" on table: "users"
COMMENT ON COLUMN "users"."name" IS 'User display name';
-- Set comment to column: "email" on table: "users"
COMMENT ON COLUMN "users"."email" IS 'User email address, used for authentication and notifications';
-- Set comment to column: "created_at" on table: "users"
COMMENT ON COLUMN "users"."created_at" IS 'Timestamp when the user was created';
-- Set comment to column: "updated_at" on table: "users"
COMMENT ON COLUMN "users"."updated_at" IS 'Timestamp when the user was last updated';
-- Set comment to column: "preferred_language" on table: "users"
COMMENT ON COLUMN "users"."preferred_language" IS 'ISO 639-1 language code for notification localization (e.g., en, ja)';
-- Set comment to column: "country" on table: "users"
COMMENT ON COLUMN "users"."country" IS 'ISO 3166-1 alpha-3 country code for geographic filtering';
-- Set comment to column: "time_zone" on table: "users"
COMMENT ON COLUMN "users"."time_zone" IS 'IANA time zone identifier for scheduling notifications';
-- Set comment to column: "is_active" on table: "users"
COMMENT ON COLUMN "users"."is_active" IS 'Whether the user account is active and can receive notifications';
-- Create "notifications" table
CREATE TABLE "notifications" (
  "id" uuid NOT NULL DEFAULT uuidv7(),
  "user_id" uuid NOT NULL,
  "artist_id" uuid NOT NULL,
  "concert_id" uuid NULL,
  "type" text NOT NULL,
  "title" text NOT NULL,
  "message" text NOT NULL,
  "language" text NOT NULL DEFAULT 'en',
  "status" text NOT NULL DEFAULT 'pending',
  "scheduled_at" timestamptz NOT NULL,
  "sent_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "notifications_artist_id_fkey" FOREIGN KEY ("artist_id") REFERENCES "artists" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "notifications_concert_id_fkey" FOREIGN KEY ("concert_id") REFERENCES "concerts" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "notifications_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "idx_notifications_scheduled_at" to table: "notifications"
CREATE INDEX "idx_notifications_scheduled_at" ON "notifications" ("scheduled_at");
-- Create index "idx_notifications_status" to table: "notifications"
CREATE INDEX "idx_notifications_status" ON "notifications" ("status");
-- Create index "idx_notifications_user_id" to table: "notifications"
CREATE INDEX "idx_notifications_user_id" ON "notifications" ("user_id");
-- Set comment to table: "notifications"
COMMENT ON TABLE "notifications" IS 'Scheduled and sent notifications about concerts and artist activities';
-- Set comment to column: "id" on table: "notifications"
COMMENT ON COLUMN "notifications"."id" IS 'Unique notification identifier (UUIDv7)';
-- Set comment to column: "user_id" on table: "notifications"
COMMENT ON COLUMN "notifications"."user_id" IS 'Reference to the recipient user';
-- Set comment to column: "artist_id" on table: "notifications"
COMMENT ON COLUMN "notifications"."artist_id" IS 'Reference to the artist related to this notification';
-- Set comment to column: "concert_id" on table: "notifications"
COMMENT ON COLUMN "notifications"."concert_id" IS 'Reference to the specific concert (nullable for general artist announcements)';
-- Set comment to column: "type" on table: "notifications"
COMMENT ON COLUMN "notifications"."type" IS 'Notification type: concert_announced, tickets_available, concert_reminder, concert_cancelled';
-- Set comment to column: "title" on table: "notifications"
COMMENT ON COLUMN "notifications"."title" IS 'Notification subject or headline';
-- Set comment to column: "message" on table: "notifications"
COMMENT ON COLUMN "notifications"."message" IS 'Full notification message body';
-- Set comment to column: "language" on table: "notifications"
COMMENT ON COLUMN "notifications"."language" IS 'ISO 639-1 language code for the notification content';
-- Set comment to column: "status" on table: "notifications"
COMMENT ON COLUMN "notifications"."status" IS 'Delivery status: pending, sent, failed, or cancelled';
-- Set comment to column: "scheduled_at" on table: "notifications"
COMMENT ON COLUMN "notifications"."scheduled_at" IS 'When the notification should be sent';
-- Set comment to column: "sent_at" on table: "notifications"
COMMENT ON COLUMN "notifications"."sent_at" IS 'Actual timestamp when the notification was sent (NULL if not sent)';
-- Set comment to column: "created_at" on table: "notifications"
COMMENT ON COLUMN "notifications"."created_at" IS 'Timestamp when the notification was created';
-- Set comment to column: "updated_at" on table: "notifications"
COMMENT ON COLUMN "notifications"."updated_at" IS 'Timestamp when notification status was last updated';
-- Set comment to index: "idx_notifications_scheduled_at" on table: "notifications"
COMMENT ON INDEX "idx_notifications_scheduled_at" IS 'Optimizes time-based notification scheduling and batch sending queries';
-- Set comment to index: "idx_notifications_status" on table: "notifications"
COMMENT ON INDEX "idx_notifications_status" IS 'Optimizes filtering notifications by delivery status (e.g., pending for batch processing)';
-- Set comment to index: "idx_notifications_user_id" on table: "notifications"
COMMENT ON INDEX "idx_notifications_user_id" IS 'Optimizes retrieval of notification history for a user';
-- Create "user_artist_subscriptions" table
CREATE TABLE "user_artist_subscriptions" (
  "id" uuid NOT NULL DEFAULT uuidv7(),
  "user_id" uuid NOT NULL,
  "artist_id" uuid NOT NULL,
  "is_active" boolean NOT NULL DEFAULT true,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "user_artist_subscriptions_artist_id_fkey" FOREIGN KEY ("artist_id") REFERENCES "artists" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "user_artist_subscriptions_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "idx_user_artist_subscriptions_artist_id" to table: "user_artist_subscriptions"
CREATE INDEX "idx_user_artist_subscriptions_artist_id" ON "user_artist_subscriptions" ("artist_id");
-- Create index "idx_user_artist_subscriptions_user_id" to table: "user_artist_subscriptions"
CREATE INDEX "idx_user_artist_subscriptions_user_id" ON "user_artist_subscriptions" ("user_id");
-- Set comment to table: "user_artist_subscriptions"
COMMENT ON TABLE "user_artist_subscriptions" IS 'User subscriptions to artists for automated concert notifications';
-- Set comment to column: "id" on table: "user_artist_subscriptions"
COMMENT ON COLUMN "user_artist_subscriptions"."id" IS 'Unique subscription identifier (UUIDv7)';
-- Set comment to column: "user_id" on table: "user_artist_subscriptions"
COMMENT ON COLUMN "user_artist_subscriptions"."user_id" IS 'Reference to the subscribing user';
-- Set comment to column: "artist_id" on table: "user_artist_subscriptions"
COMMENT ON COLUMN "user_artist_subscriptions"."artist_id" IS 'Reference to the artist being followed';
-- Set comment to column: "is_active" on table: "user_artist_subscriptions"
COMMENT ON COLUMN "user_artist_subscriptions"."is_active" IS 'Whether the subscription is active and should trigger notifications';
-- Set comment to column: "created_at" on table: "user_artist_subscriptions"
COMMENT ON COLUMN "user_artist_subscriptions"."created_at" IS 'Timestamp when the subscription was created';
-- Set comment to column: "updated_at" on table: "user_artist_subscriptions"
COMMENT ON COLUMN "user_artist_subscriptions"."updated_at" IS 'Timestamp when the subscription status was last modified';
-- Set comment to index: "idx_user_artist_subscriptions_artist_id" on table: "user_artist_subscriptions"
COMMENT ON INDEX "idx_user_artist_subscriptions_artist_id" IS 'Optimizes finding all subscribers for an artist (for notification fan-out)';
-- Set comment to index: "idx_user_artist_subscriptions_user_id" on table: "user_artist_subscriptions"
COMMENT ON INDEX "idx_user_artist_subscriptions_user_id" IS 'Optimizes retrieval of all artist subscriptions for a user';
-- Drop "posts" table
DROP TABLE "posts";
