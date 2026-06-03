package entity

import (
	"context"
	"time"
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
	// MerchURL is the optional official merchandise information page (official
	// site page or official social media post) shared across the series. Empty
	// until the merch-url discovery job resolves it; stores only the link, no
	// sale timing, channel, price, or item data.
	MerchURL string
}

// NewSeries creates a new Series with an auto-generated UUIDv7 ID.
//
// MerchURL is intentionally not a parameter: it is never known at series
// creation time and is populated asynchronously by the merch-url discovery
// job via [SeriesRepository.SetMerchURL].
func NewSeries(title string, seriesType SeriesType, sourceURL string) *Series {
	return &Series{
		ID:        newID(),
		Title:     title,
		Type:      seriesType,
		SourceURL: sourceURL,
	}
}

// MerchCandidate is a series whose earliest event falls within the merch-url
// discovery window, paired with a representative performing artist name used to
// ground the Gemini search prompt. MerchURL carries the series' current value
// so the discovery job can decide whether to search (empty) or revalidate the
// existing link (non-empty).
type MerchCandidate struct {
	// SeriesID is the series whose merch URL is being resolved.
	SeriesID string
	// SeriesTitle is the series title (tour/festival/single name) used in the
	// search prompt.
	SeriesTitle string
	// ArtistName is a representative performer of the series' earliest event,
	// used to disambiguate the search prompt.
	ArtistName string
	// MerchURL is the series' current merch_url value; empty means the series
	// needs a search, non-empty means it is a revalidation candidate.
	MerchURL string
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
	// ListSeriesInMerchWindow returns every series whose earliest event's local
	// date falls within [today, today+window], paired with a representative
	// performing artist name. The result is the superset the merch-url discovery
	// job partitions on MerchURL: empty entries are searched, non-empty entries
	// are revalidated. Series whose earliest event is already in the past or
	// more than `window` away are excluded.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If window is not positive.
	ListSeriesInMerchWindow(ctx context.Context, window time.Duration) ([]*MerchCandidate, error)
	// SetMerchURL persists a resolved merch URL for a series, but only when the
	// series' current merch_url is NULL (fill-once). A series whose merch_url is
	// already populated is left unchanged, so a live URL is never overwritten.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If seriesID or merchURL is empty.
	SetMerchURL(ctx context.Context, seriesID, merchURL string) error
	// ClearMerchURL sets a series' merch_url back to NULL. Used by the discovery
	// job to drop a dead link before re-searching.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If seriesID is empty.
	ClearMerchURL(ctx context.Context, seriesID string) error
}

// MerchSearcher resolves the official merchandise URL for a series using an
// external grounded search (Gemini Flash-Lite).
type MerchSearcher interface {
	// SearchMerchURL returns the single URL carrying the richest official merch
	// sales information for the given artist + series, restricted to the
	// artist's official website or official social media accounts. The result
	// MAY be a social-media post URL. When no confident official source exists,
	// it returns an empty string and a nil error — an empty result is a normal
	// outcome, not a failure.
	//
	// # Possible errors
	//
	//  - Unavailable: If the external search service is down.
	//  - Internal: unexpected failure during search processing.
	SearchMerchURL(ctx context.Context, artistName, seriesTitle string) (string, error)
}

// MerchLivenessChecker reports whether an existing merch URL is still reachable.
type MerchLivenessChecker interface {
	// IsDeadLink reports whether the URL is definitively dead. It returns true
	// ONLY for a definitive non-2xx/non-3xx HTTP status or a hard request
	// failure. Transient or ambiguous results (timeouts, network blips, a
	// bot-blocking response that is not a definitive failure) return false so a
	// live link is never cleared on flaky evidence.
	IsDeadLink(ctx context.Context, url string) bool
}
