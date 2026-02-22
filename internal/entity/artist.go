// Package entity defines core domain entities and business logic interfaces.
package entity

import (
	"context"

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
}

// NewArtist creates a new Artist with an auto-generated UUIDv7 ID.
func NewArtist(name, mbid string) *Artist {
	return &Artist{
		ID:   newID(),
		Name: name,
		MBID: mbid,
	}
}

func newID() string {
	id, _ := uuid.NewV7()
	return id.String()
}

// PassionLevel represents the user's enthusiasm tier for a followed artist.
type PassionLevel string

const (
	// PassionLevelMustGo indicates the user will travel anywhere for this artist.
	PassionLevelMustGo PassionLevel = "must_go"
	// PassionLevelLocalOnly indicates interest in local events only (default).
	PassionLevelLocalOnly PassionLevel = "local_only"
	// PassionLevelKeepAnEye indicates dashboard-only display, no push notifications.
	PassionLevelKeepAnEye PassionLevel = "keep_an_eye"
)

// FollowedArtist represents an artist with user-specific follow metadata.
type FollowedArtist struct {
	// Artist is the followed artist entity.
	Artist *Artist
	// PassionLevel is the user's enthusiasm tier for this artist.
	PassionLevel PassionLevel
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
	// Create persists one or more artist records in the database using bulk upsert.
	// Artists with matching MBIDs are deduplicated (ON CONFLICT DO NOTHING).
	// Returns all artists (both newly inserted and pre-existing) with valid database IDs.
	//
	// # Possible errors:
	//
	//   - Internal: database connection or execution failure.
	Create(ctx context.Context, artists ...*Artist) ([]*Artist, error)

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

	// SetPassionLevel updates the enthusiasm tier for a followed artist.
	//
	// # Possible errors:
	//
	//   - NotFound: the user is not following the specified artist.
	//   - Internal: database execution failure.
	SetPassionLevel(ctx context.Context, userID, artistID string, level PassionLevel) error

	// ListFollowed retrieves all artists followed by a specific user,
	// enriched with per-user passion level metadata.
	//
	// # Possible errors:
	//
	//   - Internal: database query failure.
	ListFollowed(ctx context.Context, userID string) ([]*FollowedArtist, error)

	// ListAllFollowed retrieves all distinct artists followed by any user.
	//
	// # Possible errors:
	//
	//   - Internal: database query failure.
	ListAllFollowed(ctx context.Context) ([]*Artist, error)

	// ListFollowers retrieves all users who are following the given artist.
	// Returns an empty slice (no error) when no users follow the artist.
	//
	// # Possible errors:
	//
	//   - Internal: database query failure.
	ListFollowers(ctx context.Context, artistID string) ([]*User, error)
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

	// ListTop retrieves the most popular artists based on charts, geographic region, or genre tag.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: the provided country code is invalid.
	//   - Unavailable: external service failure.
	ListTop(ctx context.Context, country string, tag string) ([]*Artist, error)
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

// OfficialSiteResolver resolves an artist's official site URL from an external catalog.
type OfficialSiteResolver interface {
	// ResolveOfficialSiteURL returns the primary official homepage URL for the artist
	// identified by the given MBID. Returns an empty string (no error) when no active
	// official homepage relation is found.
	//
	// # Possible errors:
	//
	//   - Unavailable: the external catalog service is down or rate-limited.
	//   - Internal: unexpected failure during resolution.
	ResolveOfficialSiteURL(ctx context.Context, mbid string) (string, error)
}
