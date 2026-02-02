// Package entity defines core domain entities and business logic interfaces.
package entity

import (
	"context"
	"time"
)

// Artist represents a musical artist or group.
//
// Corresponds to liverty_music.entity.v1.Artist.
type Artist struct {
	// ID is the unique identifier for the artist (UUID).
	ID string
	// Name is the name of the artist.
	Name string
	// CreateTime is the timestamp when the artist was created.
	CreateTime time.Time
	// UpdateTime is the timestamp when the artist was last updated.
	UpdateTime time.Time
}

// OfficialSite represents the official website for an artist.
//
// Corresponds to liverty_music.entity.v1.OfficialSite.
type OfficialSite struct {
	// ID is the unique identifier for the official site.
	ID string
	// ArtistID is the ID of the artist this site belongs to.
	ArtistID string
	// URL is the URL of the official site.
	URL string
	// CreateTime is the timestamp when the site was created.
	CreateTime time.Time
	// UpdateTime is the timestamp when the site was last updated.
	UpdateTime time.Time
}

// ArtistRepository defines the data access interface for Artists.
type ArtistRepository interface {
	// Create creates a new artist.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the artist name is empty.
	Create(ctx context.Context, artist *Artist) error

	// List returns a list of all artists.
	List(ctx context.Context) ([]*Artist, error)

	// Get retrieves an artist by ID.
	//
	// # Possible errors
	//
	//  - NotFound: If the artist does not exist.
	Get(ctx context.Context, id string) (*Artist, error)

	// Official Site operations

	// CreateOfficialSite creates a new official site for an artist.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the URL is empty.
	CreateOfficialSite(ctx context.Context, site *OfficialSite) error

	// GetOfficialSite retrieves the official site for an artist.
	//
	// # Possible errors
	//
	//  - NotFound: If the official site does not exist.
	GetOfficialSite(ctx context.Context, artistID string) (*OfficialSite, error)
}
