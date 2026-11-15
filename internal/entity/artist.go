// Package entity defines core domain entities and business logic interfaces.
package entity

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"time"
	"unicode/utf8"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
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
	// Fanart holds cached community-curated images from fanart.tv.
	// nil when image data has not been fetched yet.
	Fanart *Fanart
	// FanartSyncTime is the timestamp of the last successful fanart.tv sync.
	// nil when no sync has occurred.
	FanartSyncTime *time.Time
	// OfficialSiteCheckTime is when the artist's official site was last
	// checked in MusicBrainz, whatever the check found.
	// nil when the official site has never been checked.
	OfficialSiteCheckTime *time.Time
	// ActivityEnded is true when the music catalog marks the artist's activity
	// as ended (a group dissolved, a solo artist died). It is false when the
	// catalog records no end, and before the artist is first read from the
	// catalog. A hiatus is not an ended activity.
	ActivityEnded bool
	// OrganizerID is the Organizer that represents the artist; nil when no
	// Organizer does. An artist has at most one.
	OrganizerID *string
}

// IsActive reports whether the artist is still active, so that it is suggested,
// searched for concerts and for ticket sales: its activity has not ended in the
// music catalog. An artist whose activity has ended (a dissolved group, a solo
// artist who died) will not announce anything new.
func (a *Artist) IsActive() bool {
	return !a.ActivityEnded
}

// CheckSearchable reports whether new concerts can be searched for the
// artist: the search needs its name and its MBID, which the concerts it finds
// are linked by. An artist without them is a data problem only an admin can
// fix, so the search must not run (and spend its quota) for it.
//
// # Possible errors
//
//   - Internal: the artist has no name or no MBID.
func (a *Artist) CheckSearchable() error {
	if a.Name == "" || a.MBID == "" {
		return apperr.New(codes.Internal, "artist is missing required fields for discovery",
			slog.String("artist_id", a.ID),
			slog.Bool("name_empty", a.Name == ""),
			slog.Bool("mbid_empty", a.MBID == ""),
		)
	}
	return nil
}

// FanartDueAt reports whether the artist's images are due for a sync at now:
// never synced, or last synced before now minus FanartStaleAge. A sync exactly
// at that instant is not yet due. It does not look at followers: the
// repository's due query also needs one for an artist that was synced before
// (an artist nobody follows keeps the images of its last sync), and the parity
// test checks both.
func (a *Artist) FanartDueAt(now time.Time) bool {
	return a.FanartSyncTime == nil || a.FanartSyncTime.Before(now.Add(-FanartStaleAge))
}

// Refresh cadences and failure stops of the catalog-backed refreshes. They are
// policy, so they live with the entity that the refreshes update.
const (
	// OfficialSiteRefreshAge is how old an official site check must be for the
	// artist to be checked again.
	OfficialSiteRefreshAge = 7 * 24 * time.Hour
	// OfficialSiteRefreshCycleDays is the number of daily runs over which every
	// followed artist is refreshed once; it sizes each run's batch.
	OfficialSiteRefreshCycleDays = 7
	// OfficialSiteRefreshMaxConsecutiveFailures stops a run after this many
	// artists fail in a row, treating it as a systemic catalog failure.
	OfficialSiteRefreshMaxConsecutiveFailures = 3
	// FanartStaleAge is how old a fanart sync must be for a followed artist's
	// images to be synced again.
	FanartStaleAge = 7 * 24 * time.Hour
	// ArtistRefreshSlots is the number of days in the artist refresh rotation:
	// each daily run reads one slot, so every artist is read once per cycle.
	ArtistRefreshSlots = 30
	// ArtistRefreshMaxConsecutiveFailures stops a refresh run after this many
	// artists fail in a row, treating it as a systemic catalog failure.
	ArtistRefreshMaxConsecutiveFailures = 3
)

// NewArtist creates a new Artist with an auto-generated UUIDv7 ID.
func NewArtist(name, mbid string) *Artist {
	return &Artist{
		ID:   NewID(),
		Name: name,
		MBID: mbid,
	}
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

// maxOfficialSiteURLLength is the longest official site URL accepted, in characters.
const maxOfficialSiteURLLength = 2048

// ValidateOfficialSiteURL checks that raw is a valid official site URL: an
// absolute URI of 1 to 2048 characters. It mirrors the protovalidate
// constraint on liverty_music.entity.v1.Url. It fails with InvalidArgument,
// which callers return as it is.
func ValidateOfficialSiteURL(raw string) error {
	if raw == "" {
		return apperr.New(codes.InvalidArgument, "official site url must not be empty")
	}
	if n := utf8.RuneCountInString(raw); n > maxOfficialSiteURLLength {
		return apperr.New(codes.InvalidArgument, fmt.Sprintf("official site url must be at most %d characters, got %d", maxOfficialSiteURLLength, n))
	}
	u, err := url.Parse(raw)
	if err != nil {
		return apperr.Wrap(err, codes.InvalidArgument, "official site url is not a valid URI")
	}
	if u.Scheme == "" {
		return apperr.New(codes.InvalidArgument, fmt.Sprintf("official site url must be an absolute URI, got %q", raw))
	}
	return nil
}

// FilterArtistsByMBID removes artists with an empty MBID and deduplicates the
// remaining entries by MBID, keeping the first occurrence of each.
func FilterArtistsByMBID(artists []*Artist) []*Artist {
	seen := make(map[string]struct{})
	result := make([]*Artist, 0, len(artists))
	for _, a := range artists {
		if a.MBID == "" {
			continue
		}
		if _, ok := seen[a.MBID]; ok {
			continue
		}
		seen[a.MBID] = struct{}{}
		result = append(result, a)
	}
	return result
}

// NewOfficialSite creates a new OfficialSite for the given artist with an
// auto-generated UUIDv7 ID.
func NewOfficialSite(artistID, url string) *OfficialSite {
	return &OfficialSite{
		ID:       NewID(),
		ArtistID: artistID,
		URL:      url,
	}
}

// ArtistRepository defines the persistence layer operations for artist entities.
type ArtistRepository interface {
	// Create registers every given artist whose MBID is not yet registered
	// and returns, for each given artist in input order, the registered
	// artist with that MBID. An artist whose MBID is already registered is
	// returned as registered, unchanged, and is not registered a second time.
	// A new artist keeps the id it was given, or gets a fresh id when it has
	// none. All of the new artists are registered or none of them.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: an artist has no MBID or an MBID that is not 36
	//     characters long. Nothing is registered.
	//   - Unavailable: an announcement could not be recorded; nothing is
	//     registered.
	//   - Internal: database connection or execution failure.
	//
	// # Announces
	//
	// ARTIST.created with each Artist this call inserted, together with the
	// insert. Nothing for an MBID already registered, also by a concurrent call.
	Create(ctx context.Context, artists ...*Artist) ([]*Artist, error)

	// List retrieves all registered artists. The order is unspecified.
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

	// UpdateActivityEnded sets whether the artist's activity has ended, as last
	// read from the music catalog. Setting the value the artist already has
	// succeeds and changes nothing.
	//
	// # Possible errors:
	//
	//   - NotFound: no artist exists with the provided ID.
	//   - Internal: database execution failure.
	UpdateActivityEnded(ctx context.Context, id string, ended bool) error

	// ListRefreshDue returns every artist in one of slots equal shares of all
	// registered artists, followed or not, ordered by id. An artist's share is
	// derived from its id alone, so it stays in the same slot on every call
	// with the same slots. slot runs from 0 to slots-1.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: slots is not positive, or slot is outside 0 to slots-1.
	//   - Internal: database query failure.
	ListRefreshDue(ctx context.Context, slots, slot int) ([]*Artist, error)

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

	// Fanart operations

	// UpdateFanart replaces the artist's fanart with the given fanart and
	// sets the time of the image check to syncTime. When fanart is nil, the
	// artist is left with no fanart, removing any earlier images, and the
	// check time is still set.
	//
	// # Possible errors:
	//
	//   - NotFound: no artist exists with the provided ID.
	//   - Internal: database execution failure.
	UpdateFanart(ctx context.Context, id string, fanart *Fanart, syncTime time.Time) error

	// ListStaleOrMissingFanart returns the artists whose images are due for a
	// check at now: artists never checked, whoever registered them, and
	// artists followed by at least one user whose last check is older than
	// staleAge. An artist nobody follows keeps the images of its last
	// check. Artists are returned never-checked first and then oldest check
	// first, each at most once, and at most limit artists.
	//
	// # Possible errors:
	//
	//   - Internal: database query failure.
	ListStaleOrMissingFanart(ctx context.Context, now time.Time, staleAge time.Duration, limit int) ([]*Artist, error)

	// Official site refresh operations

	// ListStaleOfficialSite returns the artists followed by at least one user
	// whose official site is due for a check at now: never checked, or last
	// checked longer ago than age. Artists are returned whether or not they have an
	// official site, never-checked first and then oldest check first, each at
	// most once, and at most limit artists.
	//
	// # Possible errors:
	//
	//   - Internal: database query failure.
	ListStaleOfficialSite(ctx context.Context, now time.Time, age time.Duration, limit int) ([]*Artist, error)

	// UpdateOfficialSiteURL replaces the URL of the official site stored for
	// the artist, keeping the site's id. The stored site is unchanged on failure.
	//
	// # Possible errors:
	//
	//   - InvalidArgument: the URL is not a valid official site URL.
	//   - NotFound: the artist has no official site, or no artist has the id.
	//   - Internal: database execution failure.
	UpdateOfficialSiteURL(ctx context.Context, artistID, url string) error

	// MarkOfficialSiteChecked records checkTime as the artist's official site
	// check time, whether or not the artist has an official site.
	//
	// # Possible errors:
	//
	//   - NotFound: no artist exists with the provided ID.
	//   - Internal: database execution failure.
	MarkOfficialSiteChecked(ctx context.Context, artistID string, checkTime time.Time) error
}

// ArtistSearcher defines discovery operations for finding artists in external catalogs.
type ArtistSearcher interface {
	// Search finds artists matching the provided name or keyword.
	//
	// # Possible errors:
	//
	//   - NotFound: the external catalog has no matching artist record.
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
	//   - NotFound: the external catalog returns no results for the given parameters.
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

// OfficialSiteCatalog reads what an external catalog records about an
// artist's official homepages. [SelectOfficialSite] picks the official site
// among them.
type OfficialSiteCatalog interface {
	// ListOfficialSiteRelations returns the artist's name and official
	// homepage links in the catalog, for the artist identified by the given
	// MBID. Relations is empty when the catalog records no official homepage.
	//
	// # Possible errors:
	//
	//   - NotFound: the catalog has no artist with the MBID.
	//   - Unavailable: the external catalog service is down or rate-limited.
	//   - Internal: unexpected failure while reading the catalog.
	ListOfficialSiteRelations(ctx context.Context, mbid string) (*OfficialSiteRelations, error)
}
