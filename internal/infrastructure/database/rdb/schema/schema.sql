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
COMMENT ON COLUMN users.preferred_language IS 'User preferred language code (e.g., en, ja). NULL means "not yet set by client"; client backfills via UpdatePreferredLanguage on first observation.';
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

-- Series table
CREATE TABLE IF NOT EXISTS series (
    id UUID PRIMARY KEY,
    title TEXT NOT NULL,
    type series_type NOT NULL,
    source_url TEXT,
    merch_url TEXT,
    CONSTRAINT chk_series_title_not_empty CHECK (title <> ''),
    CONSTRAINT chk_series_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7')
);

COMMENT ON TABLE series IS 'Parent aggregation above events. Owns metadata shared across every event in a tour, festival, or multi-day single-venue run.';
COMMENT ON COLUMN series.id IS 'Unique series identifier (UUIDv7, application-generated). Series has no content-derived key: cross-run identity is established by adopting the series_id already carried by its member events (matched on the events physical natural key); a fresh UUIDv7 series is minted only when no member event yet exists.';
COMMENT ON COLUMN series.title IS 'Series title shared across all member events (e.g. tour name, festival name)';
COMMENT ON COLUMN series.type IS 'Classification of the series; drives presentation and notification grouping';
COMMENT ON COLUMN series.source_url IS 'Optional series-level official URL (tour page, festival page); per-event URLs are not stored';
COMMENT ON COLUMN series.merch_url IS 'Optional official merchandise information page (official site page or official social media post) shared across the series; populated asynchronously by the merch-url discovery job. Stores only the link — no sale timing, channel, price, or item data.';

-- Events table
CREATE TABLE IF NOT EXISTS events (
    id UUID PRIMARY KEY,
    series_id UUID NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    venue_id UUID NOT NULL REFERENCES venues(id) ON DELETE CASCADE,
    listed_venue_name TEXT,
    local_event_date DATE NOT NULL,
    start_at TIMESTAMPTZ,
    open_at TIMESTAMPTZ,
    merkle_root BYTEA,
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
COMMENT ON COLUMN events.merkle_root IS 'Merkle tree root hash for ZKP identity set; NULL for non-ticket events';

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

-- Sales phases table
-- Represents a single ticket-sales window for a series (e.g. FC pre-sale, general
-- lottery, general on-sale). The surrogate id is the ONLY uniqueness key — there is
-- no compound unique constraint on (series_id, channel, sequence) because overlap-
-- based convergence is enforced at the application layer.
CREATE TABLE IF NOT EXISTS sales_phases (
    id UUID PRIMARY KEY,
    series_id UUID NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    anchor_event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
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

COMMENT ON TABLE sales_phases IS 'A single ticket-sales window for a series. The surrogate id is the only uniqueness key; application-layer overlap matching converges re-discovered phases onto existing rows.';
COMMENT ON COLUMN sales_phases.id IS 'Unique sales phase identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN sales_phases.series_id IS 'Reference to the parent series that owns this sales phase';
COMMENT ON COLUMN sales_phases.anchor_event_id IS 'Earliest covered event at insert time; immutable — never recomputed after initial write. Used as a stable tiebreaker when matching on covered-event overlap.';
COMMENT ON COLUMN sales_phases.method IS 'Sales method: 0=UNSPECIFIED, 1=LOTTERY, 2=FIRST_COME';
COMMENT ON COLUMN sales_phases.channel IS 'Sales channel: 0=UNSPECIFIED, 1=FAN_CLUB, 2=OFFICIAL, 3=PLAYGUIDE, 4=CREDIT_CARD, 5=MOBILE_CARRIER, 6=GENERAL. Concrete play-guide provider names go in provider_name.';
COMMENT ON COLUMN sales_phases.provider_name IS 'Verbatim provider name from the source (e.g. "e+", "ローチケ"). NULL when indeterminate.';
COMMENT ON COLUMN sales_phases.sequence IS 'Ordinal within the same channel for phases that occur in multiple rounds (0-based). Does not uniquely identify a phase.';
COMMENT ON COLUMN sales_phases.apply_start_at IS 'Start of the application or on-sale window (required). Must be known for a phase to be persisted.';
COMMENT ON COLUMN sales_phases.apply_end_at IS 'End of the application window (lottery) or close of on-sale (first-come). NULL when unknown.';
COMMENT ON COLUMN sales_phases.lottery_result_at IS 'When lottery results are announced. NULL for first-come phases or when unknown.';
COMMENT ON COLUMN sales_phases.payment_deadline_at IS 'Payment deadline after winning a lottery. NULL for first-come phases or when unknown.';
COMMENT ON COLUMN sales_phases.url IS 'Direct URL to the sales page for this phase. NULL when not available.';
COMMENT ON COLUMN sales_phases.discovered_at IS 'Timestamp when this sales phase row was first inserted. Used as the first-sight guard: stages whose natural trigger is before discovered_at are not fired.';

-- Event–sales-phase join table (M:N)
-- Links each sales phase to the specific events it covers within the series.
CREATE TABLE IF NOT EXISTS event_sales_phases (
    sales_phase_id UUID NOT NULL REFERENCES sales_phases(id) ON DELETE CASCADE,
    event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    PRIMARY KEY (sales_phase_id, event_id)
);

COMMENT ON TABLE event_sales_phases IS 'M:N join between sales_phases and events. Populated and replaced atomically by the repository on every upsert so incremental coverage growth is handled in-place without duplicating the phase row.';
COMMENT ON COLUMN event_sales_phases.sales_phase_id IS 'Reference to the sales phase';
COMMENT ON COLUMN event_sales_phases.event_id IS 'Reference to the covered event';

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

-- Sales phases indexes
CREATE INDEX IF NOT EXISTS idx_sales_phases_series_id ON sales_phases(series_id);
COMMENT ON INDEX idx_sales_phases_series_id IS 'Optimizes listing all sales phases for a series';

CREATE INDEX IF NOT EXISTS idx_sales_phases_apply_start_at ON sales_phases(apply_start_at);
COMMENT ON INDEX idx_sales_phases_apply_start_at IS 'Supports ListUpcomingByDueWindow queries that filter by apply_start_at range';

-- Event sales phases indexes
CREATE INDEX IF NOT EXISTS idx_event_sales_phases_event_id ON event_sales_phases(event_id);
COMMENT ON INDEX idx_event_sales_phases_event_id IS 'Optimizes lookup of all sales phases covering a given event';

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
