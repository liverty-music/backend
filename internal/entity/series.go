package entity

import (
	"context"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

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

// PublishState is the publish lifecycle of an [Event] of a first-party series.
// An Event of a discovered series has none (nil). A first-party [Series]
// carries a state derived from its Events (see [DerivePublishState]).
type PublishState string

const (
	// PublishStateDraft means the event is being authored. It holds no slot of
	// the catalog, is not a [Concert], and is visible only in the organizer
	// console. A new event of a first-party series starts here.
	PublishStateDraft PublishState = "DRAFT"
	// PublishStatePublished means the event is live; it is publicly visible
	// when its series is PUBLIC.
	PublishStatePublished PublishState = "PUBLISHED"
	// PublishStateCancelled means the published event was cancelled. It is
	// terminal: the event keeps its slot and its event page but is dropped
	// from fan lists.
	PublishStateCancelled PublishState = "CANCELLED"
)

// SeriesImage represents a single image object uploaded by an organizer. The ID is
// a UUIDv7 that serves as both the creation timestamp source and the
// cache-busting object-key token. No URL is stored; the served URL is derived
// at read time from the exposure and the series image id.
type SeriesImage struct {
	// ID is the unique series image identifier (UUIDv7, application-generated). It is
	// the object-key basename and the cache-busting version token.
	ID string
	// OrganizerID is the owning organizer. Used as the stable per-Organizer segment
	// of the object key.
	OrganizerID string
	// Attributes holds kind-specific metadata (e.g. content_type for IMAGE).
	Attributes map[string]string
}

// ContentType returns the content_type attribute of the image, or empty
// string when the attribute is absent.
func (m *SeriesImage) ContentType() string {
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
	// CoverImage is the current cover image object for the series (the image
	// shown as the series' cover). Nil when no image has been uploaded. The
	// served URL is derived at read time from the exposure and the series image id —
	// it is NOT stored on the series row.
	CoverImage *SeriesImage
	// OrganizerID is the owning organizer for a first-party series. Nil marks
	// a discovery-pipeline series.
	OrganizerID *string
	// Organizer is the owning organizer, resolved on concert reads so a fan
	// sees who sells the tickets. Set exactly when OrganizerID is set on those
	// reads; nil elsewhere.
	Organizer *Organizer
	// Visibility controls who can reach the series (PUBLIC or UNLISTED). Nil
	// for discovered series.
	Visibility *SeriesVisibility
	// PublishState is derived from the series' Events (see
	// [DerivePublishState]) and filled by the repository on read; the series
	// stores no state of its own. Nil for discovered series.
	PublishState *PublishState
	// UnlistedToken is the backend-only HMAC-derived share token for an
	// UNLISTED series. Never exposed on read DTOs. Nil unless UNLISTED.
	UnlistedToken *string
}

// IsFirstParty reports whether the series was authored by an organizer (as
// opposed to sourced from the discovery pipeline).
func (s *Series) IsFirstParty() bool {
	return s.OrganizerID != nil
}

// IsPublic reports whether the series' visibility is PUBLIC.
func (s *Series) IsPublic() bool {
	return s.Visibility != nil && *s.Visibility == SeriesVisibilityPublic
}

// DerivePublishState returns the publish state of a first-party series from
// the publish states of its Events: PUBLISHED when at least one is PUBLISHED;
// CANCELLED when none is PUBLISHED and at least one is CANCELLED; DRAFT
// otherwise, when every Event is DRAFT. It returns nil when no Event has a
// publish state, as for a discovered series, unless firstParty is set: a
// first-party series always has a state.
func DerivePublishState(firstParty bool, events []*Event) *PublishState {
	if !firstParty {
		return nil
	}
	state := PublishStateDraft
	for _, e := range events {
		switch {
		case e.HasPublishState(PublishStatePublished):
			published := PublishStatePublished
			return &published
		case e.HasPublishState(PublishStateCancelled):
			state = PublishStateCancelled
		}
	}
	return &state
}

// HasPublishedOrCancelledEvent reports whether any of the series' Events was
// ever published, that is, is PUBLISHED or CANCELLED.
func HasPublishedOrCancelledEvent(events []*Event) bool {
	for _, e := range events {
		if e.HasPublishState(PublishStatePublished) || e.HasPublishState(PublishStateCancelled) {
			return true
		}
	}
	return false
}

// CheckChange returns FailedPrecondition when next would change what publish
// fixes on the series. While every Event is DRAFT, every authored attribute
// may change. Once the series has a PUBLISHED or CANCELLED Event, its type,
// source page and visibility are fixed; title, description, cover image and
// performers stay changeable.
func (s *Series) CheckChange(next *Series, events []*Event) error {
	if !HasPublishedOrCancelledEvent(events) {
		return nil
	}
	sameVisibility := (s.Visibility == nil) == (next.Visibility == nil) &&
		(s.Visibility == nil || *s.Visibility == *next.Visibility)
	if next.Type != s.Type || next.SourceURL != s.SourceURL || !sameVisibility {
		return apperr.New(codes.FailedPrecondition, "the type, source page and visibility of a published series cannot change")
	}
	return nil
}

// HasAnyEventPage reports whether the series has an event page: at least one
// of its Events has one (see [Event.HasEventPage]).
func (s *Series) HasAnyEventPage(events []*Event) bool {
	for _, e := range events {
		if e.HasEventPage(s) {
			return true
		}
	}
	return false
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

// SeriesImageRepository defines the data access interface for [SeriesImage] objects and the
// series_cover_images cut-over that the series-image-consumer performs after writing
// variants to GCS.
type SeriesImageRepository interface {
	// Create persists a series image row idempotently (ON CONFLICT(id) DO NOTHING).
	// Used by SetCoverImage to record the upload before publishing SERIES_IMAGE.uploaded.
	//
	// # Possible errors
	//
	//  - Internal: if the insert fails.
	Create(ctx context.Context, seriesImage *SeriesImage) error

	// Get retrieves a single series image row by id.
	//
	// # Possible errors
	//
	//  - NotFound: if no series image row exists with the given id.
	Get(ctx context.Context, seriesImageID string) (*SeriesImage, error)

	// ReplaceCover atomically re-points a series' cover to newSeriesImageID and
	// returns the prior series image id (empty string when the series had no cover).
	// The cut-over is performed in a single transaction:
	//  1. upsert series_cover_images(series_id) → newSeriesImageID;
	//  2. capture the old series_image_id (if any);
	//  3. delete the old series image row.
	// After a successful return the old GCS objects can be reclaimed via
	// ImageStorer.DeletePrefix. On redelivery the operation is idempotent:
	// if series_cover_images already points to newSeriesImageID the function returns ("", nil).
	//
	// # Possible errors
	//
	//  - NotFound: if the series does not exist.
	//  - Internal: if the transaction fails.
	ReplaceCover(ctx context.Context, seriesID, newSeriesImageID string) (oldSeriesImageID string, err error)

	// Delete removes a series image row by id. Used after GCS originals are
	// deleted to keep the DB consistent. Missing rows are silently skipped
	// (idempotent).
	//
	// # Possible errors
	//
	//  - Internal: if the delete fails for a reason other than "not found".
	Delete(ctx context.Context, seriesImageID string) error

	// ListByOrganizer returns every series image row owned by the organizer, whether or
	// not a series uses it as its cover. It returns an empty slice when the
	// organizer owns none.
	//
	// # Possible errors
	//
	//  - Internal: if the query fails.
	ListByOrganizer(ctx context.Context, organizerID string) ([]*SeriesImage, error)
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
	// that has no member events AND no pending concert candidates referencing it.
	// Eager series-row creation at discovery time allows a series to transiently
	// exist with zero events (a provisional group mint that adopted a
	// co-headliner's series, or a group later fully rejected); this sweep reclaims
	// those. It is deliberately scoped to caller-supplied ids — the series a
	// single discovery run minted or a rejection touched — so it can never delete
	// another concurrently-processing run's just-created (still event-less) series
	// and cause an FK violation on that run's pending event insert. An empty
	// candidate slice is a no-op. Returns the number of rows deleted. Idempotent.
	DeleteOrphaned(ctx context.Context, seriesIDs []string) (int64, error)

	// CreateDraft persists a new first-party series and its events in a single
	// transaction: each event is stored DRAFT, as a concert performed by the
	// given artists. The events' ids are kept.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the series is missing required fields.
	//  - FailedPrecondition: If a referenced organizer, venue or artist does not exist.
	CreateDraft(ctx context.Context, series *Series, events []*Event, performerArtistIDs []string) error

	// Update applies an organizer's edit to a first-party series in a single
	// transaction. It sets the series' title, type, source page, description
	// and visibility. For each given event: an event with an empty ID is added
	// as a new DRAFT event; a DRAFT event takes the given venue, listed venue
	// name, date and times; a PUBLISHED event takes the given start and open
	// times and keeps its venue and date; a CANCELLED event is left unchanged.
	// The given performers become the performers of every given DRAFT and
	// PUBLISHED event. Every DRAFT event of the series that is not given is
	// removed.
	//
	// When a PUBLISHED event's start time changes, the event takes its new
	// slot and keeps its id: a discovered event at that slot is merged into it
	// (its ticket journeys and performers move over and it is removed).
	//
	// # Possible errors
	//
	//  - FailedPrecondition: If the series does not exist or is not
	//    first-party, if a given id is not an event of the series, or if a
	//    PUBLISHED event's new slot is suppressed or held by an event of
	//    another first-party series. Nothing changes.
	Update(ctx context.Context, series *Series, events []*Event, performerArtistIDs []string) error

	// GetAuthored retrieves the full authored concert for one series: the
	// series with its cover image and derived publish state, and one Concert
	// per event whatever its publish state (ordered by date, then start time
	// with unknown start times last), each with its Venue, its publish state
	// and its own performers. [Performers] gives the artists performing at any
	// of them.
	//
	// # Possible errors
	//
	//  - NotFound: If no series with the given ID exists.
	GetAuthored(ctx context.Context, seriesID string) (*Series, []*Concert, error)

	// PublishEvents publishes the given DRAFT events of a first-party series
	// in a single transaction and returns the ids of the events it made
	// PUBLISHED in place. At each event's slot (venue, date and start time):
	// with no event there that is not DRAFT, the event becomes PUBLISHED; with
	// a PUBLISHED event of this series there, the draft is removed; with an
	// event of a discovered series or of another series of the same
	// organizer, that event is claimed (moved to this series with its id,
	// made PUBLISHED, given the draft's performers) and the draft is removed.
	// Claimed events are not returned. The series' concert candidates are
	// dropped.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If no event is given.
	//  - NotFound: If the series does not exist.
	//  - FailedPrecondition: If a given event is not a DRAFT event of the
	//    series, or a slot is suppressed, held by another organizer's event, or
	//    held by a CANCELLED event. Nothing changes.
	PublishEvents(ctx context.Context, seriesID string, eventIDs []string) (publishedEventIDs []string, err error)

	// CancelEvents cancels the given events of a first-party series in a
	// single transaction: a PUBLISHED event becomes CANCELLED and is kept, a
	// DRAFT event is removed, and a series left with no event is removed. It
	// returns the ids of the events it made CANCELLED.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If no event is given.
	//  - NotFound: If the series does not exist.
	//  - FailedPrecondition: If a given event is not an event of the series
	//    or is already CANCELLED. Nothing changes.
	CancelEvents(ctx context.Context, seriesID string, eventIDs []string) (cancelledEventIDs []string, err error)

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

	// ReplaceCover atomically replaces a series' cover in a single
	// transaction: it inserts a new series image row (with the given organizer, kind, and
	// attributes), inserts the series_cover_images join row, and deletes any prior
	// series_cover_images + series image row. It returns the prior series image id ("" when the series
	// had no cover) so the caller can reclaim the replaced object from storage.
	//
	// # Possible errors
	//
	//  - NotFound: If no series with the given ID exists.
	ReplaceCoverImage(ctx context.Context, seriesID string, newSeriesImage *SeriesImage) (oldSeriesImageID string, err error)
}
