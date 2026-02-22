-- liverty-music backend database schema
-- This schema follows Clean Architecture principles by separating
-- user management, artist discovery, and concert notifications.

-- Users table
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    external_id UUID UNIQUE NOT NULL,
    name TEXT NOT NULL,
    email TEXT UNIQUE NOT NULL,
    preferred_language TEXT DEFAULT 'en',
    country TEXT,
    time_zone TEXT,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    safe_address TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT users_safe_address_unique UNIQUE (safe_address)
);

COMMENT ON TABLE users IS 'User profiles and authentication data';
COMMENT ON COLUMN users.id IS 'Unique user identifier (UUIDv7)';
COMMENT ON COLUMN users.external_id IS 'Zitadel identity provider user ID (sub claim), used for account sync';
COMMENT ON COLUMN users.email IS 'Primary contact and login identifier';
COMMENT ON COLUMN users.name IS 'User display name from identity provider';
COMMENT ON COLUMN users.preferred_language IS 'User preferred language code (e.g., en, ja)';
COMMENT ON COLUMN users.country IS 'User country code (ISO 3166-1 alpha-2)';
COMMENT ON COLUMN users.time_zone IS 'User time zone (IANA time zone database)';
COMMENT ON COLUMN users.is_active IS 'Whether the user account is active';
COMMENT ON COLUMN users.safe_address IS 'Predicted Safe (ERC-4337) address derived deterministically from users.id via CREATE2';
COMMENT ON COLUMN users.created_at IS 'Timestamp when the user was created';
COMMENT ON COLUMN users.updated_at IS 'Timestamp when the user was last updated';

-- Artists table
CREATE TABLE IF NOT EXISTS artists (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    name TEXT NOT NULL,
    mbid VARCHAR(36)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_artists_mbid ON artists(mbid) WHERE mbid IS NOT NULL AND mbid != '';

COMMENT ON TABLE artists IS 'Musical artists or groups that users can subscribe to for concert notifications';
COMMENT ON COLUMN artists.id IS 'Unique artist identifier (UUIDv7)';
COMMENT ON COLUMN artists.name IS 'Artist or band name as displayed to users';
COMMENT ON COLUMN artists.mbid IS 'Canonical MusicBrainz Identifier (MBID format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx)';

-- Artist official site
CREATE TABLE IF NOT EXISTS artist_official_site (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE UNIQUE,
    url TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE artist_official_site IS 'Stores the official website URL for each artist, used for concert search grounding.';
COMMENT ON COLUMN artist_official_site.id IS 'Unique identifier (UUIDv7)';
COMMENT ON COLUMN artist_official_site.artist_id IS 'Reference to the artist (1:1 relationship)';
COMMENT ON COLUMN artist_official_site.url IS 'Official artist website URL';

-- Venue enrichment status enum
CREATE TYPE venue_enrichment_status AS ENUM ('pending', 'enriched', 'failed');

-- Venues table
CREATE TABLE IF NOT EXISTS venues (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    name TEXT NOT NULL,
    admin_area TEXT,
    mbid TEXT,
    google_place_id TEXT,
    enrichment_status venue_enrichment_status NOT NULL DEFAULT 'pending',
    raw_name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_venues_name_not_empty CHECK (name <> ''),
    CONSTRAINT chk_venues_raw_name_not_empty CHECK (raw_name <> '')
);

COMMENT ON TABLE venues IS 'Physical locations where concerts and live events are hosted';
COMMENT ON COLUMN venues.id IS 'Unique venue identifier (UUIDv7)';
COMMENT ON COLUMN venues.name IS 'Venue name as displayed to users';
COMMENT ON COLUMN venues.admin_area IS 'Administrative area (prefecture, state, province) where the venue is located; NULL when not determinable with confidence';
COMMENT ON COLUMN venues.mbid IS 'MusicBrainz Place ID (UUID format) for the canonical venue record; NULL until enriched';
COMMENT ON COLUMN venues.google_place_id IS 'Google Maps Place ID for the canonical venue record; NULL until enriched';
COMMENT ON COLUMN venues.enrichment_status IS 'Current state of the venue normalization pipeline: pending (default), enriched, or failed';
COMMENT ON COLUMN venues.raw_name IS 'Original scraper-provided venue name before canonical renaming; backfilled from name on migration';
COMMENT ON COLUMN venues.created_at IS 'Timestamp when the venue was added to the system';
COMMENT ON COLUMN venues.updated_at IS 'Timestamp when venue data was last updated';

-- Events table
CREATE TABLE IF NOT EXISTS events (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    venue_id UUID NOT NULL REFERENCES venues(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    listed_venue_name TEXT,
    local_event_date DATE NOT NULL,
    start_at TIMESTAMPTZ,
    open_at TIMESTAMPTZ,
    source_url TEXT,
    merkle_root BYTEA,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE events IS 'Generic event data including time, location, and metadata';
COMMENT ON COLUMN events.id IS 'Unique event identifier (UUIDv7)';
COMMENT ON COLUMN events.venue_id IS 'Reference to the venue hosting the event';
COMMENT ON COLUMN events.title IS 'Event title as displayed to users';
COMMENT ON COLUMN events.listed_venue_name IS 'Raw venue name as scraped from the source, preserved separately from the normalized venue record';
COMMENT ON COLUMN events.local_event_date IS 'Date of the event';
COMMENT ON COLUMN events.start_at IS 'Event start time (absolute)';
COMMENT ON COLUMN events.open_at IS 'Doors open time (absolute), if available';
COMMENT ON COLUMN events.source_url IS 'URL where the event information was found';
COMMENT ON COLUMN events.merkle_root IS 'Merkle tree root hash for ZKP identity set; NULL for non-ticket events';
COMMENT ON COLUMN events.created_at IS 'Timestamp when the event was created';
COMMENT ON COLUMN events.updated_at IS 'Timestamp when the event was last updated';

-- Concerts table
CREATE TABLE IF NOT EXISTS concerts (
    event_id UUID PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE
);

COMMENT ON TABLE concerts IS 'Music-specific event details, linked 1:1 with events table';
COMMENT ON COLUMN concerts.event_id IS 'Reference to the generic event (PK/FK)';
COMMENT ON COLUMN concerts.artist_id IS 'Reference to the performing artist';

-- User artist follows
CREATE TABLE IF NOT EXISTS followed_artists (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, artist_id)
);

COMMENT ON TABLE followed_artists IS 'Tracks which artists a user is following for discovery and personalization';
COMMENT ON COLUMN followed_artists.user_id IS 'Reference to the user who is following';
COMMENT ON COLUMN followed_artists.artist_id IS 'Reference to the artist being followed';
COMMENT ON COLUMN followed_artists.created_at IS 'Timestamp when the follow occurred';

-- Notifications table
CREATE TABLE IF NOT EXISTS notifications (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    concert_id UUID REFERENCES concerts(event_id) ON DELETE SET NULL,
    type TEXT NOT NULL,
    title TEXT NOT NULL,
    message TEXT NOT NULL,
    language TEXT NOT NULL DEFAULT 'en',
    status TEXT NOT NULL DEFAULT 'pending',
    scheduled_at TIMESTAMPTZ NOT NULL,
    sent_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE notifications IS 'Scheduled and sent notifications about concerts and artist activities';
COMMENT ON COLUMN notifications.id IS 'Unique notification identifier (UUIDv7)';
COMMENT ON COLUMN notifications.user_id IS 'Reference to the recipient user';
COMMENT ON COLUMN notifications.artist_id IS 'Reference to the artist related to this notification';
COMMENT ON COLUMN notifications.concert_id IS 'Reference to the specific concert (nullable for general artist announcements)';
COMMENT ON COLUMN notifications.type IS 'Notification type: concert_announced, tickets_available, concert_reminder, concert_cancelled';
COMMENT ON COLUMN notifications.title IS 'Notification subject or headline';
COMMENT ON COLUMN notifications.message IS 'Full notification message body';
COMMENT ON COLUMN notifications.language IS 'ISO 639-1 language code for the notification content';
COMMENT ON COLUMN notifications.status IS 'Delivery status: pending, sent, failed, or cancelled';
COMMENT ON COLUMN notifications.scheduled_at IS 'When the notification should be sent';
COMMENT ON COLUMN notifications.sent_at IS 'Actual timestamp when the notification was sent (NULL if not sent)';
COMMENT ON COLUMN notifications.created_at IS 'Timestamp when the notification was created';
COMMENT ON COLUMN notifications.updated_at IS 'Timestamp when notification status was last updated';

-- Latest search logs table
CREATE TABLE IF NOT EXISTS latest_search_logs (
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    searched_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (artist_id)
);

COMMENT ON TABLE latest_search_logs IS 'Tracks when each artist was last searched for concerts via external APIs';
COMMENT ON COLUMN latest_search_logs.artist_id IS 'Reference to the artist that was searched';
COMMENT ON COLUMN latest_search_logs.searched_at IS 'Timestamp of the most recent external search';

-- Tickets table (Soulbound Ticket ERC-5192)
CREATE TABLE IF NOT EXISTS tickets (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_id NUMERIC(78, 0) NOT NULL,
    tx_hash TEXT NOT NULL,
    minted_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE tickets IS 'Soulbound Ticket (ERC-5192) ownership records linking users to event tokens on-chain';
COMMENT ON COLUMN tickets.token_id IS 'On-chain ERC-721 token ID minted on Base Sepolia';
COMMENT ON COLUMN tickets.tx_hash IS 'Blockchain transaction hash of the mint operation';

-- Merkle tree nodes table for ZKP identity set per event
CREATE TABLE IF NOT EXISTS merkle_tree (
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    depth INT NOT NULL,
    node_index INT NOT NULL,
    hash BYTEA NOT NULL,
    PRIMARY KEY (event_id, depth, node_index)
);

COMMENT ON TABLE merkle_tree IS 'Merkle tree nodes for ZKP identity set per event; canonical tree maintained by backend';
COMMENT ON COLUMN merkle_tree.depth IS 'Tree depth level (0 = leaves, max = root)';
COMMENT ON COLUMN merkle_tree.node_index IS 'Node position at the given depth level';
COMMENT ON COLUMN merkle_tree.hash IS 'Poseidon hash value of the node';

-- Nullifiers table for double-entry prevention
CREATE TABLE IF NOT EXISTS nullifiers (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    nullifier_hash BYTEA NOT NULL,
    used_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE nullifiers IS 'Used ZKP nullifier hashes for preventing double entry at events';
COMMENT ON COLUMN nullifiers.nullifier_hash IS 'The nullifier hash from the ZK proof; unique per event to prevent reuse';

-- Push subscriptions table
CREATE TABLE IF NOT EXISTS push_subscriptions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint TEXT NOT NULL UNIQUE,
    p256dh TEXT NOT NULL,
    auth TEXT NOT NULL
);

COMMENT ON TABLE push_subscriptions IS 'Browser Web Push subscription data for delivering notifications';
COMMENT ON COLUMN push_subscriptions.id IS 'Unique identifier (UUIDv7)';
COMMENT ON COLUMN push_subscriptions.user_id IS 'Reference to the user who owns this subscription';
COMMENT ON COLUMN push_subscriptions.endpoint IS 'Push service endpoint URL provided by the browser';
COMMENT ON COLUMN push_subscriptions.p256dh IS 'ECDH public key for payload encryption (Base64url-encoded)';
COMMENT ON COLUMN push_subscriptions.auth IS 'Authentication secret for payload encryption (Base64url-encoded)';

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
CREATE UNIQUE INDEX IF NOT EXISTS idx_venues_mbid ON venues (mbid) WHERE mbid IS NOT NULL;
COMMENT ON INDEX idx_venues_mbid IS 'Ensures uniqueness of MusicBrainz Place ID across venue records';

CREATE UNIQUE INDEX IF NOT EXISTS idx_venues_google_place_id ON venues (google_place_id) WHERE google_place_id IS NOT NULL;
COMMENT ON INDEX idx_venues_google_place_id IS 'Ensures uniqueness of Google Maps Place ID across venue records';

CREATE INDEX IF NOT EXISTS idx_venues_raw_name ON venues (raw_name);
COMMENT ON INDEX idx_venues_raw_name IS 'Speeds up venue lookup by raw (pre-enrichment) name as fallback in GetByName';

-- Events indexes
CREATE INDEX IF NOT EXISTS idx_events_local_event_date ON events(local_event_date);
COMMENT ON INDEX idx_events_local_event_date IS 'Speeds up date-based event searches and calendar views';

CREATE INDEX IF NOT EXISTS idx_events_venue_id ON events(venue_id);
COMMENT ON INDEX idx_events_venue_id IS 'Optimizes listing events by venue';

-- Concerts indexes
CREATE INDEX IF NOT EXISTS idx_concerts_artist_id ON concerts(artist_id);
COMMENT ON INDEX idx_concerts_artist_id IS 'Optimizes listing concerts by artist';

-- Followed artists indexes
CREATE INDEX IF NOT EXISTS idx_followed_artists_user_id ON followed_artists(user_id);
COMMENT ON INDEX idx_followed_artists_user_id IS 'Optimizes retrieval of all followed artists for a user';

CREATE INDEX IF NOT EXISTS idx_followed_artists_artist_id ON followed_artists(artist_id);
COMMENT ON INDEX idx_followed_artists_artist_id IS 'Optimizes finding all followers of an artist';

-- Notifications indexes
CREATE INDEX IF NOT EXISTS idx_notifications_scheduled_at ON notifications(scheduled_at);
COMMENT ON INDEX idx_notifications_scheduled_at IS 'Optimizes time-based notification scheduling and batch sending queries';

CREATE INDEX IF NOT EXISTS idx_notifications_status ON notifications(status);
COMMENT ON INDEX idx_notifications_status IS 'Optimizes filtering notifications by delivery status (e.g., pending for batch processing)';

CREATE INDEX IF NOT EXISTS idx_notifications_user_id ON notifications(user_id);
COMMENT ON INDEX idx_notifications_user_id IS 'Optimizes retrieval of notification history for a user';

-- Tickets indexes
CREATE UNIQUE INDEX IF NOT EXISTS idx_tickets_event_user ON tickets(event_id, user_id);
CREATE INDEX IF NOT EXISTS idx_tickets_user_id ON tickets(user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tickets_token_id ON tickets(token_id);

-- Nullifiers indexes
CREATE UNIQUE INDEX IF NOT EXISTS idx_nullifiers_event_hash ON nullifiers(event_id, nullifier_hash);

-- Push subscriptions indexes
CREATE INDEX IF NOT EXISTS idx_push_subscriptions_user_id ON push_subscriptions(user_id);
