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

// Hype represents the user's enthusiasm tier for a followed artist.
// Values are ordered by ascending enthusiasm: Watch (lowest) to Anywhere (highest).
type Hype string

const (
	// HypeWatch indicates dashboard-only display, no push notifications.
	HypeWatch Hype = "watch"
	// HypeHome indicates notifications only for concerts in the user's home area.
	HypeHome Hype = "home"
	// HypeNearby is reserved for Phase 2 (physical distance based proximity).
	HypeNearby Hype = "nearby"
	// HypeAnywhere indicates notifications for all concerts nationwide (default).
	HypeAnywhere Hype = "anywhere"
)

// FollowedArtist represents an artist with user-specific follow metadata.
type FollowedArtist struct {
	// Artist is the followed artist entity.
	Artist *Artist
	// Hype is the user's enthusiasm tier for this artist.
	Hype Hype
}

// FollowerWithHype represents a follower's user ID, hype level, and home area
// for notification filtering decisions.
type FollowerWithHype struct {
	// UserID is the internal UUID of the follower.
	UserID string
	// Hype is the follower's enthusiasm tier for the artist.
	Hype Hype
	// HomeLevel1 is the ISO 3166-2 subdivision code of the user's home area.
	// Empty when the user has not set a home area.
	HomeLevel1 string
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

	// ListByMBIDs retrieves artists matching the provided MusicBrainz IDs.
	// Returns only artists that exist in the database. The result order
	// matches the input mbids order. Unknown MBIDs are silently skipped.
	//
	// # Possible errors:
	//
	//   - Internal: database query failure.
	ListByMBIDs(ctx context.Context, mbids []string) ([]*Artist, error)

	// UpdateName updates the display name of an artist identified by ID.
	//
	// # Possible errors:
	//
	//   - NotFound: no artist exists with the provided ID.
	//   - Internal: database execution failure.
	UpdateName(ctx context.Context, id string, name string) error

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

	// SetHype updates the enthusiasm tier for a followed artist.
	//
	// # Possible errors:
	//
	//   - NotFound: the user is not following the specified artist.
	//   - Internal: database execution failure.
	SetHype(ctx context.Context, userID, artistID string, hype Hype) error

	// ListFollowed retrieves all artists followed by a specific user,
	// enriched with per-user hype metadata.
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

	// ListFollowersWithHype retrieves all followers of an artist along with their
	// hype level and home area for notification filtering decisions.
	// Returns an empty slice (no error) when no users follow the artist.
	//
	// # Possible errors:
	//
	//   - Internal: database query failure.
	ListFollowersWithHype(ctx context.Context, artistID string) ([]*FollowerWithHype, error)
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
	// When limit is greater than zero, the result is capped to that many entries;
	// otherwise the external service's default is used.
	//
	// # Possible errors:
	//
	//   - NotFound: the artist record is not recognized by the external searcher.
	//   - Unavailable: external service failure.
	ListSimilar(ctx context.Context, artist *Artist, limit int32) ([]*Artist, error)

	// ListTop retrieves the most popular artists based on charts, geographic region, or genre tag.
	// When limit is greater than zero, the result is capped to that many entries;
	// otherwise the external service's default is used.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: the provided country code is invalid.
	//   - Unavailable: external service failure.
	ListTop(ctx context.Context, country string, tag string, limit int32) ([]*Artist, error)
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
