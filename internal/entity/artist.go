// Package entity defines core domain entities and business logic interfaces.
package entity

import (
	"context"
	"time"
)

// Artist represents a musical artist or group.
type Artist struct {
	ID            string
	Name          string
	SpotifyID     string
	MusicBrainzID string
	Genres        []string
	Country       string
	ImageURL      string
	Media         []*Media
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

// Media represents a social media or web link for an artist.
type Media struct {
	ID        string
	ArtistID  string
	Type      MediaType
	URL       string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// MediaType defines the type of media link.
type MediaType string

// MediaType values for artist media links.
const (
	// MediaTypeWeb represents a general website link.
	MediaTypeWeb       MediaType = "WEB"
	MediaTypeTwitter   MediaType = "TWITTER"
	MediaTypeInstagram MediaType = "INSTAGRAM"
)

// ArtistRepository defines the data access interface for Artists.
type ArtistRepository interface {
	Create(ctx context.Context, artist *Artist) error
	List(ctx context.Context) ([]*Artist, error)
	Get(ctx context.Context, id string) (*Artist, error)

	// Media operations
	AddMedia(ctx context.Context, media *Media) error
	DeleteMedia(ctx context.Context, mediaID string) error
}
