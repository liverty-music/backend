package entity

import (
	"context"
	"os"
	"strings"
	"time"
)

// mediaCDNBaseEnv is the environment variable name that holds the CDN base URL
// used to compose served cover-image URLs. The constant lives here so both the
// usecase and the RPC mapper read the same variable through CoverImageURL.
const mediaCDNBaseEnv = "ORGANIZER_MEDIA_CDN_BASE"

// SeriesType classifies the shape of an event series.
//
// Values mirror the proto enum [SeriesTypeProto] and the PostgreSQL
// [series_type] enum. Adding a new value is backwards-compatible at the
// proto level; consumers MUST handle unknown values defensively.
//
// [SeriesTypeProto]: https://github.com/liverty-music/specification/blob/main/proto/liverty_music/entity/v1/series.proto
type SeriesType string

const (
	// SeriesTypeTour groups events at multiple venues by the same set of
	// performers under a single branded engagement (e.g. "Eras Tour 2026").
	SeriesTypeTour SeriesType = "TOUR"
	// SeriesTypeSingle groups events at a single venue spanning one or more
	// consecutive days (e.g. a single concert or a "3 Days" residency).
	SeriesTypeSingle SeriesType = "SINGLE"
	// SeriesTypeFestival groups events featuring multiple performers
	// (e.g. "FUJI ROCK FESTIVAL 2026").
	SeriesTypeFestival SeriesType = "FESTIVAL"
)

// SeriesVisibility controls who can reach a first-party (organizer-authored)
// series. It is meaningful only for series that carry an OrganizerID;
// discovered series leave it zero/empty.
type SeriesVisibility string

const (
	// SeriesVisibilityPublic means the series appears in normal discovery
	// surfaces and follower feeds once published.
	SeriesVisibilityPublic SeriesVisibility = "PUBLIC"
	// SeriesVisibilityUnlisted means the series is accessible only via a
	// signed tokenized share URL; it never appears in discovery or follower
	// surfaces.
	SeriesVisibilityUnlisted SeriesVisibility = "UNLISTED"
)

// SeriesPublishState is the authoring lifecycle of a first-party series.
// It is meaningful only for series that carry an OrganizerID; discovered
// series leave it zero/empty.
type SeriesPublishState string

const (
	// SeriesPublishStateDraft means the series is being authored and is not
	// yet visible to fans. Its events live in draft_events, not events.
	SeriesPublishStateDraft SeriesPublishState = "DRAFT"
	// SeriesPublishStatePublished means the draft events have been materialized
	// into the live events table and the series is visible according to its
	// Visibility setting.
	SeriesPublishStatePublished SeriesPublishState = "PUBLISHED"
	// SeriesPublishStateCancelled means the series was cancelled. Its events
	// rows remain in the events table (claimed slots), but they are excluded
	// from all fan-facing surfaces by the shared visibility guard.
	SeriesPublishStateCancelled SeriesPublishState = "CANCELLED"
)

// MediaKind identifies the type of an organizer media asset.
type MediaKind string

const (
	// MediaKindImage is an uploaded image asset (cover photo, etc.).
	MediaKindImage MediaKind = "IMAGE"
)

// OriginalObjectKey constructs the GCS object key in the originals (internal)
// bucket for the given organizer and media id. The key is
// `{organizer_id}/{media_id}` — no cdn/ prefix because the originals bucket is
// not CDN-served.
func OriginalObjectKey(organizerID, mediaID string) string {
	return organizerID + "/" + mediaID
}

// VariantObjectKey constructs the GCS object key in the served (public) bucket
// for a specific variant of the given organizer media. The key is
// `cdn/{organizer_id}/{media_id}/{variant}.webp`.
func VariantObjectKey(organizerID, mediaID, variant string) string {
	return "cdn/" + organizerID + "/" + mediaID + "/" + variant + ".webp"
}

// VariantObjectPrefix returns the key prefix covering all variants of a single
// media object in the served bucket: `cdn/{organizer_id}/{media_id}/`.
// Pass this to ImageStorer.DeletePrefix to remove all variants in one sweep.
func VariantObjectPrefix(organizerID, mediaID string) string {
	return "cdn/" + organizerID + "/" + mediaID + "/"
}

// ObjectKey is kept for backwards compatibility with callers that have not yet
// migrated to VariantObjectKey. It constructs the legacy cdn/ key used by the
// pre-pipeline upload path.
//
// Deprecated: use VariantObjectKey or VariantObjectPrefix for new code.
func ObjectKey(organizerID, mediaID string) string {
	return "cdn/" + organizerID + "/" + mediaID
}

// VariantURL composes the public CDN URL for one variant of a cover image. It
// reads the CDN base from the ORGANIZER_MEDIA_CDN_BASE environment variable and
// appends the variant object key produced by VariantObjectKey. Returns "" when
// the env var is unset or empty so callers never emit a malformed relative URL.
//
// variant must be one of "thumb" or "large".
func VariantURL(organizerID, mediaID, variant string) string {
	base := strings.TrimRight(os.Getenv(mediaCDNBaseEnv), "/")
	if base == "" {
		return ""
	}
	return base + "/" + VariantObjectKey(organizerID, mediaID, variant)
}

// CoverImageURL composes the public CDN URL for a cover image asset. It reads
// the CDN base from the ORGANIZER_MEDIA_CDN_BASE environment variable and
// appends the object key produced by ObjectKey. Returns "" when the env var is
// unset or empty so callers never emit a malformed relative "/cdn/..." URL.
// This is the single source of truth for cover-URL composition; both the
// authoring use-case (upload response) and the RPC mapper (read response) call
// this function rather than duplicating the derivation.
//
// Deprecated: use VariantURL for new code that builds variant-aware URLs.
func CoverImageURL(organizerID, mediaID string) string {
	base := strings.TrimRight(os.Getenv(mediaCDNBaseEnv), "/")
	if base == "" {
		return ""
	}
	return base + "/" + ObjectKey(organizerID, mediaID)
}

// Media represents a single media object uploaded by an organizer. The ID is
// a UUIDv7 that serves as both the creation timestamp source and the
// cache-busting object-key token. No URL is stored; the served URL is derived
// at read time from the exposure and the media id.
type Media struct {
	// ID is the unique media identifier (UUIDv7, application-generated). It is
	// the object-key basename and the cache-busting version token.
	ID string
	// OrganizerID is the owning organizer. Used as the stable tenant segment
	// of the object key.
	OrganizerID string
	// Kind is the media asset kind (IMAGE at MVP).
	Kind MediaKind
	// Attributes holds kind-specific metadata (e.g. content_type for IMAGE).
	Attributes map[string]string
}

// ContentType returns the content_type attribute for IMAGE media, or empty
// string when the attribute is absent.
func (m *Media) ContentType() string {
	if m.Attributes == nil {
		return ""
	}
	return m.Attributes["content_type"]
}

// Series is the parent aggregation above [Event]. It owns metadata that is
// shared across every event in the same engagement, ensuring fields like
// title and source URL are stored exactly once per series rather than
// duplicated on every member event.
//
// See [SeriesProto] for the wire representation.
//
// [SeriesProto]: https://github.com/liverty-music/specification/blob/main/proto/liverty_music/entity/v1/series.proto
type Series struct {
	// ID is the unique series identifier (UUIDv7).
	ID string
	// Title is the title shared across all member events (e.g. tour or
	// festival name).
	Title string
	// Type is the classification of this series.
	Type SeriesType
	// SourceURL is the optional series-level official URL (tour page,
	// festival page). Empty when no canonical landing page is known.
	SourceURL string

	// The following fields are set only for first-party (organizer-authored)
	// series. Discovered series (OrganizerID == nil) leave them all nil.

	// Description is the optional free-form body text authored by the organizer.
	Description *string
	// CoverMedia is the current cover image media object for the series. Nil
	// when no image has been uploaded. The served URL is derived at read time
	// from the exposure and the media id — it is NOT stored on the series row.
	CoverMedia *Media
	// OrganizerID is the owning organizer for a first-party series. Nil marks
	// a discovery-pipeline series.
	OrganizerID *string
	// Visibility controls who can reach the series (PUBLIC or UNLISTED). Nil
	// for discovered series.
	Visibility *SeriesVisibility
	// PublishState is the authoring lifecycle (DRAFT/PUBLISHED/CANCELLED). Nil
	// for discovered series.
	PublishState *SeriesPublishState
	// UnlistedToken is the backend-only HMAC-derived share token for an
	// UNLISTED series. Never exposed on read DTOs. Nil unless UNLISTED.
	UnlistedToken *string
	// PublishedAt is when the series was first transitioned to PUBLISHED. Nil
	// while DRAFT.
	PublishedAt *time.Time
	// CancelledAt is when the series was cancelled. Nil unless CANCELLED.
	CancelledAt *time.Time
}

// IsFirstParty reports whether the series was authored by an organizer (as
// opposed to sourced from the discovery pipeline).
func (s *Series) IsFirstParty() bool {
	return s.OrganizerID != nil
}

// IsPubliclyVisible reports whether the series should appear on fan-facing
// public and follower surfaces. A discovered series (OrganizerID nil) is
// always publicly visible. A first-party series is only visible when it is
// PUBLISHED and PUBLIC.
func (s *Series) IsPubliclyVisible() bool {
	if !s.IsFirstParty() {
		return true
	}
	return s.PublishState != nil &&
		*s.PublishState == SeriesPublishStatePublished &&
		s.Visibility != nil &&
		*s.Visibility == SeriesVisibilityPublic
}

// NewSeries creates a new Series with an auto-generated UUIDv7 ID.
func NewSeries(title string, seriesType SeriesType, sourceURL string) *Series {
	return &Series{
		ID:        NewID(),
		Title:     title,
		Type:      seriesType,
		SourceURL: sourceURL,
	}
}

// MediaRepository defines the data access interface for [Media] objects and the
// series_media cut-over that the media-processor consumer performs after writing
// variants to GCS.
type MediaRepository interface {
	// InsertMedia persists a media row idempotently (ON CONFLICT(id) DO NOTHING).
	// Used by AttachMedia to record the upload before publishing MEDIA.uploaded.
	//
	// # Possible errors
	//
	//  - Internal: if the insert fails.
	InsertMedia(ctx context.Context, media *Media) error

	// FindMediaByID retrieves a single media row by id.
	//
	// # Possible errors
	//
	//  - NotFound: if no media row exists with the given id.
	FindMediaByID(ctx context.Context, mediaID string) (*Media, error)

	// CutOverSeriesMedia atomically re-points a series' cover to newMediaID and
	// returns the prior media id (empty string when the series had no cover).
	// The cut-over is performed in a single transaction:
	//  1. upsert series_media(series_id) → newMediaID;
	//  2. capture the old media_id (if any);
	//  3. delete the old media row.
	// After a successful return the old GCS objects can be reclaimed via
	// ImageStorer.DeletePrefix. On redelivery the operation is idempotent:
	// if series_media already points to newMediaID the function returns ("", nil).
	//
	// # Possible errors
	//
	//  - NotFound: if the series does not exist.
	//  - Internal: if the transaction fails.
	CutOverSeriesMedia(ctx context.Context, seriesID, newMediaID string) (oldMediaID string, err error)

	// DeleteMedia removes a media row by id. Used after GCS originals are
	// deleted to keep the DB consistent. Missing rows are silently skipped
	// (idempotent).
	//
	// # Possible errors
	//
	//  - Internal: if the delete fails for a reason other than "not found".
	DeleteMedia(ctx context.Context, mediaID string) error
}

// SeriesRepository defines the data access interface for [Series].
type SeriesRepository interface {
	// Create persists one or more Series rows. Nil elements are silently skipped.
	// Returns the IDs of the rows actually inserted.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If any series has an empty title or unknown type.
	Create(ctx context.Context, series ...*Series) ([]string, error)
	// Get retrieves a Series by ID.
	//
	// # Possible errors
	//
	//  - NotFound: If no series exists with the given ID.
	//  - InvalidArgument: If the ID is empty.
	Get(ctx context.Context, id string) (*Series, error)
	// ListByIDs retrieves multiple Series by ID. IDs not found are silently
	// omitted from the result.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the ids slice is empty.
	ListByIDs(ctx context.Context, ids []string) ([]*Series, error)
	// DeleteOrphaned removes, from among the given candidate series IDs, every row
	// that has no member events AND no pending staged concerts referencing it.
	// Eager series-row creation at discovery time allows a series to transiently
	// exist with zero events (a provisional group mint that adopted a
	// co-headliner's series, or a group later fully rejected); this sweep reclaims
	// those. It is deliberately scoped to caller-supplied ids — the series a
	// single discovery run minted or a rejection touched — so it can never delete
	// another concurrently-processing run's just-created (still event-less) series
	// and cause an FK violation on that run's pending event insert. An empty
	// candidate slice is a no-op. Returns the number of rows deleted. Idempotent.
	DeleteOrphaned(ctx context.Context, seriesIDs []string) (int64, error)

	// CreateDraft persists a new first-party series in the DRAFT state along with
	// its draft events and draft performers in a single transaction.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the series is missing required fields.
	//  - FailedPrecondition: If a foreign key constraint is violated.
	CreateDraft(ctx context.Context, series *Series, draftEvents []*DraftEvent, performerArtistIDs []string) error

	// UpdateDraft replaces the draft content of a DRAFT series in a single
	// transaction: updates series authoring columns and replaces all draft_events
	// and draft_series_performers by delete + reinsert. Only allowed while the
	// series is in the DRAFT state.
	//
	// # Possible errors
	//
	//  - NotFound: If no series with the given ID exists.
	//  - FailedPrecondition: If the series is not in the DRAFT state.
	UpdateDraft(ctx context.Context, series *Series, draftEvents []*DraftEvent, performerArtistIDs []string) error

	// LoadDraft retrieves the draft events and performer artist IDs for the given
	// series. Used by the Publish path to materialize draft content into events.
	//
	// # Possible errors
	//
	//  - NotFound: If no series with the given ID exists.
	LoadDraft(ctx context.Context, seriesID string) ([]*DraftEvent, []string, error)

	// GetAuthored retrieves the full authored concert for one series: the series
	// itself, its events (live events when PUBLISHED/CANCELLED, draft events when
	// DRAFT), and the performing artists.
	//
	// # Possible errors
	//
	//  - NotFound: If no series with the given ID exists.
	GetAuthored(ctx context.Context, seriesID string) (*Series, []*Event, []*Artist, error)

	// ListByOrganizer retrieves all first-party series owned by the given organizer,
	// ordered by creation time descending (newest first).
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the organizerID is empty.
	ListByOrganizer(ctx context.Context, organizerID string) ([]*Series, error)

	// GetByUnlistedToken retrieves a first-party series by its share token, using
	// the unique index for O(1) lookup. The comparison is done entirely in the DB
	// (constant-time via the index). Returns NotFound when no series carries the
	// given token.
	//
	// # Possible errors
	//
	//  - NotFound: If no series with the given token exists.
	GetByUnlistedToken(ctx context.Context, token string) (*Series, error)

	// SetUnlistedToken sets or rotates the unlisted_token for the given series.
	//
	// # Possible errors
	//
	//  - NotFound: If no series with the given ID exists.
	//  - AlreadyExists: If the generated token collides with another series.
	SetUnlistedToken(ctx context.Context, seriesID, token string) error

	// MarkPublished transitions the series to PUBLISHED and records published_at.
	// The series MUST already be a first-party series (OrganizerID not null).
	//
	// # Possible errors
	//
	//  - NotFound: If no series with the given ID exists.
	MarkPublished(ctx context.Context, seriesID string, publishedAt time.Time) error

	// MarkCancelled transitions the series to CANCELLED and records cancelled_at.
	//
	// # Possible errors
	//
	//  - NotFound: If no series with the given ID exists.
	MarkCancelled(ctx context.Context, seriesID string, cancelledAt time.Time) error

	// ReplaceCoverMedia atomically replaces a series' cover in a single
	// transaction: it inserts a new media row (with the given organizer, kind, and
	// attributes), inserts the series_media join row, and deletes any prior
	// series_media + media rows. It returns the prior media id ("" when the series
	// had no cover) so the caller can reclaim the replaced object from storage.
	//
	// # Possible errors
	//
	//  - NotFound: If no series with the given ID exists.
	ReplaceCoverMedia(ctx context.Context, seriesID string, newMedia *Media) (oldMediaID string, err error)

	// PublishDraft materializes the draft content of a first-party DRAFT series
	// into the live events table and returns the IDs of newly inserted events (the
	// notification payload). The caller is responsible for emitting CONCERT.created
	// after a successful return.
	//
	// Steps performed in one transaction:
	//  1. Load draft_events and draft_series_performers for the series.
	//  2. For each draft event, run the same suppression / duplicate / claim /
	//     cross-organizer decision tree used by the discovery pipeline.
	//  3. Delete draft_events and draft_series_performers for the series.
	//  4. Mark the series PUBLISHED (published_at = now).
	//  5. Drop any staged_concerts that reference the same series_id (they are
	//     superseded by the organizer's authoritative version).
	//
	// # Possible errors
	//
	//  - NotFound: If the series or any referenced artist/venue does not exist.
	//  - FailedPrecondition: If the series is not in the DRAFT state, if a slot
	//    is suppressed, or if a cross-organizer ownership conflict is detected.
	PublishDraft(ctx context.Context, seriesID string, now time.Time) (newEventIDs []string, err error)
}
