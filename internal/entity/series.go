package entity

import "context"

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
}

// NewSeries creates a new Series with an auto-generated UUIDv7 ID.
func NewSeries(title string, seriesType SeriesType, sourceURL string) *Series {
	return &Series{
		ID:        newID(),
		Title:     title,
		Type:      seriesType,
		SourceURL: sourceURL,
	}
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
}
