// Package entity defines core domain entities and business logic interfaces.
package entity

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Artist represents a musical artist or group recorded in the system.
//
// Artist instances are tied to canonical MusicBrainz records via their [Artist.MBID].
// See [ArtistProto] for the wire representation.
//
// [ArtistProto]: https://github.com/liverty-music/specification/blob/main/proto/liverty_music/entity/v1/artist.proto
type Artist struct {
	// ID is the unique internal identifier for the artist (UUIDv7).
	ID string
	// Name is the display name of the artist or band.
	Name string
	// MBID is the canonical MusicBrainz Identifier for identity normalization.
	MBID string
	// CreateTime is the timestamp when the artist record was first created.
	CreateTime time.Time
}

// NewID generates a new unique identifier for an artist (UUID v7).
func NewID() string {
	id, _ := uuid.NewV7()
	return id.String()
}

// OfficialSite represents the verified official website or media link for an artist.
//
// Each artist is restricted to a single primary official site in the current version.
// See [OfficialSiteProto] for the wire representation.
//
// [OfficialSiteProto]: https://github.com/liverty-music/specification/blob/main/proto/liverty_music/entity/v1/artist.proto
type OfficialSite struct {
	// ID is the unique identifier for the official site record.
	ID string
	// ArtistID is the foreign key reference to the [Artist].
	ArtistID string
	// URL is the validated HTTPS address of the website.
	URL string
}

// ArtistRepository defines the persistence layer operations for artist entities.
type ArtistRepository interface {
	// Create persists a new artist record in the database.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: the artist name or MBID is empty.
	//   - AlreadyExists: an artist with the same MBID already exists.
	//   - Internal: database connection or execution failure.
	Create(ctx context.Context, artist *Artist) error

	// List retrieves all registered artists sorted by name.
	//
	// # Possible errors:
	//
	//   - Internal: database query failure.
	List(ctx context.Context) ([]*Artist, error)

	// Get retrieves a specific artist by their internal UUID.
	//
	// # Possible errors:
	//
	//   - NotFound: no artist exists with the provided ID.
	//   - Internal: database query failure.
	Get(ctx context.Context, id string) (*Artist, error)

	// GetByMBID retrieves an artist using their canonical MusicBrainz ID.
	//
	// # Possible errors:
	//
	//   - NotFound: no artist exists with the provided MBID.
	//   - Internal: database query failure.
	GetByMBID(ctx context.Context, mbid string) (*Artist, error)

	// Official Site operations

	// CreateOfficialSite registers a new website link for an artist.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: the URL is malformed or empty.
	//   - AlreadyExists: the artist already has an official site record.
	//   - Internal: database execution failure.
	CreateOfficialSite(ctx context.Context, site *OfficialSite) error

	// GetOfficialSite retrieves the verified website for a specific artist.
	//
	// # Possible errors:
	//
	//   - NotFound: the artist exists but has no official site registered.
	//   - Internal: database query failure.
	GetOfficialSite(ctx context.Context, artistID string) (*OfficialSite, error)

	// Follow records a user's interest in an artist for notification purposes.
	//
	// # Possible errors:
	//
	//   - NotFound: the artist or user does not exist.
	//   - AlreadyExists: the user is already following this artist.
	//   - Internal: database execution failure.
	Follow(ctx context.Context, userID, artistID string) error

	// Unfollow removes the subscription between a user and an artist.
	//
	// # Possible errors:
	//
	//   - NotFound: the follow relationship does not exist.
	//   - Internal: database execution failure.
	Unfollow(ctx context.Context, userID, artistID string) error

	// ListFollowed retrieves all artists followed by a specific user.
	//
	// # Possible errors:
	//
	//   - Internal: database query failure.
	ListFollowed(ctx context.Context, userID string) ([]*Artist, error)

	// ListAllFollowed retrieves all distinct artists followed by any user.
	//
	// # Possible errors:
	//
	//   - Internal: database query failure.
	ListAllFollowed(ctx context.Context) ([]*Artist, error)
}

// ArtistSearcher defines discovery operations for finding artists in external catalogs.
type ArtistSearcher interface {
	// Search finds artists matching the provided name or keyword.
	//
	// # Possible errors:
	//
	//   - Unavailable: external search service is down or rate-limited.
	//   - Internal: unexpected error during search processing.
	Search(ctx context.Context, query string) ([]*Artist, error)

	// ListSimilar retrieves artists with musical styles similar to the input artist.
	//
	// # Possible errors:
	//
	//   - NotFound: the artist record is not recognized by the external searcher.
	//   - Unavailable: external service failure.
	ListSimilar(ctx context.Context, artist *Artist) ([]*Artist, error)

	// ListTop retrieves the most popular artists based on charts or geographic region.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: the provided country code is invalid.
	//   - Unavailable: external service failure.
	ListTop(ctx context.Context, country string) ([]*Artist, error)
}

// ArtistIdentityManager handles canonical identity resolution for artists.
type ArtistIdentityManager interface {
	// GetArtist resolves an MBID into a complete, canonical Artist entity.
	//
	// # Possible errors:
	//
	//   - NotFound: the MBID does not correspond to a valid artist record.
	//   - Unavailable: identity provider service is down.
	GetArtist(ctx context.Context, mbid string) (*Artist, error)
}
