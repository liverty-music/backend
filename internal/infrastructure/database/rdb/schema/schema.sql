-- liverty-music backend database schema
-- This schema follows Clean Architecture principles by separating
-- user management, artist discovery, and concert notifications.

CREATE SCHEMA IF NOT EXISTS app;
SET search_path TO app, public;

-- Users table
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY,
    external_id TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    email TEXT UNIQUE NOT NULL,
    preferred_language TEXT,
    country TEXT,
    time_zone TEXT,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    home_id UUID,
    CONSTRAINT chk_users_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7')
);

COMMENT ON TABLE users IS 'User profiles and authentication data';
COMMENT ON COLUMN users.id IS 'Unique user identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN users.external_id IS 'Zitadel identity provider user ID (sub claim), used for account sync';
COMMENT ON COLUMN users.email IS 'Primary contact and login identifier';
COMMENT ON COLUMN users.name IS 'User display name from identity provider';
COMMENT ON COLUMN users.preferred_language IS 'User preferred language code (e.g., en, ja). NULL means "not yet set by client"; client backfills via UpdatePreferredLanguage on first observation.';
COMMENT ON COLUMN users.country IS 'User country code (ISO 3166-1 alpha-2)';
COMMENT ON COLUMN users.time_zone IS 'User time zone (IANA time zone database)';
COMMENT ON COLUMN users.is_active IS 'Whether the user account is active';
COMMENT ON COLUMN users.home_id IS 'Reference to the user home area in the homes table. NULL when home is not set.';

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

COMMENT ON TABLE series IS 'Parent aggregation above events. Owns metadata shared across every event in a tour, festival, or multi-day single-venue run. Cover image URL is derived from the series_media join at read time.';
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

COMMENT ON TABLE concerts IS 'Music-specific event extension, linked 1:1 with events. Currently a placeholder; reserved for future music-specific columns per the Event-Type Extensibility requirement.';
COMMENT ON COLUMN concerts.event_id IS 'Reference to the generic event (PK/FK)';

-- Event performers (M:N between events and artists)
CREATE TABLE IF NOT EXISTS event_performers (
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    PRIMARY KEY (event_id, artist_id)
);

COMMENT ON TABLE event_performers IS 'M:N relation between events and performing artists. Supports festival lineups, co-headliners, and support acts.';
COMMENT ON COLUMN event_performers.event_id IS 'Reference to the event';
COMMENT ON COLUMN event_performers.artist_id IS 'Reference to the performing artist';

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
-- organizer's represented artists. On publish these become event_performers on
-- each materialized event.
CREATE TABLE IF NOT EXISTS draft_series_performers (
    series_id UUID NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    PRIMARY KEY (series_id, artist_id)
);

COMMENT ON TABLE draft_series_performers IS 'Series-level performers of a first-party DRAFT series, materialized into event_performers for every event on publish.';
COMMENT ON COLUMN draft_series_performers.series_id IS 'Parent first-party series being authored';
COMMENT ON COLUMN draft_series_performers.artist_id IS 'A performing artist the organizer represents';

-- Media kind enum. IMAGE is the only value at MVP; extend later via
-- ALTER TYPE media_kind ADD VALUE.
CREATE TYPE media_kind AS ENUM ('IMAGE');

COMMENT ON TYPE media_kind IS 'Kind of organizer media asset: IMAGE (cover photo etc.). Extend via ALTER TYPE ADD VALUE.';

-- Organizer media objects. The row id is a UUIDv7 that serves as both the
-- creation-time source and the cache-busting object-key token. The object key
-- is `{exposure}/{organizer_id}/{media_id}` where exposure is "internal" or
-- "cdn". No URL is stored; it is derived at read time.
CREATE TABLE IF NOT EXISTS media (
    id           UUID       PRIMARY KEY,
    organizer_id UUID       NOT NULL REFERENCES organizers(id),
    kind         media_kind NOT NULL,
    attributes   JSONB      NOT NULL DEFAULT '{}',
    CONSTRAINT chk_media_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7')
);

COMMENT ON TABLE media IS 'Normalised media objects uploaded by an organizer. The row id is the UUIDv7 that forms the object-key basename (internal/{org}/{id} or cdn/{org}/{id}); content_type is stored in attributes.';
COMMENT ON COLUMN media.id IS 'Unique media identifier (UUIDv7, application-generated). Doubles as the cache-busting version token in the object key; a new upload mints a new id.';
COMMENT ON COLUMN media.organizer_id IS 'Owning organizer. Used as the stable tenant segment of the object key.';
COMMENT ON COLUMN media.kind IS 'Media asset kind (IMAGE at MVP).';
COMMENT ON COLUMN media.attributes IS 'Kind-specific metadata as JSONB. For IMAGE: {"content_type": "image/jpeg"}. May include width/height later.';

-- Series media join table (first-party authoring).
-- At MVP each series carries at most one cover image (uq_series_media_series).
-- The display_order column is reserved for future gallery support.
CREATE TABLE IF NOT EXISTS series_media (
    series_id     UUID NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    media_id      UUID NOT NULL REFERENCES media(id)  ON DELETE CASCADE,
    display_order INT  NOT NULL DEFAULT 0,
    PRIMARY KEY (series_id, media_id)
);

COMMENT ON TABLE series_media IS 'Join table between a series and its media objects. One cover per series at MVP (uq_series_media_series).';
COMMENT ON COLUMN series_media.series_id IS 'Parent series this media object is attached to.';
COMMENT ON COLUMN series_media.media_id IS 'Referenced media object.';
COMMENT ON COLUMN series_media.display_order IS 'Sort position within the series gallery (reserved for future gallery support; 0 = cover).';

CREATE UNIQUE INDEX IF NOT EXISTS uq_series_media_series ON series_media(series_id);
COMMENT ON INDEX uq_series_media_series IS 'At most one media object (the cover) per series at MVP; drop or relax when galleries are introduced.';

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

-- Latest search logs table
CREATE TABLE IF NOT EXISTS latest_search_logs (
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    searched_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status TEXT NOT NULL DEFAULT 'completed',
    last_found_at TIMESTAMPTZ,
    PRIMARY KEY (artist_id)
);

COMMENT ON TABLE latest_search_logs IS 'Tracks when each artist was last searched for concerts via external APIs';
COMMENT ON COLUMN latest_search_logs.artist_id IS 'Reference to the artist that was searched';
COMMENT ON COLUMN latest_search_logs.searched_at IS 'Timestamp of the most recent external search';
COMMENT ON COLUMN latest_search_logs.status IS 'Search job status: pending, completed, or failed';
COMMENT ON COLUMN latest_search_logs.last_found_at IS 'Timestamp of the most recent search that discovered at least one new concert; NULL if none ever found';

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

-- Ticket emails table (imported ticket-related emails parsed by Gemini)
CREATE TABLE IF NOT EXISTS ticket_emails (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    email_type SMALLINT NOT NULL,
    raw_body TEXT NOT NULL,
    parsed_data JSONB,
    payment_deadline_at TIMESTAMPTZ,
    lottery_start_at TIMESTAMPTZ,
    lottery_end_at TIMESTAMPTZ,
    application_url TEXT,
    journey_status SMALLINT,
    CONSTRAINT chk_ticket_emails_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_ticket_emails_email_type CHECK (email_type BETWEEN 1 AND 2),
    CONSTRAINT chk_ticket_emails_journey_status CHECK (journey_status IS NULL OR journey_status BETWEEN 1 AND 5)
);

COMMENT ON TABLE ticket_emails IS 'Ticket-related emails imported via PWA Share Target and parsed by Gemini Flash. Linked to ticket_journeys via (user_id, event_id).';
COMMENT ON COLUMN ticket_emails.id IS 'Unique ticket email identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN ticket_emails.user_id IS 'Reference to the fan who imported this email';
COMMENT ON COLUMN ticket_emails.event_id IS 'Reference to the event this email is associated with';
COMMENT ON COLUMN ticket_emails.email_type IS 'Email type: 1=LOTTERY_INFO, 2=LOTTERY_RESULT';
COMMENT ON COLUMN ticket_emails.raw_body IS 'Email text as provided by the user (optionally redacted for PII)';
COMMENT ON COLUMN ticket_emails.parsed_data IS 'Structured JSON output from Gemini Flash parsing';
COMMENT ON COLUMN ticket_emails.payment_deadline_at IS 'Payment due date extracted from lottery result emails';
COMMENT ON COLUMN ticket_emails.lottery_start_at IS 'Lottery application period start from lottery info emails';
COMMENT ON COLUMN ticket_emails.lottery_end_at IS 'Lottery application period end from lottery info emails';
COMMENT ON COLUMN ticket_emails.application_url IS 'URL for lottery application from lottery info emails';
COMMENT ON COLUMN ticket_emails.journey_status IS 'TicketJourney status derived from email: 1=TRACKING, 2=APPLIED, 3=LOST, 4=UNPAID, 5=PAID';

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
-- Represents a single series-level ticket-sales window (e.g. FC pre-sale, general
-- lottery, general on-sale). A phase applies to its series as a whole; there is no
-- per-event coverage subset. The surrogate id is the ONLY uniqueness key — there is
-- no compound unique constraint on (series_id, apply_start_at) because convergence
-- on that pair is enforced at the application layer (a UNIQUE index MAY be added
-- later as a safety net).
CREATE TABLE IF NOT EXISTS sales_phases (
    id UUID PRIMARY KEY,
    series_id UUID NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    method SMALLINT NOT NULL,
    channel SMALLINT NOT NULL,
    provider_name TEXT,
    sequence INT NOT NULL DEFAULT 0,
    apply_start_at TIMESTAMPTZ NOT NULL,
    apply_end_at TIMESTAMPTZ,
    lottery_result_at TIMESTAMPTZ,
    payment_deadline_at TIMESTAMPTZ,
    url TEXT,
    discovered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_sales_phases_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_sales_phases_method CHECK (method BETWEEN 0 AND 2),
    CONSTRAINT chk_sales_phases_channel CHECK (channel BETWEEN 0 AND 6),
    CONSTRAINT chk_sales_phases_sequence CHECK (sequence >= 0)
);

COMMENT ON TABLE sales_phases IS 'A single series-level ticket-sales window. The surrogate id is the only uniqueness key; application-layer matching on (series_id, apply_start_at) converges re-discovered phases onto existing rows.';
COMMENT ON COLUMN sales_phases.id IS 'Unique sales phase identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN sales_phases.series_id IS 'Reference to the parent series that owns this sales phase';
COMMENT ON COLUMN sales_phases.method IS 'Sales method: 0=UNSPECIFIED, 1=LOTTERY, 2=FIRST_COME';
COMMENT ON COLUMN sales_phases.channel IS 'Sales channel: 0=UNSPECIFIED, 1=FAN_CLUB, 2=OFFICIAL, 3=PLAYGUIDE, 4=CREDIT_CARD, 5=MOBILE_CARRIER, 6=GENERAL. Concrete play-guide provider names go in provider_name.';
COMMENT ON COLUMN sales_phases.provider_name IS 'Verbatim provider name from the source (e.g. "e+", "ローチケ"). NULL when indeterminate.';
COMMENT ON COLUMN sales_phases.sequence IS 'Ordinal within the same channel for phases that occur in multiple rounds (0-based). Does not uniquely identify a phase.';
COMMENT ON COLUMN sales_phases.apply_start_at IS 'Start of the application or on-sale window (required). Must be known for a phase to be persisted. Together with series_id it is the application-layer convergence key.';
COMMENT ON COLUMN sales_phases.apply_end_at IS 'End of the application window (lottery) or close of on-sale (first-come). NULL when unknown.';
COMMENT ON COLUMN sales_phases.lottery_result_at IS 'When lottery results are announced. NULL for first-come phases or when unknown.';
COMMENT ON COLUMN sales_phases.payment_deadline_at IS 'Payment deadline after winning a lottery. NULL for first-come phases or when unknown.';
COMMENT ON COLUMN sales_phases.url IS 'Direct URL to the sales page for this phase. NULL when not available.';
COMMENT ON COLUMN sales_phases.discovered_at IS 'Timestamp when this sales phase row was first inserted. Used as the first-sight guard: stages whose natural trigger is before discovered_at are not fired.';

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
COMMENT ON COLUMN sales_phase_reminders.stage IS 'Reminder stage: 1=APPLY_OPEN (at apply_start_time), 2=APPLY_CLOSE_24H (24h before apply_end_time), 3=APPLY_CLOSE_1H (1h before apply_end_time), 4=RESULT_DAY (09:00 on lottery_result_time day). Payment-deadline stage deferred.';
COMMENT ON COLUMN sales_phase_reminders.sent_at IS 'Timestamp when the reminder was dispatched';

-- Staged concerts (approval queue)
-- Concerts discovered by the Gemini search pipeline are held here in a pending
-- state until a developer approves them in the admin console. Venue resolution
-- (Google Places) runs at staging time and is denormalised onto the row so the
-- reviewer can judge venue accuracy; the canonical venues row is created only on
-- approval. This table holds only pending rows — both approve and reject delete
-- the row, so a re-discovered concert can re-enter the queue after a rejection.
CREATE TABLE IF NOT EXISTS staged_concerts (
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
    CONSTRAINT chk_staged_concerts_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7')
);

COMMENT ON TABLE staged_concerts IS 'Approval queue for AI-discovered concerts. Holds only pending rows; approve publishes and deletes, reject logs and deletes. Re-discovery dedup consults this table plus published events, but never the rejection log.';
COMMENT ON COLUMN staged_concerts.id IS 'Unique staged concert identifier (UUIDv7, application-generated). Exposed to the admin console as StagedConcertId.';
COMMENT ON COLUMN staged_concerts.artist_id IS 'The performing artist this concert was discovered for.';
COMMENT ON COLUMN staged_concerts.series_id IS 'Parent series this staged event belongs to. The series row is created at discovery time (when the group series_id is resolved), so this is a real foreign key by staging time; approval inserts the event under it without minting a new series. type/title/source_url live on the series row.';
COMMENT ON COLUMN staged_concerts.title IS 'Descriptive title extracted for the concert (e.g. tour or show name).';
COMMENT ON COLUMN staged_concerts.local_date IS 'Scheduled calendar date of the concert in the venue local timezone.';
COMMENT ON COLUMN staged_concerts.start_at IS 'Scheduled start time. NULL when the source did not state one.';
COMMENT ON COLUMN staged_concerts.open_at IS 'Doors-open time. NULL when not announced.';
COMMENT ON COLUMN staged_concerts.listed_venue_name IS 'Raw venue name exactly as scraped from the source, preserved for review.';
COMMENT ON COLUMN staged_concerts.admin_area IS 'Administrative area extracted by Gemini for the concert. NULL when not extracted.';
COMMENT ON COLUMN staged_concerts.source_url IS 'Source URL where the concert was found. NULL when not provided.';
COMMENT ON COLUMN staged_concerts.resolved_place_id IS 'Google Places place id of the resolved venue. NULL when the listed name could not be resolved.';
COMMENT ON COLUMN staged_concerts.resolved_venue_name IS 'Canonical venue name resolved via Google Places. NULL when unresolved.';
COMMENT ON COLUMN staged_concerts.resolved_admin_area IS 'ISO 3166-2 admin area of the resolved venue. NULL when unresolved or indeterminate.';
COMMENT ON COLUMN staged_concerts.resolved_latitude IS 'WGS 84 latitude of the resolved venue. NULL when unresolved.';
COMMENT ON COLUMN staged_concerts.resolved_longitude IS 'WGS 84 longitude of the resolved venue. NULL when unresolved.';
COMMENT ON COLUMN staged_concerts.discovered_at IS 'Timestamp when the discovery pipeline staged this concert. Used to order the review queue.';

-- Rejected concerts log (append-only)
-- Every rejection is recorded here for search-quality analysis. It is NEVER read
-- by the discovery dedup path and so does not suppress future staging. artist_id
-- has no foreign key so history survives artist deletion.
CREATE TABLE IF NOT EXISTS rejected_concerts_log (
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
    CONSTRAINT chk_rejected_concerts_log_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7')
);

COMMENT ON TABLE rejected_concerts_log IS 'Append-only audit of rejected staged concerts, used for searcher-quality analysis only. Not consulted by discovery dedup; never suppresses re-discovery.';
COMMENT ON COLUMN rejected_concerts_log.id IS 'Unique log entry identifier (UUIDv7, application-generated).';
COMMENT ON COLUMN rejected_concerts_log.artist_id IS 'The performing artist the rejected concert was discovered for. Intentionally not a foreign key so the log survives artist deletion.';
COMMENT ON COLUMN rejected_concerts_log.artist_name IS 'Artist display name captured at rejection time for readability.';
COMMENT ON COLUMN rejected_concerts_log.title IS 'Descriptive title of the rejected concert.';
COMMENT ON COLUMN rejected_concerts_log.local_date IS 'Scheduled calendar date of the rejected concert.';
COMMENT ON COLUMN rejected_concerts_log.start_at IS 'Scheduled start time of the rejected concert. NULL when unknown.';
COMMENT ON COLUMN rejected_concerts_log.open_at IS 'Doors-open time of the rejected concert. NULL when unknown.';
COMMENT ON COLUMN rejected_concerts_log.listed_venue_name IS 'Raw scraped venue name of the rejected concert.';
COMMENT ON COLUMN rejected_concerts_log.admin_area IS 'Administrative area extracted for the rejected concert. NULL when not extracted.';
COMMENT ON COLUMN rejected_concerts_log.source_url IS 'Source URL of the rejected concert. NULL when not provided.';
COMMENT ON COLUMN rejected_concerts_log.resolved_place_id IS 'Google Places place id of the resolved venue at rejection time. NULL when unresolved.';
COMMENT ON COLUMN rejected_concerts_log.resolved_venue_name IS 'Resolved canonical venue name at rejection time. NULL when unresolved.';
COMMENT ON COLUMN rejected_concerts_log.resolved_admin_area IS 'Resolved admin area at rejection time. NULL when unresolved.';
COMMENT ON COLUMN rejected_concerts_log.reason IS 'Reviewer-provided reason for rejecting the concert.';
COMMENT ON COLUMN rejected_concerts_log.reviewed_by IS 'Identity (Zitadel subject) of the developer who rejected the concert. NULL when unavailable.';
COMMENT ON COLUMN rejected_concerts_log.rejected_at IS 'Timestamp when the concert was rejected.';

-- Suppressed concerts (moderation)
-- Records the natural key of a published concert an operator deleted so a later
-- discovery run does not auto-publish or re-stage it. Keyed by the event physical
-- identity (venue, local date, start time) with NULLS NOT DISTINCT so an
-- unknown-start slot collapses the same way the events unique key does. venue_id
-- carries no foreign key so a suppression survives independently of venue
-- lifecycle, mirroring the FK-less rejected_concerts_log. Distinct from that log,
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

COMMENT ON TABLE suppressed_concerts IS 'Moderation suppression set: the natural key of each published concert an operator deleted, consulted by discovery so a deleted concert is not auto-published or re-staged again. Distinct from rejected_concerts_log, which is analysis-only and never suppresses.';
COMMENT ON COLUMN suppressed_concerts.id IS 'Unique suppression entry identifier (UUIDv7, application-generated).';
COMMENT ON COLUMN suppressed_concerts.venue_id IS 'Resolved venue of the deleted event. Intentionally not a foreign key so the suppression survives independently of venue lifecycle.';
COMMENT ON COLUMN suppressed_concerts.local_event_date IS 'Local calendar date of the deleted event.';
COMMENT ON COLUMN suppressed_concerts.start_at IS 'Start time of the deleted event. NULL when unknown; NULLS NOT DISTINCT collapses an unknown-start slot like the events unique key.';
COMMENT ON COLUMN suppressed_concerts.suppressed_at IS 'Timestamp when the concert was suppressed (i.e. deleted by an operator).';

-- ============================================================
-- Indexes
-- ============================================================

-- Users indexes
CREATE INDEX IF NOT EXISTS idx_users_external_id ON users(external_id);
COMMENT ON INDEX idx_users_external_id IS 'Speeds up user lookup by Zitadel identity (sub claim)';

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

-- Event performers indexes
CREATE INDEX IF NOT EXISTS idx_event_performers_artist_id ON event_performers(artist_id);
COMMENT ON INDEX idx_event_performers_artist_id IS 'Optimizes lookup of all events for a given artist (reverse direction of the composite PK)';

-- Followed artists indexes
CREATE INDEX IF NOT EXISTS idx_followed_artists_user_id ON followed_artists(user_id);
COMMENT ON INDEX idx_followed_artists_user_id IS 'Optimizes retrieval of all followed artists for a user';

CREATE INDEX IF NOT EXISTS idx_followed_artists_artist_id ON followed_artists(artist_id);
COMMENT ON INDEX idx_followed_artists_artist_id IS 'Optimizes finding all followers of an artist';

-- Ticket journeys indexes
CREATE INDEX IF NOT EXISTS idx_ticket_journeys_event_id ON ticket_journeys(event_id);

-- Ticket emails indexes
CREATE INDEX IF NOT EXISTS idx_ticket_emails_user_event ON ticket_emails(user_id, event_id);
COMMENT ON INDEX idx_ticket_emails_user_event IS 'Optimizes lookup of imported emails for a user-event combination';

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

-- Staged concerts indexes
-- Two partial unique indexes form the NULL-safe natural key: when the venue
-- resolved, dedup on the canonical place id; otherwise fall back to the raw
-- listed venue name. They never overlap because the predicates are mutually
-- exclusive on resolved_place_id IS [NOT] NULL.
CREATE UNIQUE INDEX IF NOT EXISTS uq_staged_concerts_by_place ON staged_concerts(artist_id, local_date, resolved_place_id) WHERE resolved_place_id IS NOT NULL;
COMMENT ON INDEX uq_staged_concerts_by_place IS 'Natural-key dedup for resolved venues: one pending row per (artist, date, place id).';

CREATE UNIQUE INDEX IF NOT EXISTS uq_staged_concerts_by_listed_name ON staged_concerts(artist_id, local_date, listed_venue_name) WHERE resolved_place_id IS NULL;
COMMENT ON INDEX uq_staged_concerts_by_listed_name IS 'Natural-key dedup fallback when the venue did not resolve: one pending row per (artist, date, raw listed name).';

CREATE INDEX IF NOT EXISTS idx_staged_concerts_discovered_at ON staged_concerts(discovered_at);
COMMENT ON INDEX idx_staged_concerts_discovered_at IS 'Orders the review queue by discovery time';

-- Rejected concerts log indexes
CREATE INDEX IF NOT EXISTS idx_rejected_concerts_log_artist_id ON rejected_concerts_log(artist_id);
COMMENT ON INDEX idx_rejected_concerts_log_artist_id IS 'Supports per-artist analysis of repeated rejection patterns';

CREATE INDEX IF NOT EXISTS idx_rejected_concerts_log_rejected_at ON rejected_concerts_log(rejected_at);
COMMENT ON INDEX idx_rejected_concerts_log_rejected_at IS 'Supports time-windowed analysis of rejections';

-- Organizers: the vetted seller an admin creates to represent artists. Existence
-- is the vetting (no verified flag). status is the operational lifecycle
-- (1=provisioning, 2=active, 3=deactivated); zitadel_org_id links the token
-- tenant to this row and is set once tenant provisioning completes.
CREATE TABLE IF NOT EXISTS organizers (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    operator_email TEXT NOT NULL,
    zitadel_org_id TEXT,
    status SMALLINT NOT NULL,
    CONSTRAINT chk_organizers_name_non_empty CHECK (name <> ''),
    CONSTRAINT chk_organizers_operator_email_non_empty CHECK (operator_email <> ''),
    CONSTRAINT chk_organizers_status CHECK (status BETWEEN 1 AND 3),
    CONSTRAINT chk_organizers_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7')
);
COMMENT ON TABLE organizers IS 'Vetted sellers (label / agency / promoter / self-publishing artist) that represent artists';
COMMENT ON COLUMN organizers.id IS 'Unique organizer identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN organizers.name IS 'Organizer display name (label / agency / promoter / self-publishing artist)';
COMMENT ON COLUMN organizers.operator_email IS 'Email of the operator who administers this organizer; seeded as the initial Zitadel owner user';
COMMENT ON COLUMN organizers.zitadel_org_id IS 'Zitadel tenant organization ID; NULL until provisioning completes';
COMMENT ON COLUMN organizers.status IS 'Lifecycle state: 1=provisioning, 2=active, 3=deactivated';

CREATE UNIQUE INDEX IF NOT EXISTS uq_organizers_zitadel_org_id ON organizers(zitadel_org_id) WHERE zitadel_org_id IS NOT NULL;
COMMENT ON INDEX uq_organizers_zitadel_org_id IS 'One Zitadel tenant org maps to at most one Organizer; NULL while provisioning';

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

-- Verified identities table
-- Binds one account (users row) to a verified real person via JPKI or the
-- driver's-licence fallback (Pocket Sign Verify). The person key is the
-- Pocket Sign tenant-scoped User.id (pocket_sign_user_id). No 基本4情報,
-- no raw certificate, no 個人番号 is ever stored here — only the result.
-- The partial unique index (uq_active_pocket_sign_user_id) enforces the
-- invariant: at most one row with status='ACTIVE' per pocket_sign_user_id.
CREATE TABLE IF NOT EXISTS verified_identities (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    method SMALLINT NOT NULL,
    pocket_sign_user_id TEXT NOT NULL,
    dedupe_strength SMALLINT NOT NULL,
    verified_at TIMESTAMPTZ NOT NULL,
    status SMALLINT NOT NULL,
    CONSTRAINT chk_verified_identities_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_verified_identities_method CHECK (method BETWEEN 1 AND 2),
    CONSTRAINT chk_verified_identities_dedupe_strength CHECK (dedupe_strength BETWEEN 1 AND 2),
    CONSTRAINT chk_verified_identities_status CHECK (status BETWEEN 1 AND 2),
    CONSTRAINT chk_verified_identities_pocket_sign_user_id_non_empty CHECK (pocket_sign_user_id <> '')
);

COMMENT ON TABLE verified_identities IS 'Account-level identity verification. One row per (user, verification attempt); the active invariant (at most one ACTIVE row per pocket_sign_user_id) is enforced by the partial unique index.';
COMMENT ON COLUMN verified_identities.id IS 'Unique verification record identifier (UUIDv7, application-generated).';
COMMENT ON COLUMN verified_identities.user_id IS 'Reference to the user account this verification is bound to.';
COMMENT ON COLUMN verified_identities.method IS 'Verification method: 1=JPKI, 2=DRIVER_LICENCE.';
COMMENT ON COLUMN verified_identities.pocket_sign_user_id IS 'Pocket Sign tenant-scoped person key (User.id). The only person-identifying datum stored; never the serial, never the 個人番号.';
COMMENT ON COLUMN verified_identities.dedupe_strength IS 'Dedupe guarantee strength: 1=STRONG (JPKI, stable per-person key), 2=WEAK (driver licence, document-scoped key).';
COMMENT ON COLUMN verified_identities.verified_at IS 'Timestamp when the verification was established.';
COMMENT ON COLUMN verified_identities.status IS 'Freshness lifecycle: 1=ACTIVE (current), 2=NEEDS_REVERIFICATION (revoked/changed/expired per 現況確認).';

-- Partial unique index enforces the 1-person-1-active-account invariant.
CREATE UNIQUE INDEX IF NOT EXISTS uq_active_pocket_sign_user_id
    ON verified_identities(pocket_sign_user_id) WHERE status = 1;
COMMENT ON INDEX uq_active_pocket_sign_user_id IS 'At most one ACTIVE verified identity row per Pocket Sign person key. A second IDENTITY_VERIFIED account for the same person is rejected here.';

CREATE INDEX IF NOT EXISTS idx_verified_identities_user_id ON verified_identities(user_id);
COMMENT ON INDEX idx_verified_identities_user_id IS 'Optimizes lookup of a user''s active verification record.';

-- Rollback snapshot for the one-time series-consolidation migration
-- (20260826000000_consolidate_fragmented_series). Created and populated by that
-- migration before it re-points events/sales_phases and re-types series, and
-- INTENTIONALLY RETAINED afterwards as a manual rollback safety net for the
-- production data mutation. Declared here as desired state so `atlas migrate
-- diff` does not generate a DROP that would destroy the snapshot. Safe to drop
-- manually once the consolidation is verified in production (change task 5.4).
CREATE TABLE IF NOT EXISTS _series_consolidation_backup (
    entity TEXT NOT NULL,
    id UUID NOT NULL,
    old_series_id UUID,
    old_type series_type,
    PRIMARY KEY (entity, id)
);
COMMENT ON TABLE _series_consolidation_backup IS 'One-time rollback snapshot of series/events/sales_phases series_id (and series type) captured by the series-consolidation migration; retained for manual rollback, droppable once verified in prod.';
COMMENT ON COLUMN _series_consolidation_backup.entity IS 'Which table the row snapshots: event | sales_phase | series';
COMMENT ON COLUMN _series_consolidation_backup.id IS 'Primary key of the snapshotted row in its source table';
COMMENT ON COLUMN _series_consolidation_backup.old_series_id IS 'Pre-migration series_id (for event/sales_phase rows) or the series own id (for series rows)';
COMMENT ON COLUMN _series_consolidation_backup.old_type IS 'Pre-migration series.type (series rows only; NULL otherwise)';
