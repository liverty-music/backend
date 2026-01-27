-- Schema for Liverty Music Backend
-- Manually maintained to align with entity definitions
-- Following 2026 PostgreSQL standards (UUIDv7, TEXT types, TIMESTAMPTZ)

-- ============================================================================
-- TABLES
-- ============================================================================

-- Users table
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    email TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    preferred_language TEXT DEFAULT 'en',
    country TEXT,
    time_zone TEXT,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE users IS 'Registered users who subscribe to concert notifications for their favorite artists';
COMMENT ON COLUMN users.id IS 'Unique user identifier (UUIDv7)';
COMMENT ON COLUMN users.email IS 'User email address, used for authentication and notifications';
COMMENT ON COLUMN users.name IS 'User display name';
COMMENT ON COLUMN users.preferred_language IS 'ISO 639-1 language code for notification localization (e.g., en, ja)';
COMMENT ON COLUMN users.country IS 'ISO 3166-1 alpha-3 country code for geographic filtering';
COMMENT ON COLUMN users.time_zone IS 'IANA time zone identifier for scheduling notifications';
COMMENT ON COLUMN users.is_active IS 'Whether the user account is active and can receive notifications';
COMMENT ON COLUMN users.created_at IS 'Timestamp when the user was created';
COMMENT ON COLUMN users.updated_at IS 'Timestamp when the user was last updated';

-- Artists table
CREATE TABLE IF NOT EXISTS artists (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    name TEXT NOT NULL,
    spotify_id TEXT UNIQUE,
    musicbrainz_id TEXT UNIQUE,
    genres TEXT[],
    country TEXT,
    image_url TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE artists IS 'Musical artists or groups that users can subscribe to for concert notifications';
COMMENT ON COLUMN artists.id IS 'Unique artist identifier (UUIDv7)';
COMMENT ON COLUMN artists.name IS 'Artist or band name as displayed to users';
COMMENT ON COLUMN artists.spotify_id IS 'Spotify unique identifier for cross-platform data enrichment';
COMMENT ON COLUMN artists.musicbrainz_id IS 'MusicBrainz unique identifier for authoritative music metadata';
COMMENT ON COLUMN artists.genres IS 'Array of music genres associated with the artist';
COMMENT ON COLUMN artists.country IS 'ISO 3166-1 alpha-3 country code of artist origin';
COMMENT ON COLUMN artists.image_url IS 'URL to artist profile image or album art';
COMMENT ON COLUMN artists.created_at IS 'Timestamp when the artist was added to the system';
COMMENT ON COLUMN artists.updated_at IS 'Timestamp when artist data was last updated';

-- Artist media (social links, websites, etc.)
CREATE TABLE IF NOT EXISTS artist_media (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    url TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE artist_media IS 'Social media links and web presence for artists (Twitter, Instagram, official websites)';
COMMENT ON COLUMN artist_media.id IS 'Unique media link identifier (UUIDv7)';
COMMENT ON COLUMN artist_media.artist_id IS 'Reference to the artist this media belongs to';
COMMENT ON COLUMN artist_media.type IS 'Type of media link (WEB, TWITTER, INSTAGRAM)';
COMMENT ON COLUMN artist_media.url IS 'Full URL to the artist social media profile or website';
COMMENT ON COLUMN artist_media.created_at IS 'Timestamp when the media link was added';
COMMENT ON COLUMN artist_media.updated_at IS 'Timestamp when the media link was last updated';

-- Venues table
CREATE TABLE IF NOT EXISTS venues (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE venues IS 'Physical locations where concerts and live events are hosted';
COMMENT ON COLUMN venues.id IS 'Unique venue identifier (UUIDv7)';
COMMENT ON COLUMN venues.name IS 'Venue name as displayed to users';
COMMENT ON COLUMN venues.created_at IS 'Timestamp when the venue was added to the system';
COMMENT ON COLUMN venues.updated_at IS 'Timestamp when venue data was last updated';

-- Concerts table
CREATE TABLE IF NOT EXISTS concerts (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    venue_id UUID NOT NULL REFERENCES venues(id),
    title TEXT NOT NULL,
    date DATE NOT NULL,
    start_time TIME NOT NULL,
    open_time TIME,
    status TEXT NOT NULL DEFAULT 'scheduled',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE concerts IS 'Scheduled live music events with artist, venue, and timing information';
COMMENT ON COLUMN concerts.id IS 'Unique concert identifier (UUIDv7)';
COMMENT ON COLUMN concerts.artist_id IS 'Reference to the performing artist';
COMMENT ON COLUMN concerts.venue_id IS 'Reference to the venue hosting the concert';
COMMENT ON COLUMN concerts.title IS 'Concert or tour title as displayed to users';
COMMENT ON COLUMN concerts.date IS 'Date of the concert event';
COMMENT ON COLUMN concerts.start_time IS 'Concert start time (local to venue)';
COMMENT ON COLUMN concerts.open_time IS 'Doors open time (local to venue), if available';
COMMENT ON COLUMN concerts.status IS 'Concert status: scheduled, canceled, or completed';
COMMENT ON COLUMN concerts.created_at IS 'Timestamp when the concert was added to the system';
COMMENT ON COLUMN concerts.updated_at IS 'Timestamp when concert data was last updated';

-- User-Artist subscriptions
CREATE TABLE IF NOT EXISTS user_artist_subscriptions (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE user_artist_subscriptions IS 'User subscriptions to artists for automated concert notifications';
COMMENT ON COLUMN user_artist_subscriptions.id IS 'Unique subscription identifier (UUIDv7)';
COMMENT ON COLUMN user_artist_subscriptions.user_id IS 'Reference to the subscribing user';
COMMENT ON COLUMN user_artist_subscriptions.artist_id IS 'Reference to the artist being followed';
COMMENT ON COLUMN user_artist_subscriptions.is_active IS 'Whether the subscription is active and should trigger notifications';
COMMENT ON COLUMN user_artist_subscriptions.created_at IS 'Timestamp when the subscription was created';
COMMENT ON COLUMN user_artist_subscriptions.updated_at IS 'Timestamp when the subscription status was last modified';

-- Notifications table
CREATE TABLE IF NOT EXISTS notifications (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    concert_id UUID REFERENCES concerts(id) ON DELETE SET NULL,
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

-- ============================================================================
-- INDEXES
-- ============================================================================

CREATE INDEX IF NOT EXISTS idx_artists_name ON artists(name);
COMMENT ON INDEX idx_artists_name IS 'Speeds up artist search by name';

CREATE INDEX IF NOT EXISTS idx_artist_media_artist_id ON artist_media(artist_id);
COMMENT ON INDEX idx_artist_media_artist_id IS 'Optimizes retrieval of all media for a specific artist';

CREATE INDEX IF NOT EXISTS idx_concerts_artist_id ON concerts(artist_id);
COMMENT ON INDEX idx_concerts_artist_id IS 'Optimizes listing concerts by artist';

CREATE INDEX IF NOT EXISTS idx_concerts_venue_id ON concerts(venue_id);
COMMENT ON INDEX idx_concerts_venue_id IS 'Optimizes listing concerts by venue';

CREATE INDEX IF NOT EXISTS idx_concerts_date ON concerts(date);
COMMENT ON INDEX idx_concerts_date IS 'Optimizes date-based concert queries and upcoming event searches';

CREATE INDEX IF NOT EXISTS idx_user_artist_subscriptions_user_id ON user_artist_subscriptions(user_id);
COMMENT ON INDEX idx_user_artist_subscriptions_user_id IS 'Optimizes retrieval of all artist subscriptions for a user';

CREATE INDEX IF NOT EXISTS idx_user_artist_subscriptions_artist_id ON user_artist_subscriptions(artist_id);
COMMENT ON INDEX idx_user_artist_subscriptions_artist_id IS 'Optimizes finding all subscribers for an artist (for notification fan-out)';

CREATE INDEX IF NOT EXISTS idx_notifications_user_id ON notifications(user_id);
COMMENT ON INDEX idx_notifications_user_id IS 'Optimizes retrieval of notification history for a user';

CREATE INDEX IF NOT EXISTS idx_notifications_status ON notifications(status);
COMMENT ON INDEX idx_notifications_status IS 'Optimizes filtering notifications by delivery status (e.g., pending for batch processing)';

CREATE INDEX IF NOT EXISTS idx_notifications_scheduled_at ON notifications(scheduled_at);
COMMENT ON INDEX idx_notifications_scheduled_at IS 'Optimizes time-based notification scheduling and batch sending queries';
