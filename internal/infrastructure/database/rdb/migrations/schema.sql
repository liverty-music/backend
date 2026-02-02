-- liverty-music backend database schema
-- This schema follows Clean Architecture principles by separating
-- user management, artist discovery, and concert notifications.

-- Users table
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    email TEXT UNIQUE NOT NULL,
    nickname TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE users IS 'User profiles and authentication data';
COMMENT ON COLUMN users.id IS 'Unique user identifier (UUIDv7)';
COMMENT ON COLUMN users.email IS 'Primary contact and login identifier';
COMMENT ON COLUMN users.nickname IS 'User display name';
COMMENT ON COLUMN users.created_at IS 'Timestamp when the user was created';
COMMENT ON COLUMN users.updated_at IS 'Timestamp when the user was last updated';

-- Artists table
CREATE TABLE IF NOT EXISTS artists (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE artists IS 'Musical artists or groups that users can subscribe to for concert notifications';
COMMENT ON COLUMN artists.id IS 'Unique artist identifier (UUIDv7)';
COMMENT ON COLUMN artists.name IS 'Artist or band name as displayed to users';

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
COMMENT ON COLUMN artist_official_site.created_at IS 'Timestamp when the record was created';
COMMENT ON COLUMN artist_official_site.updated_at IS 'Timestamp when the record was last updated';

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
    venue_id UUID NOT NULL REFERENCES venues(id) ON DELETE CASCADE,
    date DATE NOT NULL,
    start_time TIME NOT NULL,
    open_time TIME,
    title TEXT NOT NULL,
    source_url TEXT,
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

COMMENT ON TABLE notifications IS 'User notification log for concert alerts and platform updates';
COMMENT ON COLUMN notifications.id IS 'Unique notification identifier (UUIDv7)';
COMMENT ON COLUMN notifications.user_id IS 'Target user for the notification';
COMMENT ON COLUMN notifications.artist_id IS 'Related artist (if applicable)';
COMMENT ON COLUMN notifications.concert_id IS 'Related concert (if applicable)';
COMMENT ON COLUMN notifications.type IS 'Notification type (ALERT, FEATURE_UPDATE, SYSTEM)';
COMMENT ON COLUMN notifications.title IS 'Short heading for the notification';
COMMENT ON COLUMN notifications.message IS 'Detailed notification body text';
COMMENT ON COLUMN notifications.language IS 'Content language (ISO 639-1)';
COMMENT ON COLUMN notifications.status IS 'Delivery status (pending, sent, failed)';
COMMENT ON COLUMN notifications.scheduled_at IS 'When the notification is planned to be sent';
COMMENT ON COLUMN notifications.sent_at IS 'Actual delivery timestamp';
COMMENT ON COLUMN notifications.created_at IS 'Record creation timestamp';
COMMENT ON COLUMN notifications.updated_at IS 'Last update timestamp';

-- Posts table
CREATE TABLE IF NOT EXISTS posts (
    id UUID PRIMARY KEY DEFAULT uuidv7(),
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE posts IS 'Social posts or updates from artists (for future features)';
COMMENT ON COLUMN posts.id IS 'Unique post identifier (UUIDv7)';
COMMENT ON COLUMN posts.artist_id IS 'Author artist identifier';
COMMENT ON COLUMN posts.content IS 'Post content body';

-- Indexes
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
COMMENT ON INDEX idx_users_email IS 'Speeds up user lookup by email during authentication';

CREATE INDEX IF NOT EXISTS idx_artists_name ON artists(name);
COMMENT ON INDEX idx_artists_name IS 'Speeds up artist search by name';

CREATE INDEX IF NOT EXISTS idx_artist_official_site_artist_id ON artist_official_site(artist_id);
COMMENT ON INDEX idx_artist_official_site_artist_id IS 'Optimizes retrieval of official site for an artist';

CREATE INDEX IF NOT EXISTS idx_concerts_artist_id ON concerts(artist_id);
COMMENT ON INDEX idx_concerts_artist_id IS 'Optimizes listing concerts by artist';

CREATE INDEX IF NOT EXISTS idx_concerts_date ON concerts(date);
COMMENT ON INDEX idx_concerts_date IS 'Speeds up date-based concert searches and calendar views';

CREATE INDEX IF NOT EXISTS idx_user_artist_subscriptions_composite ON user_artist_subscriptions(user_id, artist_id);
COMMENT ON INDEX idx_user_artist_subscriptions_composite IS 'Optimizes subscription check for a user-artist pair';

CREATE INDEX IF NOT EXISTS idx_notifications_user_status ON notifications(user_id, status);
COMMENT ON INDEX idx_notifications_user_status IS 'Optimizes notification inbox retrieval for users';

CREATE INDEX IF NOT EXISTS idx_notifications_scheduled_at ON notifications(scheduled_at);
COMMENT ON INDEX idx_notifications_scheduled_at IS 'Speeds up retrieval of notifications due for delivery';
