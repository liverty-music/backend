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
    preferred_language TEXT DEFAULT 'en',
    country TEXT,
    time_zone TEXT,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    safe_address TEXT,
    home_id UUID,
    CONSTRAINT users_safe_address_unique UNIQUE (safe_address),
    CONSTRAINT chk_safe_address_format CHECK (safe_address IS NULL OR safe_address ~ '^0x[0-9a-fA-F]{40}$'),
    CONSTRAINT chk_users_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7')
);

COMMENT ON TABLE users IS 'User profiles and authentication data';
COMMENT ON COLUMN users.id IS 'Unique user identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN users.external_id IS 'Zitadel identity provider user ID (sub claim), used for account sync';
COMMENT ON COLUMN users.email IS 'Primary contact and login identifier';
COMMENT ON COLUMN users.name IS 'User display name from identity provider';
COMMENT ON COLUMN users.preferred_language IS 'User preferred language code (e.g., en, ja)';
COMMENT ON COLUMN users.country IS 'User country code (ISO 3166-1 alpha-2)';
COMMENT ON COLUMN users.time_zone IS 'User time zone (IANA time zone database)';
COMMENT ON COLUMN users.is_active IS 'Whether the user account is active';
COMMENT ON COLUMN users.safe_address IS 'Predicted Safe (ERC-4337) address derived deterministically from users.id via CREATE2';
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

-- Events table
CREATE TABLE IF NOT EXISTS events (
    id UUID PRIMARY KEY,
    venue_id UUID NOT NULL REFERENCES venues(id) ON DELETE CASCADE,
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    listed_venue_name TEXT,
    local_event_date DATE NOT NULL,
    start_at TIMESTAMPTZ,
    open_at TIMESTAMPTZ,
    source_url TEXT,
    merkle_root BYTEA,
    CONSTRAINT uq_events_natural_key UNIQUE (artist_id, local_event_date),
    CONSTRAINT chk_events_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7')
);

COMMENT ON TABLE events IS 'Generic event data including time, location, and metadata';
COMMENT ON CONSTRAINT uq_events_natural_key ON events IS 'Prevents duplicate events for the same artist and date. An artist cannot perform at two venues simultaneously.';
COMMENT ON COLUMN events.id IS 'Unique event identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN events.venue_id IS 'Reference to the venue hosting the event';
COMMENT ON COLUMN events.artist_id IS 'Reference to the performing artist; denormalized from concerts for natural key deduplication';
COMMENT ON COLUMN events.title IS 'Event title as displayed to users';
COMMENT ON COLUMN events.listed_venue_name IS 'Raw venue name as scraped from the source, preserved separately from the normalized venue record';
COMMENT ON COLUMN events.local_event_date IS 'Date of the event';
COMMENT ON COLUMN events.start_at IS 'Event start time (absolute)';
COMMENT ON COLUMN events.open_at IS 'Doors open time (absolute), if available';
COMMENT ON COLUMN events.source_url IS 'URL where the event information was found';
COMMENT ON COLUMN events.merkle_root IS 'Merkle tree root hash for ZKP identity set; NULL for non-ticket events';

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
    hype TEXT NOT NULL DEFAULT 'watch',
    PRIMARY KEY (user_id, artist_id),
    CONSTRAINT chk_followed_artists_hype CHECK (hype IN ('watch', 'home', 'nearby', 'away'))
);

COMMENT ON TABLE followed_artists IS 'Tracks which artists a user is following for discovery and personalization';
COMMENT ON COLUMN followed_artists.user_id IS 'Reference to the user who is following';
COMMENT ON COLUMN followed_artists.artist_id IS 'Reference to the artist being followed';
COMMENT ON COLUMN followed_artists.hype IS 'User enthusiasm tier: watch (no notifications, default), home (home area only), nearby (reserved), or away (all concerts)';

-- Latest search logs table
CREATE TABLE IF NOT EXISTS latest_search_logs (
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    searched_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status TEXT NOT NULL DEFAULT 'completed',
    PRIMARY KEY (artist_id)
);

COMMENT ON TABLE latest_search_logs IS 'Tracks when each artist was last searched for concerts via external APIs';
COMMENT ON COLUMN latest_search_logs.artist_id IS 'Reference to the artist that was searched';
COMMENT ON COLUMN latest_search_logs.searched_at IS 'Timestamp of the most recent external search';
COMMENT ON COLUMN latest_search_logs.status IS 'Search job status: pending, completed, or failed';

-- Tickets table (Soulbound Ticket ERC-5192)
CREATE TABLE IF NOT EXISTS tickets (
    id UUID PRIMARY KEY,
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_id NUMERIC(78, 0) NOT NULL,
    tx_hash TEXT NOT NULL,
    minted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_tickets_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7')
);

COMMENT ON TABLE tickets IS 'Soulbound Ticket (ERC-5192) ownership records linking users to event tokens on-chain';
COMMENT ON COLUMN tickets.id IS 'Unique ticket identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN tickets.event_id IS 'Reference to the event this ticket grants entry to';
COMMENT ON COLUMN tickets.user_id IS 'Reference to the ticket holder';
COMMENT ON COLUMN tickets.token_id IS 'On-chain ERC-721 token ID minted on Base Sepolia';
COMMENT ON COLUMN tickets.tx_hash IS 'Blockchain transaction hash of the mint operation';
COMMENT ON COLUMN tickets.minted_at IS 'Timestamp when the ticket was minted on-chain';

-- Merkle tree nodes table for ZKP identity set per event
CREATE TABLE IF NOT EXISTS merkle_tree (
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    depth INT NOT NULL,
    node_index INT NOT NULL,
    hash BYTEA NOT NULL,
    PRIMARY KEY (event_id, depth, node_index),
    CONSTRAINT chk_merkle_depth_positive CHECK (depth >= 0),
    CONSTRAINT chk_merkle_index_positive CHECK (node_index >= 0),
    CONSTRAINT chk_merkle_hash_size CHECK (octet_length(hash) = 32)
);

COMMENT ON TABLE merkle_tree IS 'Merkle tree nodes for ZKP identity set per event; canonical tree maintained by backend';
COMMENT ON COLUMN merkle_tree.event_id IS 'Reference to the event this Merkle tree belongs to';
COMMENT ON COLUMN merkle_tree.depth IS 'Tree depth level (0 = leaves, max = root)';
COMMENT ON COLUMN merkle_tree.node_index IS 'Node position at the given depth level';
COMMENT ON COLUMN merkle_tree.hash IS 'Poseidon hash value of the node';

-- Nullifiers table for double-entry prevention
CREATE TABLE IF NOT EXISTS nullifiers (
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    nullifier_hash BYTEA NOT NULL,
    used_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (event_id, nullifier_hash),
    CONSTRAINT chk_nullifier_hash_size CHECK (octet_length(nullifier_hash) = 32)
);

COMMENT ON TABLE nullifiers IS 'Used ZKP nullifier hashes for preventing double entry at events';
COMMENT ON COLUMN nullifiers.event_id IS 'Reference to the event this nullifier was used at';
COMMENT ON COLUMN nullifiers.nullifier_hash IS 'The nullifier hash from the ZK proof; unique per event to prevent reuse';
COMMENT ON COLUMN nullifiers.used_at IS 'Timestamp when the nullifier was consumed for event entry';

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
    payment_deadline TIMESTAMPTZ,
    lottery_start TIMESTAMPTZ,
    lottery_end TIMESTAMPTZ,
    application_url TEXT,
    lottery_result SMALLINT,
    payment_status SMALLINT,
    CONSTRAINT chk_ticket_emails_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_ticket_emails_email_type CHECK (email_type BETWEEN 1 AND 2),
    CONSTRAINT chk_ticket_emails_lottery_result CHECK (lottery_result IS NULL OR lottery_result BETWEEN 1 AND 2),
    CONSTRAINT chk_ticket_emails_payment_status CHECK (payment_status IS NULL OR payment_status BETWEEN 1 AND 2)
);

COMMENT ON TABLE ticket_emails IS 'Ticket-related emails imported via PWA Share Target and parsed by Gemini Flash. Linked to ticket_journeys via (user_id, event_id).';
COMMENT ON COLUMN ticket_emails.id IS 'Unique ticket email identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN ticket_emails.user_id IS 'Reference to the fan who imported this email';
COMMENT ON COLUMN ticket_emails.event_id IS 'Reference to the event this email is associated with';
COMMENT ON COLUMN ticket_emails.email_type IS 'Email type: 1=LOTTERY_INFO, 2=LOTTERY_RESULT';
COMMENT ON COLUMN ticket_emails.raw_body IS 'Email text as provided by the user (optionally redacted for PII)';
COMMENT ON COLUMN ticket_emails.parsed_data IS 'Structured JSON output from Gemini Flash parsing';
COMMENT ON COLUMN ticket_emails.payment_deadline IS 'Payment due date extracted from lottery result emails';
COMMENT ON COLUMN ticket_emails.lottery_start IS 'Lottery application period start from lottery info emails';
COMMENT ON COLUMN ticket_emails.lottery_end IS 'Lottery application period end from lottery info emails';
COMMENT ON COLUMN ticket_emails.application_url IS 'URL for lottery application from lottery info emails';
COMMENT ON COLUMN ticket_emails.lottery_result IS 'Lottery outcome: 1=WON, 2=LOST. Present only for LOTTERY_RESULT emails.';
COMMENT ON COLUMN ticket_emails.payment_status IS 'Payment state: 1=UNPAID, 2=PAID. Present only for LOTTERY_RESULT emails where lottery_result=WON.';

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

-- Tickets indexes
CREATE UNIQUE INDEX IF NOT EXISTS idx_tickets_event_user ON tickets(event_id, user_id);
CREATE INDEX IF NOT EXISTS idx_tickets_user_id ON tickets(user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tickets_token_id ON tickets(token_id);

-- Ticket journeys indexes
CREATE INDEX IF NOT EXISTS idx_ticket_journeys_event_id ON ticket_journeys(event_id);

-- Ticket emails indexes
CREATE INDEX IF NOT EXISTS idx_ticket_emails_user_event ON ticket_emails(user_id, event_id);
COMMENT ON INDEX idx_ticket_emails_user_event IS 'Optimizes lookup of imported emails for a user-event combination';

-- Push subscriptions indexes
CREATE INDEX IF NOT EXISTS idx_push_subscriptions_user_id ON push_subscriptions(user_id);
