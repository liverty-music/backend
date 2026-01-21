package entity

import (
	"context"
	"time"
)

// Artist represents an artist/musician domain entity.
type Artist struct {
	ID            string
	Name          string
	SpotifyID     string
	MusicBrainzID string
	Genres        []string
	Country       string
	ImageURL      string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// NewArtist represents data for creating a new artist.
type NewArtist struct {
	Name          string
	SpotifyID     string
	MusicBrainzID string
	Genres        []string
	Country       string
	ImageURL      string
}

// ArtistRepository defines the interface for artist data access.
type ArtistRepository interface {
	Create(ctx context.Context, params *NewArtist) (*Artist, error)
	Get(ctx context.Context, id string) (*Artist, error)
	GetBySpotifyID(ctx context.Context, spotifyID string) (*Artist, error)
	Update(ctx context.Context, id string, params *NewArtist) (*Artist, error)
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, limit, offset int) ([]*Artist, error)
}
