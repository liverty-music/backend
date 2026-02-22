-- +goose Up
-- Create venues table
CREATE TABLE venues (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Create artists table (updating/extending existing if any, but following fresh design)
CREATE TABLE artists (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name TEXT NOT NULL,
    spotify_id VARCHAR(100) UNIQUE,
    musicbrainz_id VARCHAR(100) UNIQUE,
    genres TEXT[],
    country VARCHAR(3),
    image_url TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_artists_name ON artists(name);

-- Create artist_media table
CREATE TABLE artist_media (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    url TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_artist_media_artist_id ON artist_media(artist_id);

-- Create concerts table
CREATE TABLE concerts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    artist_id UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    venue_id UUID NOT NULL REFERENCES venues(id),
    title TEXT NOT NULL,
    date DATE NOT NULL,
    start_time TIME NOT NULL,
    open_time TIME,
    status VARCHAR(20) DEFAULT 'scheduled',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_concerts_artist_id ON concerts(artist_id);
CREATE INDEX idx_concerts_venue_id ON concerts(venue_id);
