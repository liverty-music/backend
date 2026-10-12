-- liverty-music backend database schema
-- This schema follows Clean Architecture principles by separating
-- user management, artist discovery, and concert notifications.

CREATE SCHEMA IF NOT EXISTS app;
SET search_path TO app, public;

-- Users table
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY,
    provider_user_id TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    email TEXT UNIQUE NOT NULL,
    preferred_language TEXT,
    time_zone TEXT,
    home_id UUID,
    full_name TEXT,
    phone_number TEXT,
    CONSTRAINT chk_users_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_users_full_name_len CHECK (full_name IS NULL OR char_length(full_name) BETWEEN 1 AND 200),
    CONSTRAINT chk_users_phone_number_e164 CHECK (phone_number IS NULL OR phone_number ~ '^\+[1-9][0-9]{1,14}$')
);

COMMENT ON TABLE users IS 'User profiles and authentication data';
COMMENT ON COLUMN users.id IS 'Unique user identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN users.provider_user_id IS 'The identity provider''s ID for the user (Zitadel sub claim), used for account sync';
COMMENT ON COLUMN users.email IS 'Primary contact and login identifier';
COMMENT ON COLUMN users.name IS 'User display name from identity provider';
COMMENT ON COLUMN users.preferred_language IS 'User preferred language code (e.g., en, ja). NULL means "not yet set by client"; client backfills via UpdatePreferredLanguage on first observation.';
COMMENT ON COLUMN users.time_zone IS 'User time zone (IANA time zone database)';
COMMENT ON COLUMN users.home_id IS 'Reference to the user home area in the homes table. NULL when home is not set.';
COMMENT ON COLUMN users.full_name IS '本人確認 full name (1-200 characters) shown on the face of the user''s tickets; NULL until the user first gives it';
COMMENT ON COLUMN users.phone_number IS '本人確認 phone number in E.164 form shown on the face of the user''s tickets; NULL until the user first gives it';

-- Homes table
CREATE TABLE IF NOT EXISTS homes (
    id UUID PRIMARY KEY,
    country_code TEXT NOT NULL,
    level_1 TEXT NOT NULL,
    level_2 TEXT,
    centroid_latitude DOUBLE PRECISION,
    centroid_longitude DOUBLE PRECISION,
    CONSTRAINT chk_homes_country_code_length CHECK (char_length(country_code) = 2),
    CONSTRAINT chk_homes_level_1_length CHECK (char_length(level_1) BETWEEN 2 AND 6),
    CONSTRAINT chk_homes_level_2_length CHECK (level_2 IS NULL OR char_length(level_2) <= 20),
    CONSTRAINT chk_homes_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7')
);

ALTER TABLE users ADD CONSTRAINT fk_users_home_id FOREIGN KEY (home_id) REFERENCES homes(id) ON DELETE SET NULL;

COMMENT ON TABLE homes IS 'Structured geographic home area for users. Determines proximity classification (home/nearby/away).';
COMMENT ON COLUMN homes.id IS 'Unique home record identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN homes.country_code IS 'ISO 3166-1 alpha-2 country code (e.g., JP, US)';
COMMENT ON COLUMN homes.level_1 IS 'ISO 3166-2 subdivision code (e.g., JP-13 for Tokyo, US-NY for New York)';
COMMENT ON COLUMN homes.level_2 IS 'Optional finer-grained area code. Code system determined by country_code. NULL in Phase 1.';
COMMENT ON COLUMN homes.centroid_latitude IS 'Approximate latitude of the home area centroid, resolved at write time from level_1. Used for proximity calculations.';
COMMENT ON COLUMN homes.centroid_longitude IS 'Approximate longitude of the home area centroid, resolved at write time from level_1. Used for proximity calculations.';

-- Artists table
CREATE TABLE IF NOT EXISTS artists (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    mbid TEXT NOT NULL,
    fanart JSONB,
    fanart_synced_at TIMESTAMPTZ,
    official_site_checked_at TIMESTAMPTZ,
    CONSTRAINT chk_artists_mbid_format CHECK (char_length(mbid) = 36),
    CONSTRAINT chk_artists_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7')
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_artists_mbid ON artists(mbid);

COMMENT ON TABLE artists IS 'Musical artists or groups that users can subscribe to for concert notifications';
COMMENT ON COLUMN artists.id IS 'Unique artist identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN artists.name IS 'Artist or band name as displayed to users';
COMMENT ON COLUMN artists.mbid IS 'Canonical MusicBrainz Identifier (MBID format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx)';
COMMENT ON COLUMN artists.fanart IS 'Cached fanart.tv API response containing community-curated artist images (thumb, background, logo, banner)';
COMMENT ON COLUMN artists.fanart_synced_at IS 'Timestamp of the last successful fanart.tv API sync for this artist';
COMMENT ON COLUMN artists.official_site_checked_at IS 'Timestamp of the last official-site check in MusicBrainz for this artist, whatever the check found. NULL when never checked.';

-- Artist official site
CREATE TABLE IF NOT EXISTS artist_official_site (
    id UUID PRIMARY KEY,
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE UNIQUE,
    url TEXT NOT NULL,
    CONSTRAINT chk_artist_official_site_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7')
);

COMMENT ON TABLE artist_official_site IS 'Stores the official website URL for each artist, used for concert search grounding.';
COMMENT ON COLUMN artist_official_site.id IS 'Unique identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN artist_official_site.artist_id IS 'Reference to the artist (1:1 relationship)';
COMMENT ON COLUMN artist_official_site.url IS 'Official artist website URL';

-- Venues table
CREATE TABLE IF NOT EXISTS venues (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    admin_area TEXT,
    google_place_id TEXT,
    latitude DOUBLE PRECISION,
    longitude DOUBLE PRECISION,
    listed_venue_name TEXT,
    CONSTRAINT chk_venues_name_not_empty CHECK (name <> ''),
    CONSTRAINT chk_venues_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7')
);

COMMENT ON TABLE venues IS 'Physical locations where concerts and live events are hosted';
COMMENT ON COLUMN venues.id IS 'Unique venue identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN venues.name IS 'Canonical venue name from Google Places API';
COMMENT ON COLUMN venues.admin_area IS 'ISO 3166-2 subdivision code (e.g., JP-13) for the venue location; NULL when not determinable with confidence';
COMMENT ON COLUMN venues.google_place_id IS 'Google Maps Place ID for the canonical venue record';
COMMENT ON COLUMN venues.latitude IS 'WGS 84 latitude of the venue from Google Places API';
COMMENT ON COLUMN venues.longitude IS 'WGS 84 longitude of the venue from Google Places API';
COMMENT ON COLUMN venues.listed_venue_name IS 'Raw scraped venue name as returned by Gemini; used for DB-first lookup to avoid redundant Places API calls';

-- Series type enum
CREATE TYPE series_type AS ENUM ('TOUR', 'SINGLE', 'FESTIVAL');

COMMENT ON TYPE series_type IS 'Classification of an event series: TOUR (multi-venue), SINGLE (single-venue standalone, possibly multi-day), FESTIVAL (multi-performer)';

-- Series visibility enum (first-party authoring). PASSWORD is reserved for a
-- future authoring extension; add it with ALTER TYPE when introduced.
CREATE TYPE series_visibility AS ENUM ('PUBLIC', 'UNLISTED');

COMMENT ON TYPE series_visibility IS 'Who can reach a first-party (organizer-authored) series: PUBLIC (normal discovery) or UNLISTED (signed tokenized URL only). NULL for discovered series.';

-- Series publish-state enum (first-party authoring lifecycle). SCHEDULED is
-- reserved for a future authoring extension; add it with ALTER TYPE when
-- introduced.
CREATE TYPE series_publish_state AS ENUM ('DRAFT', 'PUBLISHED', 'CANCELLED');

COMMENT ON TYPE series_publish_state IS 'Authoring lifecycle of a first-party series: DRAFT (console-only), PUBLISHED (live), CANCELLED (terminal). NULL for discovered series.';

-- Series table
CREATE TABLE IF NOT EXISTS series (
    id UUID PRIMARY KEY,
    title TEXT NOT NULL,
    type series_type NOT NULL,
    source_url TEXT,
    description TEXT,
    organizer_id UUID REFERENCES organizers(id),
    visibility series_visibility,
    publish_state series_publish_state,
    unlisted_token TEXT,
    published_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    CONSTRAINT chk_series_title_not_empty CHECK (title <> ''),
    CONSTRAINT chk_series_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    -- A first-party series (organizer_id set) always carries visibility and
    -- publish_state; a discovered series (organizer_id NULL) carries neither.
    CONSTRAINT chk_series_first_party_state CHECK (
        (organizer_id IS NULL AND visibility IS NULL AND publish_state IS NULL) OR
        (organizer_id IS NOT NULL AND visibility IS NOT NULL AND publish_state IS NOT NULL)
    )
);

COMMENT ON TABLE series IS 'Parent aggregation above events. Owns metadata shared across every event in a tour, festival, or multi-day single-venue run. Cover image URL is derived from the series_cover_images join at read time.';
COMMENT ON COLUMN series.id IS 'Unique series identifier (UUIDv7, application-generated). Series has no content-derived key: cross-run identity is established by adopting the series_id already carried by its member events (matched on the events physical natural key); a fresh UUIDv7 series is minted only when no member event yet exists.';
COMMENT ON COLUMN series.title IS 'Series title shared across all member events (e.g. tour name, festival name)';
COMMENT ON COLUMN series.type IS 'Classification of the series; drives presentation and notification grouping';
COMMENT ON COLUMN series.source_url IS 'Optional series-level official URL (tour page, festival page); per-event URLs are not stored';
COMMENT ON COLUMN series.description IS 'Free-form body text of a first-party series page, authored by an organizer. NULL for discovered series or organizer series without a write-up.';
COMMENT ON COLUMN series.organizer_id IS 'Owning organizer for a first-party series; NULL marks a discovery-pipeline series. Non-null makes the series organizer-authored.';
COMMENT ON COLUMN series.visibility IS 'First-party visibility (PUBLIC / UNLISTED). NULL for discovered series.';
COMMENT ON COLUMN series.publish_state IS 'First-party authoring lifecycle (DRAFT / PUBLISHED / CANCELLED). NULL for discovered series.';
COMMENT ON COLUMN series.unlisted_token IS 'Backend-only HMAC-derived share token for an UNLISTED series; rotated by RegenerateToken. NULL unless the series is UNLISTED. Never exposed on read DTOs.';
COMMENT ON COLUMN series.published_at IS 'When the series was first published (first PUBLISHED transition). NULL while DRAFT.';
COMMENT ON COLUMN series.cancelled_at IS 'When the series was cancelled. NULL unless CANCELLED.';

-- Events table
CREATE TABLE IF NOT EXISTS events (
    id UUID PRIMARY KEY,
    series_id UUID NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    venue_id UUID NOT NULL REFERENCES venues(id) ON DELETE CASCADE,
    listed_venue_name TEXT,
    local_event_date DATE NOT NULL,
    start_at TIMESTAMPTZ,
    open_at TIMESTAMPTZ,
    CONSTRAINT uq_events_natural_key UNIQUE NULLS NOT DISTINCT (venue_id, local_event_date, start_at),
    CONSTRAINT chk_events_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7')
);

COMMENT ON TABLE events IS 'A single performance occurring on a specific date at a specific venue. Belongs to exactly one parent series.';
COMMENT ON CONSTRAINT uq_events_natural_key ON events IS 'Physical identity of a performance: one row per (venue, local date, start time), independent of series or performing artist. start_at is part of the key so two shows at one venue on one date with different start times (matinee/evening) are distinct; NULLS NOT DISTINCT collapses two shows whose start time is not yet published. The same physical show discovered via different artists/series resolves to one row.';
COMMENT ON COLUMN events.id IS 'Unique event identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN events.series_id IS 'Reference to the parent series that aggregates this event with any sibling events. Not part of the natural key — series is a grouping parent, not a component of event identity.';
COMMENT ON COLUMN events.venue_id IS 'Reference to the venue hosting the event';
COMMENT ON COLUMN events.listed_venue_name IS 'Raw venue name as scraped from the source, preserved separately from the normalized venue record';
COMMENT ON COLUMN events.local_event_date IS 'Date of the event';
COMMENT ON COLUMN events.start_at IS 'Event start time (absolute)';
COMMENT ON COLUMN events.open_at IS 'Doors open time (absolute), if available';

-- Concerts table
CREATE TABLE IF NOT EXISTS concerts (
    event_id UUID PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE
);

COMMENT ON TABLE concerts IS 'Music-specific event extension, linked 1:1 with events. Its performing artists are in concert_artists.';
COMMENT ON COLUMN concerts.event_id IS 'Reference to the generic event (PK/FK)';

-- Concert artists (M:N between concerts and artists). Keyed by the concert, not
-- the generic event, so artists attach only to the music kind of event.
CREATE TABLE IF NOT EXISTS concert_artists (
    event_id UUID NOT NULL REFERENCES concerts(event_id) ON DELETE CASCADE,
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    PRIMARY KEY (event_id, artist_id)
);

COMMENT ON TABLE concert_artists IS 'M:N relation between concerts and performing artists. Supports festival lineups, co-headliners, and support acts.';
COMMENT ON COLUMN concert_artists.event_id IS 'Reference to the concert (its event id)';
COMMENT ON COLUMN concert_artists.artist_id IS 'Reference to the performing artist';

-- Draft events (first-party authoring staging).
-- While a first-party series is a DRAFT its performances are held here rather
-- than in "events", so the live natural-key space (uq_events_natural_key) stays
-- clean and no discovered slot is claimed before publish. On publish each draft
-- event is materialized into "events" via the natural-key upsert (with the
-- supersede / suppression / cross-organizer rules); on cancel or delete the
-- draft rows cascade away. There is intentionally NO natural-key uniqueness here
-- so an organizer can freely edit a draft.
CREATE TABLE IF NOT EXISTS draft_events (
    id UUID PRIMARY KEY,
    series_id UUID NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    venue_id UUID NOT NULL REFERENCES venues(id) ON DELETE CASCADE,
    listed_venue_name TEXT,
    local_event_date DATE NOT NULL,
    start_at TIMESTAMPTZ,
    open_at TIMESTAMPTZ,
    CONSTRAINT chk_draft_events_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7')
);

COMMENT ON TABLE draft_events IS 'Unpublished performances of a first-party DRAFT series, held out of the live "events" table until publish. No natural-key uniqueness so drafts are freely editable.';
COMMENT ON COLUMN draft_events.id IS 'Unique draft-event identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN draft_events.series_id IS 'Parent first-party series being authored';
COMMENT ON COLUMN draft_events.venue_id IS 'Venue resolved (Places get-or-create) at draft time';
COMMENT ON COLUMN draft_events.listed_venue_name IS 'Raw venue name the organizer entered, preserved for display and re-resolution';
COMMENT ON COLUMN draft_events.local_event_date IS 'Date of the performance';
COMMENT ON COLUMN draft_events.start_at IS 'Performance start time (absolute), if set';
COMMENT ON COLUMN draft_events.open_at IS 'Doors open time (absolute), if set';

-- Draft series performers (series-level, first-party authoring).
-- Performers are chosen at the series level (applied to every event) from the
-- organizer's represented artists. On publish these become concert_artists on
-- each materialized event.
CREATE TABLE IF NOT EXISTS draft_series_performers (
    series_id UUID NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    PRIMARY KEY (series_id, artist_id)
);

COMMENT ON TABLE draft_series_performers IS 'Series-level performers of a first-party DRAFT series, materialized into concert_artists for every event on publish.';
COMMENT ON COLUMN draft_series_performers.series_id IS 'Parent first-party series being authored';
COMMENT ON COLUMN draft_series_performers.artist_id IS 'A performing artist the organizer represents';

-- Series images: images an organizer uploaded for one of its Series. The row
-- id is a UUIDv7 that serves as both the creation-time source and the
-- cache-busting object-key token. The object key is
-- `{exposure}/{organizer_id}/{series_image_id}` where exposure is "internal" or
-- "cdn". No URL is stored; it is derived at read time.
CREATE TABLE IF NOT EXISTS series_images (
    id           UUID       PRIMARY KEY,
    organizer_id UUID       NOT NULL REFERENCES organizers(id),
    attributes   JSONB      NOT NULL DEFAULT '{}',
    CONSTRAINT chk_series_images_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7')
);

COMMENT ON TABLE series_images IS 'Images uploaded by an organizer for its Series. The row id is the UUIDv7 that forms the object-key basename ({org}/{id} in the internal bucket, cdn/{org}/{id}/ in the served bucket); content_type is stored in attributes.';
COMMENT ON COLUMN series_images.id IS 'Unique series image identifier (UUIDv7, application-generated). Doubles as the cache-busting version token in the object key; a new upload mints a new id.';
COMMENT ON COLUMN series_images.organizer_id IS 'Owning organizer. Used as the stable tenant segment of the object key.';
COMMENT ON COLUMN series_images.attributes IS 'Upload metadata as JSONB: {"content_type": "image/jpeg"}. May include width/height later.';

-- Series cover images: the one SeriesImage shown as each Series' cover
-- (uq_series_cover_images_series).
CREATE TABLE IF NOT EXISTS series_cover_images (
    series_id       UUID NOT NULL REFERENCES series(id)        ON DELETE CASCADE,
    series_image_id UUID NOT NULL REFERENCES series_images(id) ON DELETE CASCADE,
    PRIMARY KEY (series_id, series_image_id)
);

COMMENT ON TABLE series_cover_images IS 'The cover SeriesImage of a Series. One cover per series (uq_series_cover_images_series).';
COMMENT ON COLUMN series_cover_images.series_id IS 'The Series whose cover this is.';
COMMENT ON COLUMN series_cover_images.series_image_id IS 'The SeriesImage shown as the cover.';

CREATE UNIQUE INDEX IF NOT EXISTS uq_series_cover_images_series ON series_cover_images(series_id);
COMMENT ON INDEX uq_series_cover_images_series IS 'At most one cover image per series.';

-- User artist follows
CREATE TABLE IF NOT EXISTS followed_artists (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    hype TEXT NOT NULL DEFAULT 'nearby',
    PRIMARY KEY (user_id, artist_id),
    CONSTRAINT chk_followed_artists_hype CHECK (hype IN ('watch', 'home', 'nearby', 'away'))
);

COMMENT ON TABLE followed_artists IS 'Tracks which artists a user is following for discovery and personalization';
COMMENT ON COLUMN followed_artists.user_id IS 'Reference to the user who is following';
COMMENT ON COLUMN followed_artists.artist_id IS 'Reference to the artist being followed';
COMMENT ON COLUMN followed_artists.hype IS 'User enthusiasm tier: watch (no notifications), home (home area only), nearby (within ~200km of home, default), or away (all concerts)';

-- Concert searches table (the latest search per artist)
CREATE TABLE IF NOT EXISTS concert_searches (
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    searched_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status TEXT NOT NULL DEFAULT 'completed',
    last_found_at TIMESTAMPTZ,
    PRIMARY KEY (artist_id)
);

COMMENT ON TABLE concert_searches IS 'Tracks when each artist was last searched for concerts via external APIs';
COMMENT ON COLUMN concert_searches.artist_id IS 'Reference to the artist that was searched';
COMMENT ON COLUMN concert_searches.searched_at IS 'Timestamp of the most recent external search';
COMMENT ON COLUMN concert_searches.status IS 'Search job status: pending, completed, or failed';
COMMENT ON COLUMN concert_searches.last_found_at IS 'Timestamp of the most recent search that discovered at least one new concert; NULL if none ever found';

-- Ticket journeys table (user-managed ticket acquisition tracking)
CREATE TABLE IF NOT EXISTS ticket_journeys (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    status SMALLINT NOT NULL,
    PRIMARY KEY (user_id, event_id),
    CONSTRAINT chk_ticket_journeys_status CHECK (status BETWEEN 1 AND 5)
);

COMMENT ON TABLE ticket_journeys IS 'Per-user ticket acquisition status tracking for events. Status values: 1=TRACKING, 2=APPLIED, 3=LOST, 4=UNPAID, 5=PAID';
COMMENT ON COLUMN ticket_journeys.user_id IS 'Reference to the fan tracking this event';
COMMENT ON COLUMN ticket_journeys.event_id IS 'Reference to the event being tracked';
COMMENT ON COLUMN ticket_journeys.status IS 'Ticket journey status: 1=TRACKING, 2=APPLIED, 3=LOST, 4=UNPAID, 5=PAID';

-- Push subscriptions table
CREATE TABLE IF NOT EXISTS push_subscriptions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint TEXT NOT NULL UNIQUE,
    p256dh TEXT NOT NULL,
    auth TEXT NOT NULL,
    CONSTRAINT chk_push_subscriptions_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7')
);

COMMENT ON TABLE push_subscriptions IS 'Browser Web Push subscription data for delivering notifications';
COMMENT ON COLUMN push_subscriptions.id IS 'Unique identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN push_subscriptions.user_id IS 'Reference to the user who owns this subscription';
COMMENT ON COLUMN push_subscriptions.endpoint IS 'Push service endpoint URL provided by the browser';
COMMENT ON COLUMN push_subscriptions.p256dh IS 'ECDH public key for payload encryption (Base64url-encoded)';
COMMENT ON COLUMN push_subscriptions.auth IS 'Authentication secret for payload encryption (Base64url-encoded)';

-- Notifications log
-- One row per logical user-facing notification (new-concert alert, sales reminder,
-- sales-phase announcement, ...). The record is the source of truth for delivery
-- auditing and the in-app inbox: it is created (queued) before the channel send and
-- then updated to delivered/failed with the outcome in hand. Per-channel delivery
-- state is kept inline because web push is the only channel today; a normalised
-- notification_deliveries child table can be added additively when a second channel
-- (email / in-app) arrives.
CREATE TABLE IF NOT EXISTS notifications (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    payload JSONB NOT NULL,
    delivery_status TEXT NOT NULL DEFAULT 'queued',
    failure_reason TEXT,
    queued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at TIMESTAMPTZ,
    read_at TIMESTAMPTZ,
    dismissed_at TIMESTAMPTZ,
    CONSTRAINT chk_notifications_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_notifications_delivery_status CHECK (delivery_status IN ('queued', 'delivered', 'failed'))
);

COMMENT ON TABLE notifications IS 'Notification log: one durable record per user-facing notification, with per-channel delivery state (queued/delivered/failed) and per-user read/dismiss state. Source of truth for delivery auditing and the in-app inbox.';
COMMENT ON COLUMN notifications.id IS 'Unique notification identifier (UUIDv7, application-generated). Propagated into the push payload data.notification_id as the end-to-end correlation key.';
COMMENT ON COLUMN notifications.user_id IS 'Reference to the recipient user';
COMMENT ON COLUMN notifications.type IS 'Notification type: new_concerts, sales_reminder, sales_phase_announcement';
COMMENT ON COLUMN notifications.payload IS 'Rendered notification payload (title, body, url, tag) as delivered to the channel';
COMMENT ON COLUMN notifications.delivery_status IS 'Web-push channel delivery state: queued (on creation), delivered (push service accepted the send), or failed';
COMMENT ON COLUMN notifications.failure_reason IS 'Human-readable reason set when delivery_status is failed; NULL otherwise';
COMMENT ON COLUMN notifications.queued_at IS 'Timestamp when the notification record was created in the queued state';
COMMENT ON COLUMN notifications.delivered_at IS 'Timestamp when the channel accepted the send; NULL until delivered';
COMMENT ON COLUMN notifications.read_at IS 'Timestamp when the user marked the notification read; NULL until read';
COMMENT ON COLUMN notifications.dismissed_at IS 'Timestamp when the user dismissed the notification; NULL until dismissed';

-- Sales phases table
-- Represents a single series-level ticket-sales window: one application period
-- under one method (lottery or first come). A phase applies to its series as a
-- whole; there is no per-event coverage subset. Re-discovery converges on
-- (series_id, method, apply start date in Japan time), enforced by a unique key.
CREATE TABLE IF NOT EXISTS sales_phases (
    id UUID PRIMARY KEY,
    series_id UUID NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    method SMALLINT NOT NULL,
    apply_start_at TIMESTAMPTZ NOT NULL,
    apply_end_at TIMESTAMPTZ,
    lottery_result_at TIMESTAMPTZ,
    discovered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    apply_start_date_jst DATE GENERATED ALWAYS AS ((apply_start_at AT TIME ZONE 'Asia/Tokyo')::date) STORED,
    CONSTRAINT chk_sales_phases_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_sales_phases_method CHECK (method IN (1, 2)),
    CONSTRAINT chk_sales_phases_result_only_lottery CHECK (method = 1 OR lottery_result_at IS NULL),
    CONSTRAINT chk_sales_phases_lottery_has_end CHECK (method = 2 OR apply_end_at IS NOT NULL),
    CONSTRAINT chk_sales_phases_end_after_start CHECK (apply_end_at IS NULL OR apply_end_at > apply_start_at),
    CONSTRAINT uq_sales_phases_series_method_start_date UNIQUE (series_id, method, apply_start_date_jst)
);

COMMENT ON TABLE sales_phases IS 'A single series-level ticket-sales window. Re-discovered phases converge on (series_id, method, apply_start_date_jst).';
COMMENT ON COLUMN sales_phases.id IS 'Unique sales phase identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN sales_phases.series_id IS 'Reference to the parent series that owns this sales phase';
COMMENT ON COLUMN sales_phases.method IS 'Sales method: 1=LOTTERY, 2=FIRST_COME';
COMMENT ON COLUMN sales_phases.apply_start_at IS 'Start of the application or on-sale window (required).';
COMMENT ON COLUMN sales_phases.apply_end_at IS 'End of the application window. Required for a lottery; NULL for a first-come sale that ends when tickets run out.';
COMMENT ON COLUMN sales_phases.lottery_result_at IS 'When lottery results are announced. NULL for first-come phases or when not announced.';
COMMENT ON COLUMN sales_phases.discovered_at IS 'Timestamp when this sales phase row was first inserted. Used as the first-sight guard: stages whose natural trigger is before discovered_at are not fired.';
COMMENT ON COLUMN sales_phases.apply_start_date_jst IS 'Calendar date of apply_start_at in Asia/Tokyo; part of the re-discovery convergence key.';

-- Sales phase search logs
-- Records when each series was last searched for sales phases, so discovery
-- searches a series at most once per interval whether or not it found anything.
CREATE TABLE IF NOT EXISTS sales_phase_search_logs (
    series_id UUID PRIMARY KEY REFERENCES series(id) ON DELETE CASCADE,
    searched_at TIMESTAMPTZ NOT NULL
);

COMMENT ON TABLE sales_phase_search_logs IS 'When each series was last searched for ticket sales phases. One row per series.';
COMMENT ON COLUMN sales_phase_search_logs.series_id IS 'The series that was searched';
COMMENT ON COLUMN sales_phase_search_logs.searched_at IS 'When the series was last searched successfully';

-- Sales phase reminders sent-log
-- Tracks which reminder stages have already been dispatched to each user for a
-- given sales phase. UNIQUE (user_id, sales_phase_id, stage) prevents double-send.
CREATE TABLE IF NOT EXISTS sales_phase_reminders (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sales_phase_id UUID NOT NULL REFERENCES sales_phases(id) ON DELETE CASCADE,
    stage SMALLINT NOT NULL,
    sent_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_sales_phase_reminders UNIQUE (user_id, sales_phase_id, stage),
    CONSTRAINT chk_sales_phase_reminders_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_sales_phase_reminders_stage CHECK (stage BETWEEN 1 AND 10)
);

COMMENT ON TABLE sales_phase_reminders IS 'Sent-log for sales phase reminder notifications. UNIQUE (user_id, sales_phase_id, stage) prevents duplicate dispatches.';
COMMENT ON COLUMN sales_phase_reminders.id IS 'Unique reminder record identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN sales_phase_reminders.user_id IS 'Reference to the user who received the reminder';
COMMENT ON COLUMN sales_phase_reminders.sales_phase_id IS 'Reference to the sales phase this reminder relates to';
COMMENT ON COLUMN sales_phase_reminders.stage IS 'Reminder stage: 1=APPLY_OPEN (at apply_start_time for a lottery, 30 minutes before it for a first-come sale), 2=APPLY_CLOSE_24H (24h before apply_end_time, lottery only), 4=RESULT_DAY (09:00 on lottery_result_time day). 3 was APPLY_CLOSE_1H and is unused.';
COMMENT ON COLUMN sales_phase_reminders.sent_at IS 'Timestamp when the reminder was dispatched';

-- Concert candidates (approval queue)
-- Concerts discovered by the Gemini search pipeline are held here in a pending
-- state until a developer approves them in the admin console. Venue resolution
-- (Google Places) runs at staging time and is denormalised onto the row so the
-- reviewer can judge venue accuracy; the canonical venues row is created only on
-- approval. This table holds only pending rows — both approve and reject delete
-- the row, so a re-discovered concert can re-enter the queue after a rejection.
CREATE TABLE IF NOT EXISTS concert_candidates (
    id UUID PRIMARY KEY,
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    series_id UUID NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    local_date DATE NOT NULL,
    start_at TIMESTAMPTZ,
    open_at TIMESTAMPTZ,
    listed_venue_name TEXT NOT NULL,
    admin_area TEXT,
    source_url TEXT,
    resolved_place_id TEXT,
    resolved_venue_name TEXT,
    resolved_admin_area TEXT,
    resolved_latitude DOUBLE PRECISION,
    resolved_longitude DOUBLE PRECISION,
    discovered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_concert_candidates_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7')
);

COMMENT ON TABLE concert_candidates IS 'Approval queue for AI-discovered concerts. Holds only pending rows; approve publishes and deletes, reject logs and deletes. Re-discovery dedup consults this table plus published events, but never the rejection log.';
COMMENT ON COLUMN concert_candidates.id IS 'Unique concert candidate identifier (UUIDv7, application-generated). Exposed to the admin console as ConcertCandidateId.';
COMMENT ON COLUMN concert_candidates.artist_id IS 'The performing artist this concert was discovered for.';
COMMENT ON COLUMN concert_candidates.series_id IS 'Parent series this staged event belongs to. The series row is created at discovery time (when the group series_id is resolved), so this is a real foreign key by staging time; approval inserts the event under it without minting a new series. type/title/source_url live on the series row.';
COMMENT ON COLUMN concert_candidates.title IS 'Descriptive title extracted for the concert (e.g. tour or show name).';
COMMENT ON COLUMN concert_candidates.local_date IS 'Scheduled calendar date of the concert in the venue local timezone.';
COMMENT ON COLUMN concert_candidates.start_at IS 'Scheduled start time. NULL when the source did not state one.';
COMMENT ON COLUMN concert_candidates.open_at IS 'Doors-open time. NULL when not announced.';
COMMENT ON COLUMN concert_candidates.listed_venue_name IS 'Raw venue name exactly as scraped from the source, preserved for review.';
COMMENT ON COLUMN concert_candidates.admin_area IS 'Administrative area extracted by Gemini for the concert. NULL when not extracted.';
COMMENT ON COLUMN concert_candidates.source_url IS 'Source URL where the concert was found. NULL when not provided.';
COMMENT ON COLUMN concert_candidates.resolved_place_id IS 'Google Places place id of the resolved venue. NULL when the listed name could not be resolved.';
COMMENT ON COLUMN concert_candidates.resolved_venue_name IS 'Canonical venue name resolved via Google Places. NULL when unresolved.';
COMMENT ON COLUMN concert_candidates.resolved_admin_area IS 'ISO 3166-2 admin area of the resolved venue. NULL when unresolved or indeterminate.';
COMMENT ON COLUMN concert_candidates.resolved_latitude IS 'WGS 84 latitude of the resolved venue. NULL when unresolved.';
COMMENT ON COLUMN concert_candidates.resolved_longitude IS 'WGS 84 longitude of the resolved venue. NULL when unresolved.';
COMMENT ON COLUMN concert_candidates.discovered_at IS 'Timestamp when the discovery pipeline staged this concert. Used to order the review queue.';

-- Rejected concerts (append-only)
-- Every rejection is recorded here for search-quality analysis. It is NEVER read
-- by the discovery dedup path and so does not suppress future staging. artist_id
-- has no foreign key so history survives artist deletion.
CREATE TABLE IF NOT EXISTS rejected_concerts (
    id UUID PRIMARY KEY,
    artist_id UUID NOT NULL,
    artist_name TEXT NOT NULL,
    title TEXT NOT NULL,
    local_date DATE NOT NULL,
    start_at TIMESTAMPTZ,
    open_at TIMESTAMPTZ,
    listed_venue_name TEXT NOT NULL,
    admin_area TEXT,
    source_url TEXT,
    resolved_place_id TEXT,
    resolved_venue_name TEXT,
    resolved_admin_area TEXT,
    reason TEXT NOT NULL,
    reviewed_by TEXT,
    rejected_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_rejected_concerts_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7')
);

COMMENT ON TABLE rejected_concerts IS 'Append-only record of rejected concert candidates, used for searcher-quality analysis only. Not consulted by discovery dedup; never suppresses re-discovery.';
COMMENT ON COLUMN rejected_concerts.id IS 'Unique rejected concert identifier (UUIDv7, application-generated).';
COMMENT ON COLUMN rejected_concerts.artist_id IS 'The performing artist the rejected concert was discovered for. Intentionally not a foreign key so the log survives artist deletion.';
COMMENT ON COLUMN rejected_concerts.artist_name IS 'Artist display name captured at rejection time for readability.';
COMMENT ON COLUMN rejected_concerts.title IS 'Descriptive title of the rejected concert.';
COMMENT ON COLUMN rejected_concerts.local_date IS 'Scheduled calendar date of the rejected concert.';
COMMENT ON COLUMN rejected_concerts.start_at IS 'Scheduled start time of the rejected concert. NULL when unknown.';
COMMENT ON COLUMN rejected_concerts.open_at IS 'Doors-open time of the rejected concert. NULL when unknown.';
COMMENT ON COLUMN rejected_concerts.listed_venue_name IS 'Raw scraped venue name of the rejected concert.';
COMMENT ON COLUMN rejected_concerts.admin_area IS 'Administrative area extracted for the rejected concert. NULL when not extracted.';
COMMENT ON COLUMN rejected_concerts.source_url IS 'Source URL of the rejected concert. NULL when not provided.';
COMMENT ON COLUMN rejected_concerts.resolved_place_id IS 'Google Places place id of the resolved venue at rejection time. NULL when unresolved.';
COMMENT ON COLUMN rejected_concerts.resolved_venue_name IS 'Resolved canonical venue name at rejection time. NULL when unresolved.';
COMMENT ON COLUMN rejected_concerts.resolved_admin_area IS 'Resolved admin area at rejection time. NULL when unresolved.';
COMMENT ON COLUMN rejected_concerts.reason IS 'Reviewer-provided reason for rejecting the concert.';
COMMENT ON COLUMN rejected_concerts.reviewed_by IS 'Identity (Zitadel subject) of the developer who rejected the concert. NULL when unavailable.';
COMMENT ON COLUMN rejected_concerts.rejected_at IS 'Timestamp when the concert was rejected.';

-- Suppressed concerts (moderation)
-- Records the natural key of a published concert an operator deleted so a later
-- discovery run does not auto-publish or re-stage it. Keyed by the event physical
-- identity (venue, local date, start time) with NULLS NOT DISTINCT so an
-- unknown-start slot collapses the same way the events unique key does. venue_id
-- carries no foreign key so a suppression survives independently of venue
-- lifecycle, mirroring the FK-less rejected_concerts. Distinct from that log,
-- which is analysis-only and never suppresses.
CREATE TABLE IF NOT EXISTS suppressed_concerts (
    id UUID PRIMARY KEY,
    venue_id UUID NOT NULL,
    local_event_date DATE NOT NULL,
    start_at TIMESTAMPTZ,
    suppressed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_suppressed_concerts_natural_key UNIQUE NULLS NOT DISTINCT (venue_id, local_event_date, start_at),
    CONSTRAINT chk_suppressed_concerts_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7')
);

COMMENT ON TABLE suppressed_concerts IS 'Moderation suppression set: the natural key of each published concert an operator deleted, consulted by discovery so a deleted concert is not auto-published or re-staged again. Distinct from rejected_concerts, which is analysis-only and never suppresses.';
COMMENT ON COLUMN suppressed_concerts.id IS 'Unique suppression entry identifier (UUIDv7, application-generated).';
COMMENT ON COLUMN suppressed_concerts.venue_id IS 'Resolved venue of the deleted event. Intentionally not a foreign key so the suppression survives independently of venue lifecycle.';
COMMENT ON COLUMN suppressed_concerts.local_event_date IS 'Local calendar date of the deleted event.';
COMMENT ON COLUMN suppressed_concerts.start_at IS 'Start time of the deleted event. NULL when unknown; NULLS NOT DISTINCT collapses an unknown-start slot like the events unique key.';
COMMENT ON COLUMN suppressed_concerts.suppressed_at IS 'Timestamp when the concert was suppressed (i.e. deleted by an operator).';

-- ============================================================
-- Indexes
-- ============================================================

-- Users indexes
CREATE INDEX IF NOT EXISTS idx_users_provider_user_id ON users(provider_user_id);
COMMENT ON INDEX idx_users_provider_user_id IS 'Speeds up user lookup by the identity provider''s ID (Zitadel sub claim)';

CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
COMMENT ON INDEX idx_users_email IS 'Speeds up user lookup by email during authentication';

-- Artists indexes
CREATE INDEX IF NOT EXISTS idx_artists_name ON artists(name);
COMMENT ON INDEX idx_artists_name IS 'Speeds up artist search by name';

-- Artist official site indexes
CREATE INDEX IF NOT EXISTS idx_artist_official_site_artist_id ON artist_official_site(artist_id);
COMMENT ON INDEX idx_artist_official_site_artist_id IS 'Optimizes retrieval of official site for an artist';

-- Venues indexes
CREATE UNIQUE INDEX IF NOT EXISTS idx_venues_google_place_id ON venues (google_place_id) WHERE google_place_id IS NOT NULL;
COMMENT ON INDEX idx_venues_google_place_id IS 'Ensures uniqueness of Google Maps Place ID across venue records';

-- Series indexes (first-party authoring)
CREATE INDEX IF NOT EXISTS idx_series_organizer_id ON series(organizer_id) WHERE organizer_id IS NOT NULL;
COMMENT ON INDEX idx_series_organizer_id IS 'Optimizes listing an organizer own authored series (List) and ownership checks';

CREATE INDEX IF NOT EXISTS idx_series_first_party_state ON series(publish_state, visibility) WHERE organizer_id IS NOT NULL;
COMMENT ON INDEX idx_series_first_party_state IS 'Optimizes publish-state / visibility filtering for the shared guard that excludes DRAFT / UNLISTED / CANCELLED series from public and follower surfaces';

CREATE UNIQUE INDEX IF NOT EXISTS uq_series_unlisted_token ON series(unlisted_token) WHERE unlisted_token IS NOT NULL;
COMMENT ON INDEX uq_series_unlisted_token IS 'Resolves an UNLISTED series from its share token on the public read path and enforces token uniqueness';

-- Events indexes
CREATE INDEX IF NOT EXISTS idx_events_local_event_date ON events(local_event_date);
COMMENT ON INDEX idx_events_local_event_date IS 'Speeds up date-based event searches and calendar views';

CREATE INDEX IF NOT EXISTS idx_events_venue_id ON events(venue_id);
COMMENT ON INDEX idx_events_venue_id IS 'Optimizes listing events by venue';

CREATE INDEX IF NOT EXISTS idx_events_series_id ON events(series_id);
COMMENT ON INDEX idx_events_series_id IS 'Optimizes listing all events belonging to a series';

-- Concert artists indexes
CREATE INDEX IF NOT EXISTS idx_concert_artists_artist_id ON concert_artists(artist_id);
COMMENT ON INDEX idx_concert_artists_artist_id IS 'Optimizes lookup of all events for a given artist (reverse direction of the composite PK)';

-- Followed artists indexes
CREATE INDEX IF NOT EXISTS idx_followed_artists_user_id ON followed_artists(user_id);
COMMENT ON INDEX idx_followed_artists_user_id IS 'Optimizes retrieval of all followed artists for a user';

CREATE INDEX IF NOT EXISTS idx_followed_artists_artist_id ON followed_artists(artist_id);
COMMENT ON INDEX idx_followed_artists_artist_id IS 'Optimizes finding all followers of an artist';

-- Ticket journeys indexes
CREATE INDEX IF NOT EXISTS idx_ticket_journeys_event_id ON ticket_journeys(event_id);

-- Push subscriptions indexes
CREATE INDEX IF NOT EXISTS idx_push_subscriptions_user_id ON push_subscriptions(user_id);

-- Notifications indexes
CREATE INDEX IF NOT EXISTS idx_notifications_user_queued ON notifications(user_id, queued_at DESC);
COMMENT ON INDEX idx_notifications_user_queued IS 'Optimizes the inbox query: a user''s notifications most-recent-first';

-- Sales phases indexes
CREATE INDEX IF NOT EXISTS idx_sales_phases_series_id ON sales_phases(series_id);
COMMENT ON INDEX idx_sales_phases_series_id IS 'Optimizes listing all sales phases for a series';

CREATE INDEX IF NOT EXISTS idx_sales_phases_apply_start_at ON sales_phases(apply_start_at);
COMMENT ON INDEX idx_sales_phases_apply_start_at IS 'Supports ListUpcomingByDueWindow queries that filter by apply_start_at range';

-- Sales phase reminders indexes
CREATE INDEX IF NOT EXISTS idx_sales_phase_reminders_user_id ON sales_phase_reminders(user_id);
COMMENT ON INDEX idx_sales_phase_reminders_user_id IS 'Optimizes lookup of all reminders for a user';

CREATE INDEX IF NOT EXISTS idx_sales_phase_reminders_sales_phase_id ON sales_phase_reminders(sales_phase_id);
COMMENT ON INDEX idx_sales_phase_reminders_sales_phase_id IS 'Optimizes lookup of all reminder records for a sales phase';

-- Concert candidates indexes
-- Two partial unique indexes form the NULL-safe natural key: when the venue
-- resolved, dedup on the canonical place id; otherwise fall back to the raw
-- listed venue name. They never overlap because the predicates are mutually
-- exclusive on resolved_place_id IS [NOT] NULL.
CREATE UNIQUE INDEX IF NOT EXISTS uq_concert_candidates_by_place ON concert_candidates(artist_id, local_date, resolved_place_id) WHERE resolved_place_id IS NOT NULL;
COMMENT ON INDEX uq_concert_candidates_by_place IS 'Natural-key dedup for resolved venues: one pending row per (artist, date, place id).';

CREATE UNIQUE INDEX IF NOT EXISTS uq_concert_candidates_by_listed_name ON concert_candidates(artist_id, local_date, listed_venue_name) WHERE resolved_place_id IS NULL;
COMMENT ON INDEX uq_concert_candidates_by_listed_name IS 'Natural-key dedup fallback when the venue did not resolve: one pending row per (artist, date, raw listed name).';

CREATE INDEX IF NOT EXISTS idx_concert_candidates_discovered_at ON concert_candidates(discovered_at);
COMMENT ON INDEX idx_concert_candidates_discovered_at IS 'Orders the review queue by discovery time';

-- Rejected concerts indexes
CREATE INDEX IF NOT EXISTS idx_rejected_concerts_artist_id ON rejected_concerts(artist_id);
COMMENT ON INDEX idx_rejected_concerts_artist_id IS 'Supports per-artist analysis of repeated rejection patterns';

CREATE INDEX IF NOT EXISTS idx_rejected_concerts_rejected_at ON rejected_concerts(rejected_at);
COMMENT ON INDEX idx_rejected_concerts_rejected_at IS 'Supports time-windowed analysis of rejections';

-- Organizers: the vetted business an admin creates to represent artists. Existence
-- is the vetting (no verified flag). status is the operational lifecycle
-- (1=provisioning, 2=active, 3=deactivated); provider_organizer_id links the token
-- tenant to this row and is set once tenant provisioning completes.
CREATE TABLE IF NOT EXISTS organizers (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    operator_email TEXT NOT NULL,
    provider_organizer_id TEXT,
    status SMALLINT NOT NULL,
    legal_name TEXT,
    representative_name TEXT,
    business_address TEXT,
    business_phone_number TEXT,
    contact_email TEXT,
    platform_fee_rate_bps INTEGER NOT NULL DEFAULT 800,
    CONSTRAINT chk_organizers_name_non_empty CHECK (name <> ''),
    CONSTRAINT chk_organizers_operator_email_non_empty CHECK (operator_email <> ''),
    CONSTRAINT chk_organizers_status CHECK (status BETWEEN 1 AND 3),
    CONSTRAINT chk_organizers_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_organizers_platform_fee_rate_bps CHECK (platform_fee_rate_bps BETWEEN 0 AND 3000),
    CONSTRAINT chk_organizers_business_details_together CHECK ((legal_name IS NULL AND representative_name IS NULL AND business_address IS NULL AND business_phone_number IS NULL AND contact_email IS NULL) OR (legal_name IS NOT NULL AND representative_name IS NOT NULL AND business_address IS NOT NULL AND business_phone_number IS NOT NULL AND contact_email IS NOT NULL)),
    CONSTRAINT chk_organizers_business_phone_e164 CHECK (business_phone_number IS NULL OR business_phone_number ~ '^\+[1-9][0-9]{1,14}$')
);
COMMENT ON TABLE organizers IS 'Vetted businesses (label / agency / promoter / self-publishing artist) that represent artists and sell their tickets';
COMMENT ON COLUMN organizers.id IS 'Unique organizer identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN organizers.name IS 'Organizer display name (label / agency / promoter / self-publishing artist)';
COMMENT ON COLUMN organizers.operator_email IS 'Email of the operator who administers this organizer; seeded as the initial Zitadel owner user';
COMMENT ON COLUMN organizers.provider_organizer_id IS 'The identity provider''s ID for the Organizer (Zitadel tenant organization ID); NULL until provisioning completes';
COMMENT ON COLUMN organizers.status IS 'Lifecycle state: 1=provisioning, 2=active, 3=deactivated';
COMMENT ON COLUMN organizers.legal_name IS '特商法 販売業者 legal name (1-200 characters); NULL until an admin enters the business details';
COMMENT ON COLUMN organizers.representative_name IS '特商法 representative or responsible person (1-100 characters); NULL until entered';
COMMENT ON COLUMN organizers.business_address IS '特商法 business address (1-300 characters); NULL until entered';
COMMENT ON COLUMN organizers.business_phone_number IS '特商法 business phone number in E.164 form; NULL until entered';
COMMENT ON COLUMN organizers.contact_email IS '特商法 contact email; NULL until entered';
COMMENT ON COLUMN organizers.platform_fee_rate_bps IS 'Platform fee applied to future orders, in basis points (0-3000). 800 for a new organizer; organizers that existed before per-organizer rates keep 500.';

CREATE UNIQUE INDEX IF NOT EXISTS uq_organizers_provider_organizer_id ON organizers(provider_organizer_id) WHERE provider_organizer_id IS NOT NULL;
COMMENT ON INDEX uq_organizers_provider_organizer_id IS 'One Zitadel tenant org maps to at most one Organizer; NULL while provisioning';

-- Organizer-artist association: an organizer represents many artists, and each
-- artist is represented by at most one organizer (uq_organizer_artists_artist_id).
-- Deactivation and disassociation delete rows, freeing the artist for re-association.
CREATE TABLE IF NOT EXISTS organizer_artists (
    organizer_id UUID NOT NULL REFERENCES organizers(id) ON DELETE CASCADE,
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    PRIMARY KEY (organizer_id, artist_id)
);
COMMENT ON TABLE organizer_artists IS 'Links an organizer to the artists it represents';
COMMENT ON COLUMN organizer_artists.organizer_id IS 'Organizer that represents the artist';
COMMENT ON COLUMN organizer_artists.artist_id IS 'Artist represented by the organizer';

CREATE UNIQUE INDEX IF NOT EXISTS uq_organizer_artists_artist_id ON organizer_artists(artist_id);
COMMENT ON INDEX uq_organizer_artists_artist_id IS 'Each artist is represented by at most one organizer';

CREATE INDEX IF NOT EXISTS idx_organizer_artists_organizer_id ON organizer_artists(organizer_id);
COMMENT ON INDEX idx_organizer_artists_organizer_id IS 'Optimizes listing the artists an organizer represents';

-- Identity verifications table
-- The result of a fan's 本人確認 (identity check): binds one account (users
-- row) to a verified real person via JPKI or the driver's-licence fallback
-- (Pocket Sign Verify). The person key is the Pocket Sign tenant-scoped
-- User.id (provider_user_id). No 基本4情報, no raw certificate, no 個人番号
-- is ever stored here — only the result.
-- The partial unique index (uq_identity_verifications_active_provider_user_id)
-- enforces the invariant: at most one row with status='ACTIVE' per provider_user_id.
CREATE TABLE IF NOT EXISTS identity_verifications (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    method SMALLINT NOT NULL,
    provider_user_id TEXT NOT NULL,
    dedupe_strength SMALLINT NOT NULL,
    verify_at TIMESTAMPTZ NOT NULL,
    status SMALLINT NOT NULL,
    CONSTRAINT chk_identity_verifications_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_identity_verifications_method CHECK (method BETWEEN 1 AND 2),
    CONSTRAINT chk_identity_verifications_dedupe_strength CHECK (dedupe_strength BETWEEN 1 AND 2),
    CONSTRAINT chk_identity_verifications_status CHECK (status BETWEEN 1 AND 2),
    CONSTRAINT chk_identity_verifications_provider_user_id_non_empty CHECK (provider_user_id <> '')
);

COMMENT ON TABLE identity_verifications IS 'Account-level 本人確認 (IdentityVerification) results. One row per (user, verification attempt); the active invariant (at most one ACTIVE row per provider_user_id) is enforced by the partial unique index.';
COMMENT ON COLUMN identity_verifications.id IS 'Unique IdentityVerification identifier (UUIDv7, application-generated).';
COMMENT ON COLUMN identity_verifications.user_id IS 'Reference to the user account this IdentityVerification is bound to.';
COMMENT ON COLUMN identity_verifications.method IS 'Verification method: 1=JPKI, 2=DRIVER_LICENCE.';
COMMENT ON COLUMN identity_verifications.provider_user_id IS 'The verification provider''s ID for the user (Pocket Sign tenant-scoped person key, User.id). The only person-identifying datum stored; never the serial, never the 個人番号.';
COMMENT ON COLUMN identity_verifications.dedupe_strength IS 'Dedupe guarantee strength: 1=STRONG (JPKI, stable per-person key), 2=WEAK (driver licence, document-scoped key).';
COMMENT ON COLUMN identity_verifications.verify_at IS 'Timestamp when the verification was established.';
COMMENT ON COLUMN identity_verifications.status IS 'Freshness lifecycle: 1=ACTIVE (current), 2=NEEDS_REVERIFICATION (revoked/changed/expired per 現況確認).';

-- Partial unique index enforces the 1-person-1-active-account invariant.
CREATE UNIQUE INDEX IF NOT EXISTS uq_identity_verifications_active_provider_user_id
    ON identity_verifications(provider_user_id) WHERE status = 1;
COMMENT ON INDEX uq_identity_verifications_active_provider_user_id IS 'At most one ACTIVE IdentityVerification per verification provider person key. A second verified account for the same person is rejected here.';

CREATE INDEX IF NOT EXISTS idx_identity_verifications_user_id ON identity_verifications(user_id);
COMMENT ON INDEX idx_identity_verifications_user_id IS 'Optimizes lookup of a user''s active IdentityVerification.';

-- ============================================================
-- Ticket sales (organizer-authored first-come and lottery sales)
-- ============================================================

-- Ticket sales: one sale opportunity (受付) over the events of one Series: a
-- name, an allocation method, a window and a verification requirement. What it
-- offers for each event is a ticket_types row.
CREATE TABLE IF NOT EXISTS ticket_sales (
    id                       UUID        PRIMARY KEY,
    series_id                UUID        NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    name                     TEXT        NOT NULL,
    method                   SMALLINT    NOT NULL,
    sale_start_at            TIMESTAMPTZ NOT NULL,
    sale_end_at              TIMESTAMPTZ NOT NULL,
    verification_requirement SMALLINT    NOT NULL DEFAULT 0,
    draw_at                  TIMESTAMPTZ,
    CONSTRAINT chk_ticket_sales_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_ticket_sales_name_len CHECK (char_length(name) BETWEEN 1 AND 100),
    CONSTRAINT chk_ticket_sales_method CHECK (method IN (1, 2)),
    CONSTRAINT chk_ticket_sales_window CHECK (sale_end_at > sale_start_at),
    CONSTRAINT chk_ticket_sales_verification_requirement CHECK (verification_requirement BETWEEN 0 AND 2),
    CONSTRAINT chk_ticket_sales_draw_lottery_only CHECK (draw_at IS NULL OR method = 2)
);

COMMENT ON TABLE ticket_sales IS 'One sale opportunity (受付) over the events of one Series: a name, an allocation method, a window and a verification requirement. Its per-event price, quantity and limit are ticket_types rows.';
COMMENT ON COLUMN ticket_sales.id IS 'Unique ticket sale identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN ticket_sales.series_id IS 'The Series whose events the sale offers tickets for';
COMMENT ON COLUMN ticket_sales.name IS 'Sale name shown to fans and operators (1-100 characters), e.g. ファンクラブ先行 or 一般発売';
COMMENT ON COLUMN ticket_sales.method IS 'Allocation method: 1=FirstCome, 2=Lottery';
COMMENT ON COLUMN ticket_sales.sale_start_at IS 'When the sale opens';
COMMENT ON COLUMN ticket_sales.sale_end_at IS 'When the sale closes; after sale_start_at, 1 to 14 days after it for a lottery, and before the start of every event the sale covers (checked by the application)';
COMMENT ON COLUMN ticket_sales.verification_requirement IS '本人確認 requirement: 0=NONE (any fan), 1=VERIFIED_ANY (an Active IdentityVerification is required), 2=JPKI_ONLY (a JPKI-backed IdentityVerification is required)';
COMMENT ON COLUMN ticket_sales.draw_at IS 'When the lottery draw ran for every ticket type of the sale; NULL until the draw runs and for a first-come sale. Set atomically with the draw outcome; the draw sweeper skips sales where it is set.';

CREATE INDEX IF NOT EXISTS idx_ticket_sales_series_id ON ticket_sales(series_id);
COMMENT ON INDEX idx_ticket_sales_series_id IS 'Optimizes listing the sales of a Series';

-- Ticket types: what one ticket sale offers for one event. For a first-come
-- sale, sold_count holds the tickets of the type's Committed and Completed
-- checkouts; held tickets are derived from the Held checkouts whose hold has
-- not expired.
CREATE TABLE IF NOT EXISTS ticket_types (
    id                UUID    PRIMARY KEY,
    ticket_sale_id    UUID    NOT NULL REFERENCES ticket_sales(id) ON DELETE CASCADE,
    event_id          UUID    NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    price             BIGINT  NOT NULL,
    quantity          INTEGER NOT NULL,
    per_account_limit INTEGER NOT NULL,
    sold_count        INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT chk_ticket_types_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_ticket_types_price CHECK (price BETWEEN 1 AND 1000000),
    CONSTRAINT chk_ticket_types_quantity CHECK (quantity >= 1),
    CONSTRAINT chk_ticket_types_per_account_limit CHECK (per_account_limit BETWEEN 1 AND 10 AND per_account_limit <= quantity),
    CONSTRAINT chk_ticket_types_sold_count CHECK (sold_count BETWEEN 0 AND quantity),
    CONSTRAINT uq_ticket_types_sale_event UNIQUE (ticket_sale_id, event_id)
);

COMMENT ON TABLE ticket_types IS 'What one ticket sale offers for one event: a quantity at one tax-inclusive price with a per-account limit, and for a first-come sale the tickets sold. One per (sale, event).';
COMMENT ON COLUMN ticket_types.id IS 'Unique ticket type identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN ticket_types.ticket_sale_id IS 'The sale that offers the ticket type';
COMMENT ON COLUMN ticket_types.event_id IS 'The event the tickets admit to; an event of the sale''s Series (checked by the application)';
COMMENT ON COLUMN ticket_types.price IS 'Price of one ticket in yen, tax-inclusive (税込); for a first-come sale fixed while any ticket is sold or held';
COMMENT ON COLUMN ticket_types.quantity IS 'Tickets offered; for a first-come sale it can shrink only down to sold plus held';
COMMENT ON COLUMN ticket_types.per_account_limit IS 'Most tickets one account may hold for the event once this sale is done, counting tickets it already holds; 1 to 10 and at most the quantity';
COMMENT ON COLUMN ticket_types.sold_count IS 'Tickets on Committed and Completed checkouts (first-come only; stays 0 for a lottery); changed together with the checkout status under the ticket type row lock';

CREATE INDEX IF NOT EXISTS idx_ticket_types_event_id ON ticket_types(event_id);
COMMENT ON INDEX idx_ticket_types_event_id IS 'Optimizes listing the sales of an event through its ticket types';

-- Lottery entries: a fan's entry into the lottery for one ticket type. One row
-- per attempt; a new entry after withdrawal creates a fresh row. The partial
-- unique index enforces at most one active (non-withdrawn) entry per
-- (ticket_type_id, user_id).
CREATE TABLE IF NOT EXISTS lottery_entries (
    id                     UUID     PRIMARY KEY,
    ticket_type_id         UUID     NOT NULL REFERENCES ticket_types(id) ON DELETE CASCADE,
    user_id                UUID     NOT NULL,
    ticket_count           INT      NOT NULL,
    card_authorization_id  TEXT,
    state                  SMALLINT NOT NULL,
    draw_position          BIGINT,
    CONSTRAINT chk_lottery_entries_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_lottery_entries_ticket_count_positive CHECK (ticket_count > 0),
    CONSTRAINT chk_lottery_entries_state CHECK (state BETWEEN 1 AND 5),
    CONSTRAINT chk_lottery_entries_card_authorization_id_not_empty CHECK (card_authorization_id IS NULL OR card_authorization_id <> ''),
    CONSTRAINT chk_lottery_entries_authorized_from_entered CHECK (card_authorization_id IS NOT NULL OR state IN (4, 5))
);

COMMENT ON TABLE lottery_entries IS 'Fan entries into the lottery of a ticket type. One row per attempt; a new entry after withdrawal creates a fresh row. State: 1=Entered, 2=Won, 3=Lost, 4=Withdrawn, 5=Draft.';
COMMENT ON COLUMN lottery_entries.id IS 'Unique lottery entry identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN lottery_entries.ticket_type_id IS 'The ticket type entered; its sale''s method is Lottery';
COMMENT ON COLUMN lottery_entries.user_id IS 'The fan user ID (references users.id at application layer; no FK to survive user lifecycle independently)';
COMMENT ON COLUMN lottery_entries.ticket_count IS 'Companion-group size (all-or-nothing allocation). Must be positive.';
COMMENT ON COLUMN lottery_entries.card_authorization_id IS 'The payment provider''s ID for the card authorization (Stripe pi_...); NULL on a Draft until the card authorization is opened, and on an entry withdrawn as a Draft; required from Entered on';
COMMENT ON COLUMN lottery_entries.state IS 'Lifecycle state: 1=Entered (card authorization in place), 2=Won (captured), 3=Lost (card authorization released), 4=Withdrawn (fan-cancelled or abandoned Draft, card authorization released), 5=Draft (before confirmation; takes no part in the draw)';
COMMENT ON COLUMN lottery_entries.draw_position IS 'Zero-based position in the ticket type''s draw shuffle. NULL until the draw runs; the draw order of the losers for official resale.';

-- Partial unique index: at most one active entry per (ticket_type_id, user_id).
-- "Active" means every state but Withdrawn (4): Draft, Entered, Won and Lost.
-- A Withdrawn row is excluded so the fan can enter again after withdrawal.
-- Won and Lost are included because they represent a concluded draw outcome
-- that still uniquely identifies this fan's participation.
CREATE UNIQUE INDEX IF NOT EXISTS uq_lottery_entries_active
    ON lottery_entries(ticket_type_id, user_id)
    WHERE state <> 4;

COMMENT ON INDEX uq_lottery_entries_active IS 'At most one active (Draft/Entered/Won/Lost) entry per (ticket type, user). Withdrawn (4) is excluded so the fan can enter again. Won/Lost are included: a concluded draw outcome still uniquely identifies participation.';

CREATE INDEX IF NOT EXISTS idx_lottery_entries_ticket_type_id ON lottery_entries(ticket_type_id);
COMMENT ON INDEX idx_lottery_entries_ticket_type_id IS 'Optimizes listing all entries for a given ticket type (draw batch load)';

-- Checkouts: one fan's first-come purchase in progress on a ticket type. A
-- hold lasts exactly 15 minutes and is never extended. A charged checkout
-- (capture_at set) is never Expired or Released.
CREATE TABLE IF NOT EXISTS checkouts (
    id                        UUID        PRIMARY KEY,
    ticket_type_id            UUID        NOT NULL REFERENCES ticket_types(id) ON DELETE RESTRICT,
    user_id                   UUID        NOT NULL,
    ticket_count              INTEGER     NOT NULL,
    amount                    BIGINT      NOT NULL,
    card_authorization_id     TEXT,
    card_authorization_release_at TIMESTAMPTZ,
    status                    SMALLINT    NOT NULL,
    hold_expire_at            TIMESTAMPTZ NOT NULL,
    commit_at                 TIMESTAMPTZ,
    capture_at                TIMESTAMPTZ,
    CONSTRAINT chk_checkouts_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_checkouts_ticket_count CHECK (ticket_count BETWEEN 1 AND 10),
    CONSTRAINT chk_checkouts_amount_positive CHECK (amount > 0),
    CONSTRAINT chk_checkouts_status CHECK (status BETWEEN 1 AND 5),
    CONSTRAINT chk_checkouts_card_authorization_id_not_empty CHECK (card_authorization_id IS NULL OR card_authorization_id <> ''),
    CONSTRAINT chk_checkouts_release_needs_card_authorization CHECK (card_authorization_release_at IS NULL OR card_authorization_id IS NOT NULL),
    CONSTRAINT chk_checkouts_commit_time CHECK (status NOT IN (2, 3) OR commit_at IS NOT NULL),
    CONSTRAINT chk_checkouts_charged_never_ended CHECK (capture_at IS NULL OR status IN (2, 3)),
    CONSTRAINT chk_checkouts_completed_charged CHECK (status <> 3 OR capture_at IS NOT NULL)
);

COMMENT ON TABLE checkouts IS 'One fan''s first-come purchase in progress on a ticket type: holds a count of tickets for 15 minutes, then is committed, charged and completed, or expires or is released. Status 1=Held, 2=Committed, 3=Completed, 4=Expired, 5=Released.';
COMMENT ON COLUMN checkouts.id IS 'Unique checkout identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN checkouts.ticket_type_id IS 'The ticket type the checkout buys from; its sale''s method is FirstCome';
COMMENT ON COLUMN checkouts.user_id IS 'The fan checking out (no FK to survive user lifecycle independently)';
COMMENT ON COLUMN checkouts.ticket_count IS 'Tickets held, 1 to the ticket type''s per-account limit';
COMMENT ON COLUMN checkouts.amount IS 'Total to pay in yen: the ticket type price times ticket_count, fixed at creation';
COMMENT ON COLUMN checkouts.card_authorization_id IS 'The payment provider''s ID for the card authorization (Stripe pi_...); NULL until the fan authorizes, then set once';
COMMENT ON COLUMN checkouts.card_authorization_release_at IS 'When the card authorization was released without a charge; NULL unless released';
COMMENT ON COLUMN checkouts.status IS 'Lifecycle: 1=Held, 2=Committed, 3=Completed, 4=Expired, 5=Released';
COMMENT ON COLUMN checkouts.hold_expire_at IS 'When the hold lapses: 15 minutes after the checkout started, never extended; a commit needs hold_expire_at > now';
COMMENT ON COLUMN checkouts.commit_at IS 'When the tickets were committed against the stock; kept when an uncharged commit is reverted';
COMMENT ON COLUMN checkouts.capture_at IS 'When the card authorization (card_authorization_id) was captured; set once, and a row with it is never Expired or Released';

CREATE UNIQUE INDEX IF NOT EXISTS uq_checkouts_held ON checkouts(ticket_type_id, user_id) WHERE status = 1;
COMMENT ON INDEX uq_checkouts_held IS 'At most one Held checkout per (ticket type, user): a concurrent second insert loses and reads the winner';

CREATE INDEX IF NOT EXISTS idx_checkouts_ticket_type_status ON checkouts(ticket_type_id, status);
COMMENT ON INDEX idx_checkouts_ticket_type_status IS 'Optimizes the held and per-user committed counts of a ticket type';

CREATE INDEX IF NOT EXISTS idx_checkouts_due ON checkouts(status, hold_expire_at) WHERE status IN (1, 2) OR (status IN (4, 5) AND card_authorization_id IS NOT NULL AND card_authorization_release_at IS NULL);
COMMENT ON INDEX idx_checkouts_due IS 'Optimizes ListDue: lapsed holds, card authorizations to release and stalled commits';

-- Orders: the ⑤ purchase record for one won lottery entry or one completed
-- checkout. Created already paid from the captured payment (no pending state).
-- Stores only opaque provider references and display facets — never
-- PAN/CVC/expiry (PCI SAQ A).
CREATE TABLE IF NOT EXISTS orders (
    id                 UUID    PRIMARY KEY,
    user_id            UUID    NOT NULL,
    lottery_entry_id   UUID    REFERENCES lottery_entries(id) ON DELETE RESTRICT,
    provider           SMALLINT NOT NULL,
    provider_payment_id TEXT   NOT NULL,
    provider_payment_method_id TEXT NOT NULL DEFAULT '',
    card_brand         TEXT    NOT NULL DEFAULT '',
    card_last4         TEXT    NOT NULL DEFAULT '',
    status             SMALLINT NOT NULL,
    amount             BIGINT  NOT NULL,
    currency           TEXT    NOT NULL,
    pay_at             TIMESTAMPTZ NOT NULL,
    provider_refund_id TEXT    NOT NULL DEFAULT '',
    checkout_id        UUID    REFERENCES checkouts(id) ON DELETE RESTRICT,
    confirmation_send_at TIMESTAMPTZ,
    CONSTRAINT chk_orders_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_orders_provider CHECK (provider IN (1, 2)),
    CONSTRAINT chk_orders_status CHECK (status IN (1, 2, 3)),
    CONSTRAINT chk_orders_amount_positive CHECK (amount > 0),
    CONSTRAINT chk_orders_currency_len CHECK (char_length(currency) = 3),
    CONSTRAINT chk_orders_one_source CHECK ((lottery_entry_id IS NULL) <> (checkout_id IS NULL))
);

COMMENT ON TABLE orders IS 'Purchase record for one won lottery entry or one completed checkout. Created already paid from the captured payment (status 1=Paid, 2=Refunded, 3=Failed; no pending). One order covers all tickets of its source.';
COMMENT ON COLUMN orders.id IS 'Unique order identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN orders.user_id IS 'The fan who bought the tickets (no FK to survive user lifecycle independently)';
COMMENT ON COLUMN orders.lottery_entry_id IS 'The Won-captured lottery entry this order derives from; NULL when the source is a checkout; unique (one order per entry)';
COMMENT ON COLUMN orders.provider IS 'Payment provider: 1=Stripe, 2=KOMOJU';
COMMENT ON COLUMN orders.provider_payment_id IS 'The payment provider''s ID for the captured payment (e.g. Stripe pi_...)';
COMMENT ON COLUMN orders.provider_payment_method_id IS 'The payment provider''s ID for the payment method (e.g. Stripe pm_...), optional';
COMMENT ON COLUMN orders.card_brand IS 'Display-only card brand facet (e.g. visa); never the PAN';
COMMENT ON COLUMN orders.card_last4 IS 'Display-only last four digits; never the full PAN';
COMMENT ON COLUMN orders.status IS 'Order status: 1=Paid, 2=Refunded, 3=Failed (capture-succeeded-but-issuance-refunded edge)';
COMMENT ON COLUMN orders.amount IS 'Total captured amount in the currency smallest unit (yen total for JPY)';
COMMENT ON COLUMN orders.currency IS 'ISO 4217 currency code of amount (JPY for the MVP)';
COMMENT ON COLUMN orders.pay_at IS 'When the order was recorded as paid (after the draw''s capture or the checkout''s charge)';
COMMENT ON COLUMN orders.checkout_id IS 'The Committed, charged checkout this order completes; NULL when the source is a lottery entry; unique (one order per checkout)';
COMMENT ON COLUMN orders.confirmation_send_at IS 'When the purchase confirmation email was sent; NULL until sent. Set right after a successful send so a redelivered event sends nothing.';
COMMENT ON COLUMN orders.provider_refund_id IS 'The payment provider''s ID for the refund (e.g. Stripe "re_...") set when the order is refunded via CANCELLATION. Empty for DISPUTE reason (the chargeback already reversed the charge at the card network) and for orders that are not yet refunded.';

CREATE UNIQUE INDEX IF NOT EXISTS uq_orders_lottery_entry_id ON orders(lottery_entry_id);
COMMENT ON INDEX uq_orders_lottery_entry_id IS 'One order per winning lottery entry — the issuance idempotency guard (a duplicate insert raises unique_violation, surfaced as AlreadyExists).';

CREATE UNIQUE INDEX IF NOT EXISTS uq_orders_checkout_id ON orders(checkout_id);
COMMENT ON INDEX uq_orders_checkout_id IS 'One order per checkout — the issuance idempotency guard for checkouts (a duplicate insert raises unique_violation, surfaced as AlreadyExists).';

CREATE INDEX IF NOT EXISTS idx_orders_user_id ON orders(user_id);
COMMENT ON INDEX idx_orders_user_id IS 'Optimizes listing a user''s orders';

-- Tickets: account-bound covered tickets (特定興行入場券) issued from a won
-- lottery entry or a completed checkout. One order issues the N tickets of its
-- companion group. The face shows the user's current full name and phone
-- number (users row), not a copy.
CREATE TABLE IF NOT EXISTS tickets (
    id                                UUID    PRIMARY KEY,
    order_id                          UUID    NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
    user_id                           UUID    NOT NULL,
    event_id                          UUID    NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    identity_verification_id          UUID,
    resale_without_consent_prohibited BOOLEAN NOT NULL DEFAULT TRUE,
    status                            SMALLINT NOT NULL,
    issue_at                          TIMESTAMPTZ NOT NULL,
    admit_at                          TIMESTAMPTZ,
    CONSTRAINT chk_tickets_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_tickets_status CHECK (status IN (1, 2)),
    CONSTRAINT chk_tickets_resale_prohibited CHECK (resale_without_consent_prohibited = TRUE)
);

COMMENT ON TABLE tickets IS 'Account-bound covered tickets (特定興行入場券) issued from a won lottery entry or a completed checkout. Each carries the three covered-ticket conditions: resale-without-consent prohibited, date/venue+eligible-person (event_id + user_id, whose current full name and phone number the face shows), and bound 本人確認.';
COMMENT ON COLUMN tickets.id IS 'Unique ticket identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN tickets.order_id IS 'The order that issued this ticket';
COMMENT ON COLUMN tickets.user_id IS 'The account the ticket is bound to; never changes (official resale voids the ticket and issues a new one)';
COMMENT ON COLUMN tickets.event_id IS 'The event this ticket admits to (supplies the covered-ticket face date/venue)';
COMMENT ON COLUMN tickets.identity_verification_id IS 'The IdentityVerification the ticket is bound to when its sale required verification; NULL otherwise. No FK so privacy deletion of an IdentityVerification is independent.';
COMMENT ON COLUMN tickets.resale_without_consent_prohibited IS 'Covered-ticket condition (i): always true (enforced by CHECK) so every issued ticket qualifies as a 特定興行入場券';
COMMENT ON COLUMN tickets.status IS 'Ticket status: 1=Issued, 2=Voided (on refund)';
COMMENT ON COLUMN tickets.issue_at IS 'When the ticket was issued (= the order capture/issuance time)';
COMMENT ON COLUMN tickets.admit_at IS 'When the ticket was admitted at the venue; NULL until admitted. Set once together with its admissions row (conditional update on this row) and kept when the ticket is later voided.';

CREATE INDEX IF NOT EXISTS idx_tickets_order_id ON tickets(order_id);
COMMENT ON INDEX idx_tickets_order_id IS 'Optimizes loading the tickets of an order';

CREATE INDEX IF NOT EXISTS idx_tickets_user_id ON tickets(user_id);
COMMENT ON INDEX idx_tickets_user_id IS 'Optimizes a user''s my-tickets view';

CREATE INDEX IF NOT EXISTS idx_tickets_event_id ON tickets(event_id);
COMMENT ON INDEX idx_tickets_event_id IS 'Optimizes per-event ticket lookups (check-in, cancellation)';

-- ============================================================
-- Settlement / payout tables (ticket-settlement-and-payout)
-- ============================================================

-- Payout accounts: the payment provider's ID for each Organizer's
-- payout account at the payment provider (Stripe). One row per
-- Organizer; the platform provisions the account as a recipient (transfers
-- capability only, never card_payments) with losses_collector = application
-- so the platform absorbs negative balances.
CREATE TABLE IF NOT EXISTS payout_accounts (
    organizer_id    UUID    PRIMARY KEY REFERENCES organizers(id) ON DELETE CASCADE,
    provider_account_id TEXT NOT NULL,
    status          SMALLINT NOT NULL,
    provisioned_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status_synced_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_payout_accounts_provider_account_id_not_empty CHECK (provider_account_id <> ''),
    CONSTRAINT chk_payout_accounts_status CHECK (status IN (1, 2, 3))
);

COMMENT ON TABLE payout_accounts IS 'The Organizer''s payout account at the payment provider (for example a Stripe account, Accounts v2, transfers capability only). One row per Organizer. status: 1=Pending (payout account verification in progress), 2=Active (eligible for payout), 3=Restricted (capability disabled).';
COMMENT ON COLUMN payout_accounts.organizer_id IS 'The Organizer that owns this payout-recipient account (1:1 with organizers)';
COMMENT ON COLUMN payout_accounts.provider_account_id IS 'The payment provider''s ID for the payout account (e.g. Stripe "acct_..."); meaningful only to the provider';
COMMENT ON COLUMN payout_accounts.status IS 'Payout-onboarding readiness: 1=Pending, 2=Active, 3=Restricted. Eligibility = status 2 only.';
COMMENT ON COLUMN payout_accounts.provisioned_at IS 'When the payout account was first provisioned at the payment provider';
COMMENT ON COLUMN payout_accounts.status_synced_at IS 'When the capability status was last refreshed from the payment provider';

-- Settlements: one payout record per Order. Tracks the lifecycle of the
-- Organizer's net share (held on the platform balance until the release gate
-- passes) and the Transfer references that pay it out. The platform fee is the
-- un-transferred remainder — it is NOT a split row.
CREATE TABLE IF NOT EXISTS settlements (
    id            UUID    PRIMARY KEY,
    order_id      UUID    NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
    organizer_id  UUID    NOT NULL,
    event_id      UUID    NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    provider_charge_id TEXT,
    status        SMALLINT NOT NULL,
    release_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL,
    CONSTRAINT chk_settlements_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_settlements_status CHECK (status IN (1, 2, 3)),
    CONSTRAINT chk_settlements_provider_charge_id_not_empty CHECK (provider_charge_id IS NULL OR provider_charge_id <> '')
);

COMMENT ON TABLE settlements IS 'Payout record for one Order. Holds the captured platform charge until the release gate passes (event start_time + dispute buffer), then tracks the Transfer(s) that pay out the Organizer net share. Platform fee = un-transferred remainder (no fee split row). status: 1=Held, 2=Released, 3=Reversed.';
COMMENT ON COLUMN settlements.id IS 'Unique settlement identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN settlements.order_id IS 'The Order this settlement pays out; one settlement per order (unique index)';
COMMENT ON COLUMN settlements.organizer_id IS 'The Organizer that receives the payout. Stored denormalized so the sweeper avoids joining through the order''s lottery entry or checkout → ticket type → event.';
COMMENT ON COLUMN settlements.event_id IS 'The event this settlement gates on. Stored denormalized so the release-gate check reads events.start_at without extra joins.';
COMMENT ON COLUMN settlements.provider_charge_id IS 'The payment provider''s ID for the charge (e.g. Stripe "ch_...") resolved from the Order''s provider payment ID at payout time. NULL until resolved. Used as source_transaction on each Transfer.';
COMMENT ON COLUMN settlements.status IS 'Settlement lifecycle: 1=Held (gate not passed), 2=Released (Transfer(s) created), 3=Reversed (transfer_reversal clawback applied)';
COMMENT ON COLUMN settlements.release_at IS 'When the settlement was released (Transfer(s) created). NULL while Held.';
COMMENT ON COLUMN settlements.created_at IS 'When this settlement row was created (= Order issuance time for the initial Held row)';

CREATE UNIQUE INDEX IF NOT EXISTS uq_settlements_order_id ON settlements(order_id);
COMMENT ON INDEX uq_settlements_order_id IS 'One settlement per Order — the payout idempotency guard; a duplicate insert raises unique_violation.';

CREATE INDEX IF NOT EXISTS idx_settlements_status ON settlements(status);
COMMENT ON INDEX idx_settlements_status IS 'Optimizes the payout sweeper''s ListHeld scan (WHERE status = 1)';

CREATE INDEX IF NOT EXISTS idx_settlements_organizer_id ON settlements(organizer_id);
COMMENT ON INDEX idx_settlements_organizer_id IS 'Optimizes listing an Organizer''s settlements for the console';

-- Settlement splits: one row per payee per settlement. MVP = exactly one row
-- (the Organizer). The platform fee is the un-transferred remainder (no row).
-- Extensible to N payees (venue, artist) without a schema break.
CREATE TABLE IF NOT EXISTS settlement_splits (
    settlement_id          UUID    NOT NULL REFERENCES settlements(id) ON DELETE CASCADE,
    payee_organizer_id     UUID    NOT NULL,
    amount                 BIGINT  NOT NULL,
    provider_payout_id          TEXT,
    provider_payout_reversal_id TEXT,
    PRIMARY KEY (settlement_id, payee_organizer_id),
    CONSTRAINT chk_settlement_splits_amount_positive CHECK (amount > 0),
    CONSTRAINT chk_settlement_splits_provider_payout_id_not_empty CHECK (provider_payout_id IS NULL OR provider_payout_id <> ''),
    CONSTRAINT chk_settlement_splits_provider_payout_reversal_id_not_empty CHECK (provider_payout_reversal_id IS NULL OR provider_payout_reversal_id <> '')
);

COMMENT ON TABLE settlement_splits IS 'One payee share per settlement. MVP = one row per settlement (the Organizer). Extensible to N payees (venue, artist) by adding rows. The platform fee is the un-transferred remainder and has no row here.';
COMMENT ON COLUMN settlement_splits.settlement_id IS 'Reference to the parent settlement';
COMMENT ON COLUMN settlement_splits.payee_organizer_id IS 'Organizer that receives this split (no FK so payee lifecycle is independent of the settlement)';
COMMENT ON COLUMN settlement_splits.amount IS 'Net share in the Order currency smallest unit (whole yen for JPY). Must be positive.';
COMMENT ON COLUMN settlement_splits.provider_payout_id IS 'The payment provider''s ID for the payout (a Stripe Transfer, e.g. "tr_..."). NULL until the split is released.';
COMMENT ON COLUMN settlement_splits.provider_payout_reversal_id IS 'The payment provider''s ID for the payout reversal (a Stripe transfer reversal, e.g. "trr_..."). NULL unless reversed on a refund/dispute.';

-- Processed webhook events: idempotency table for inbound Stripe webhook
-- delivery. Each provider event id is recorded after successful processing so
-- that a duplicate delivery (Stripe retries on non-2xx or network failures)
-- is detected and skipped without re-applying the side effect.
CREATE TABLE IF NOT EXISTS processed_webhook_events (
    provider_event_id TEXT PRIMARY KEY,
    processed_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE processed_webhook_events IS 'Idempotency guard for Stripe webhook events. Each provider event id is recorded after the event is applied; a duplicate delivery is detected via EXISTS check and skipped. Covers charge.refunded, charge.dispute.created, transfer.reversed, payout.paid, and payout.failed event types.';
COMMENT ON COLUMN processed_webhook_events.provider_event_id IS 'Stripe event id (e.g. evt_...). Primary key — unique per event.';
COMMENT ON COLUMN processed_webhook_events.processed_at IS 'Timestamp when this event was first applied.';

-- ============================================================
-- Ticket wallet and venue admission (ticket-wallet-and-checkin)
-- ============================================================

-- Wallet public keys: the public half of the key pair a fan's device created
-- for showing tickets. One row per user; registering another device's key
-- replaces the row in one upsert.
CREATE TABLE IF NOT EXISTS wallet_public_keys (
    user_id       UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    public_key    BYTEA       NOT NULL,
    registered_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT chk_wallet_public_keys_sec1_uncompressed CHECK (octet_length(public_key) = 65 AND get_byte(public_key, 0) = 4)
);

COMMENT ON TABLE wallet_public_keys IS 'Public key (ECDSA P-256) of the device a fan shows tickets from. One per user, replaced when another device registers; AdmissionCodes are verified against it.';
COMMENT ON COLUMN wallet_public_keys.user_id IS 'The user whose device created the key (one key per user)';
COMMENT ON COLUMN wallet_public_keys.public_key IS 'Uncompressed SEC1 point 0x04 || X || Y (65 bytes); validated as a P-256 curve point by the application';
COMMENT ON COLUMN wallet_public_keys.registered_at IS 'When the device registered the key';

-- Scanners: one venue-staff device's right to admit fans to one event,
-- handed over as a link that carries its link token. Numbered per event in
-- issue order, numbers never reused. The link token itself is kept only while
-- the Scanner is unused (so the console can show its link); lookups go through
-- its SHA-256.
CREATE TABLE IF NOT EXISTS scanners (
    id               UUID        PRIMARY KEY,
    event_id         UUID        NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    number           INTEGER     NOT NULL,
    link_token       TEXT,
    link_token_hash  BYTEA       NOT NULL,
    bound_public_key BYTEA,
    status           SMALLINT    NOT NULL,
    bound_at         TIMESTAMPTZ,
    revoked_at       TIMESTAMPTZ,
    CONSTRAINT chk_scanners_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_scanners_number_positive CHECK (number >= 1),
    CONSTRAINT chk_scanners_status CHECK (status IN (1, 2, 3)),
    CONSTRAINT chk_scanners_link_token_hash_len CHECK (octet_length(link_token_hash) = 32),
    CONSTRAINT chk_scanners_link_token_only_unused CHECK (link_token IS NULL OR status = 1),
    CONSTRAINT chk_scanners_bound_key_sec1 CHECK (bound_public_key IS NULL OR (octet_length(bound_public_key) = 65 AND get_byte(bound_public_key, 0) = 4)),
    CONSTRAINT chk_scanners_bound_together CHECK ((bound_public_key IS NULL) = (bound_at IS NULL)),
    CONSTRAINT chk_scanners_unused_unbound CHECK (status <> 1 OR bound_public_key IS NULL),
    CONSTRAINT chk_scanners_in_use_bound CHECK (status <> 2 OR bound_public_key IS NOT NULL),
    CONSTRAINT chk_scanners_revoked_time CHECK ((status = 3) = (revoked_at IS NOT NULL)),
    CONSTRAINT uq_scanners_event_number UNIQUE (event_id, number),
    CONSTRAINT uq_scanners_link_token_hash UNIQUE (link_token_hash)
);

COMMENT ON TABLE scanners IS 'One venue-staff device''s right, issued by the Organizer, to admit fans to one event. Status 1=Unused, 2=InUse (bound to a device public key), 3=Revoked.';
COMMENT ON COLUMN scanners.id IS 'Unique scanner identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN scanners.event_id IS 'The one event the scanner admits to';
COMMENT ON COLUMN scanners.number IS 'Number within the event in issue order (shown as 受付1, 受付2 ...); never reused, assigned under a lock on the event row';
COMMENT ON COLUMN scanners.link_token IS 'The secret part of the scanner''s link, kept only while the scanner is unused so the Organizer can copy the link; cleared once bound or revoked';
COMMENT ON COLUMN scanners.link_token_hash IS 'SHA-256 of the link token (32 bytes); the only form a link token is looked up by';
COMMENT ON COLUMN scanners.bound_public_key IS 'Uncompressed SEC1 P-256 public key of the device the scanner is bound to; set once on first open';
COMMENT ON COLUMN scanners.status IS 'Lifecycle: 1=Unused, 2=InUse, 3=Revoked';
COMMENT ON COLUMN scanners.bound_at IS 'When a device first opened the scanner''s link';
COMMENT ON COLUMN scanners.revoked_at IS 'When the Organizer revoked the scanner';

-- Admissions: the permanent note that a ticket was let in. Written only in the
-- same statement as tickets.admit_at; at most one per ticket. Append-only.
CREATE TABLE IF NOT EXISTS admissions (
    ticket_id         UUID        PRIMARY KEY REFERENCES tickets(id) ON DELETE RESTRICT,
    event_id          UUID        NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    scanner_id        UUID        NOT NULL REFERENCES scanners(id) ON DELETE RESTRICT,
    admitted_at       TIMESTAMPTZ NOT NULL
);

COMMENT ON TABLE admissions IS 'Append-only attendance evidence: a ticket let in at the venue, through which scanner and when. Written together with tickets.admit_at; never changed or removed, even when the ticket is later voided.';
COMMENT ON COLUMN admissions.ticket_id IS 'The ticket let in (at most one admission per ticket)';
COMMENT ON COLUMN admissions.event_id IS 'The event of the ticket';
COMMENT ON COLUMN admissions.scanner_id IS 'The scanner that scanned the ticket';
COMMENT ON COLUMN admissions.admitted_at IS 'When the ticket was let in; equal to tickets.admit_at';

CREATE INDEX IF NOT EXISTS idx_admissions_scanner_id ON admissions(scanner_id);
COMMENT ON INDEX idx_admissions_scanner_id IS 'Supports the scanners foreign key and per-scanner admission lookups';

-- Rejected scans: the append-only note of a refused scan or of one refused
-- ticket in it. ticket_id is absent exactly for reason Forged. It carries no
-- foreign key: losing a rejection is acceptable while a failed batch would
-- lose all of them.
CREATE TABLE IF NOT EXISTS rejected_scans (
    id                UUID        PRIMARY KEY,
    event_id          UUID        NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    scanner_id        UUID        NOT NULL REFERENCES scanners(id) ON DELETE RESTRICT,
    ticket_id         UUID,
    reason            SMALLINT    NOT NULL,
    scanned_at        TIMESTAMPTZ NOT NULL,
    CONSTRAINT chk_rejected_scans_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_rejected_scans_reason CHECK (reason IN (1, 2, 3, 5, 6)),
    CONSTRAINT chk_rejected_scans_ticket_unless_forged CHECK ((reason = 1) = (ticket_id IS NULL))
);

COMMENT ON TABLE rejected_scans IS 'Append-only note of a refused scan (Forged, Expired, OtherEvent) or of one refused ticket in it (Voided, AlreadyAdmitted). Reason 1=Forged, 2=Expired, 3=OtherEvent, 5=Voided, 6=AlreadyAdmitted (4 is retired).';
COMMENT ON COLUMN rejected_scans.id IS 'Unique rejected scan identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN rejected_scans.event_id IS 'The event of the scanner that scanned';
COMMENT ON COLUMN rejected_scans.scanner_id IS 'The scanner that scanned';
COMMENT ON COLUMN rejected_scans.ticket_id IS 'The presented ticket the refusal is about; NULL exactly when the reason is Forged. No FK: a genuine code may present an id that is no ticket';
COMMENT ON COLUMN rejected_scans.reason IS 'Why it was refused: 1=Forged, 2=Expired, 3=OtherEvent, 5=Voided, 6=AlreadyAdmitted';
COMMENT ON COLUMN rejected_scans.scanned_at IS 'When the scan was decided';

CREATE INDEX IF NOT EXISTS idx_rejected_scans_event_id ON rejected_scans(event_id);
COMMENT ON INDEX idx_rejected_scans_event_id IS 'Supports per-event review of rejected scans and the events foreign key';

CREATE INDEX IF NOT EXISTS idx_rejected_scans_scanner_id ON rejected_scans(scanner_id);
COMMENT ON INDEX idx_rejected_scans_scanner_id IS 'Supports the scanners foreign key';
