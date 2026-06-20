package entity

import (
	"context"
	"time"

	"github.com/liverty-music/backend/pkg/geo"
)

// Concert is the user-facing DTO for a music live event.
//
// It composes the underlying [Event] with its parent [Series] and the full list
// of performing artists so that a single read carries everything the UI needs
// without follow-up fetches. Series-level metadata (title, source URL, type)
// lives on the embedded Series. The previously-singular ArtistID has been
// replaced by Performers to support festival lineups and co-headliners.
//
// Corresponds to liverty_music.entity.v1.Concert.
type Concert struct {
	Event
	// Series is the parent series that aggregates this concert with any sibling
	// events sharing the same tour, festival, or multi-day run. Populated by
	// the repository layer on read; required when building a Concert for write.
	Series *Series
	// Performers are the artists performing at this concert, in display order.
	// Always contains at least one performer; multi-performer values cover
	// festivals, co-headliners, and support acts. Populated by the repository
	// layer on read.
	Performers []*Artist
}

// PerformerIDs returns the IDs of all performers attached to this concert.
// Convenient for callers that only need identifiers (e.g. mock comparisons,
// repository writes, or hype checks against followed_artists).
//
// Nil entries in Performers (which the type system permits even though the
// supported insert path rejects them) are skipped silently rather than
// triggering a nil-pointer panic, so read-side callers can safely call this
// on any Concert hydrated from external code paths or test fixtures.
func (c *Concert) PerformerIDs() []string {
	if len(c.Performers) == 0 {
		return nil
	}
	ids := make([]string, 0, len(c.Performers))
	for _, p := range c.Performers {
		if p == nil {
			continue
		}
		ids = append(ids, p.ID)
	}
	return ids
}

// ScrapedConcert represents raw concert information rediscovered from external sources.
// It lacks system-specific identifiers like ID, SeriesID, or PerformerIDs.
// JSON tags are present to support serialization as an event payload (concert.discovered).
type ScrapedConcert struct {
	// Title is the descriptive title of the scraped event. During the transition
	// to first-class series, this is used as both the series title and (when the
	// event has a unique subtitle) the event-specific suffix.
	Title string `json:"title"`
	// ListedVenueName is the raw venue name as listed in the source data.
	ListedVenueName string `json:"listed_venue_name"`
	// AdminArea is the administrative area (prefecture, state, province) where the venue is located.
	// It is nil when the area could not be determined with confidence.
	AdminArea *string `json:"admin_area,omitempty"`
	// LocalDate represents the calendar date of the event.
	// See entity.Concert.LocalDate for detailed specifications.
	LocalDate time.Time `json:"local_date"`
	// StartTime is the specific starting time (optional).
	// Zero value means unknown; omitted from JSON via omitzero.
	StartTime time.Time `json:"start_time,omitzero"`
	// OpenTime is the time when doors open (optional).
	// Zero value means unknown; omitted from JSON via omitzero.
	OpenTime time.Time `json:"open_time,omitzero"`
	// SourceURL is the URL where this information was found. Used to populate
	// the parent Series.SourceURL.
	SourceURL string `json:"source_url"`
	// IsTour reports whether this concert originated from a Gemini <tour> block
	// (as opposed to <standalone>). It selects SeriesType downstream: TOUR vs
	// SINGLE. Serialized so it survives the concert.discovered Pub/Sub payload.
	IsTour bool `json:"is_tour,omitempty"`
	// TourGroup ties together all concerts from the same tour block within one
	// discovery run, so the creation path can persist them under one Series. It
	// is an intra-run handle (unique within a single search only), NOT a
	// cross-run series key — series identity is adopted from already-persisted
	// member events. Zero for standalone concerts.
	TourGroup int `json:"tour_group,omitempty"`
}

// ToConcert converts a ScrapedConcert into a fully-populated Concert entity.
//
// The caller supplies the three IDs (artist, event, venue) and the seriesID
// of the parent Series that has already been built (or will be built in the
// same transaction). The returned Concert embeds the Series shell — with the
// title and source URL copied from the scrape — so the caller can pass the
// same Series instance into SeriesRepository.Create alongside ConcertRepository.Create.
//
// Two usage patterns exist:
//
//   - Search path (concert_uc): pass empty strings for eventID, venueID, and
//     seriesID. The returned Concert is for immediate return to callers and is
//     never persisted; the embedded Series is non-canonical.
//   - Creation path (concert_creation_uc): pass UUIDs for all four IDs. The
//     returned Concert is bulk-inserted into the database, and the embedded
//     Series row is inserted in the same use-case run via SeriesRepository.
func (sc *ScrapedConcert) ToConcert(artistID, seriesID, eventID, venueID string, seriesType SeriesType) *Concert {
	listedName := sc.ListedVenueName
	series := &Series{
		ID:        seriesID,
		Title:     sc.Title,
		Type:      seriesType,
		SourceURL: sc.SourceURL,
	}
	c := &Concert{
		Event: Event{
			ID:              eventID,
			SeriesID:        seriesID,
			VenueID:         venueID,
			ListedVenueName: &listedName,
			LocalDate:       sc.LocalDate,
		},
		Series:     series,
		Performers: []*Artist{{ID: artistID}},
	}
	c.StartTime = NullableTime(sc.StartTime)
	c.OpenTime = NullableTime(sc.OpenTime)
	return c
}

// NullableTime returns a pointer to t, or nil when t is the zero value (an
// unknown time). It is the canonical "unknown time → SQL NULL" conversion used
// across the event physical key, mirroring how nullable TIMESTAMPTZ columns
// model "unknown".
func NullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// StartKey returns the canonical dedup key for an optional event start time
// under NULLS NOT DISTINCT semantics: an unknown (nil/zero) start is the empty
// string; a known start is its UTC RFC3339 form. Two events share a physical
// start time iff their StartKey values are equal — the single source of truth
// the application-layer dedup/fill paths use to match the DB's
// `(venue_id, local_event_date, start_at) NULLS NOT DISTINCT` constraint.
func StartKey(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// ScrapedConcerts is a slice of ScrapedConcert pointers with domain-level operations.
type ScrapedConcerts []*ScrapedConcert

// FilterNew returns scraped concerts that do not conflict with existing concerts,
// a best-effort upstream filter aligned with the events physical natural key
// `(venue_id, local_event_date, start_at)`. It handles both cross-batch
// deduplication (against existing DB concerts) and within-batch deduplication
// (multiple scraped rows for the same key in the receiver). The key uses
// ListedVenueName rather than the resolved venue_id because scraped concerts
// have not yet been venue-resolved; ListedVenueName is the upstream identity
// that survives both sides of the comparison.
//
// start_time disambiguates within a (date, venue), but asymmetrically so the
// downstream resolution stays correct:
//   - two KNOWN start times that differ → distinct shows (matinee/evening —
//     昼夜2公演) → both kept.
//   - a scraped entry with NO start time, when anything is already known at that
//     (date, venue) → it adds no information → dropped.
//   - a scraped entry WITH a start time, when only an unknown-start row exists at
//     that (date, venue) → kept, so the creation path can fill the announced
//     time onto the existing row instead of dropping it here.
//
// The creation path resolves event identity authoritatively (exact dedup, fill,
// or insert); this filter only avoids redundant publish/UPSERT round-trips.
//
// Returns nil if no new concerts remain after filtering.
func (ss ScrapedConcerts) FilterNew(existing []*Concert) ScrapedConcerts {
	type vdKey struct {
		date  string
		venue string
	}
	// seen maps (date, venue) → the set of StartKey values already recorded
	// there, with "" representing an unknown-start row. The asymmetric rule
	// reads off this one map: an unknown-start entry is redundant iff anything
	// is recorded (len>0); a known-start entry is redundant only iff that exact
	// StartKey is recorded (the "" sentinel never matches a known start, so an
	// unknown-start row never absorbs a known start the creation path must fill).
	seen := make(map[vdKey]map[string]bool)
	mark := func(k vdKey, start string) {
		if seen[k] == nil {
			seen[k] = make(map[string]bool)
		}
		seen[k][start] = true
	}
	for _, ex := range existing {
		// Skip NULL-venue existing rows from dedup tracking — a row inserted
		// before listed_venue_name was stored has no upstream identity to dedup
		// against, and keying it as {date, ""} would silently swallow a
		// legitimate re-scrape that now carries the real venue name. Symmetric
		// with the empty-venue scraped skip below.
		if ex.ListedVenueName == nil {
			continue
		}
		mark(vdKey{date: ex.LocalDate.Format("2006-01-02"), venue: *ex.ListedVenueName}, StartKey(ex.StartTime))
	}

	var result ScrapedConcerts
	for _, s := range ss {
		// Drop blank-venue (TBA) scraped entries entirely. They have no
		// natural-key identity to dedup against on the creation path —
		// CreateFromDiscovered already skips them with a Warn before insert —
		// AND they have no useful content for the search path: ToConcert on the
		// search side produces an empty VenueID / event ID, so the client would
		// receive malformed concert rows with empty wrappers. Dropping at the
		// FilterNew boundary is the single chokepoint for both consumers.
		if s.ListedVenueName == "" {
			continue
		}
		k := vdKey{date: s.LocalDate.Format("2006-01-02"), venue: s.ListedVenueName}
		start := StartKey(NullableTime(s.StartTime))
		var dup bool
		if start == "" {
			// Unknown start: redundant if anything is already known here.
			dup = len(seen[k]) > 0
		} else {
			// Known start: redundant only if this exact time was already seen;
			// an existing unknown-start row does NOT absorb it (it must pass so
			// the creation path can fill the announced time).
			dup = seen[k][start]
		}
		if dup {
			continue
		}
		mark(k, start)
		result = append(result, s)
	}
	return result
}

// ProximityTo determines the geographic proximity of this concert's venue
// relative to the given user home area.
//
// Classification rules (evaluated in order):
//  1. AWAY — if home is nil or venue is nil.
//  2. HOME — venue admin_area matches the user's home Level1 (ISO 3166-2 code).
//  3. NEARBY — venue has Coordinates and home has Centroid, and the Haversine distance <= NearbyThresholdKm.
//  4. AWAY — everything else.
func (c *Concert) ProximityTo(home *Home) Proximity {
	if home == nil {
		return ProximityAway
	}
	venue := c.Venue
	if venue == nil {
		return ProximityAway
	}

	// HOME: admin_area match takes priority.
	if venue.AdminArea != nil && *venue.AdminArea == home.Level1 {
		return ProximityHome
	}

	// NEARBY: requires venue coordinates and home centroid.
	if venue.Coordinates == nil || home.Centroid == nil {
		return ProximityAway
	}

	dist := geo.Haversine(home.Centroid.Latitude, home.Centroid.Longitude, venue.Coordinates.Latitude, venue.Coordinates.Longitude)
	if dist <= NearbyThresholdKm {
		return ProximityNearby
	}
	return ProximityAway
}

// ProximityGroup contains concerts for a single calendar date, classified into
// three geographic proximity buckets relative to the user's home area.
type ProximityGroup struct {
	// Date is the calendar date for this group.
	Date time.Time
	// Home contains concerts at venues within the user's home admin_area.
	Home []*Concert
	// Nearby contains concerts at venues within 200km of the user's home centroid.
	Nearby []*Concert
	// Away contains concerts beyond 200km, with unknown location, or when the user has no home set.
	Away []*Concert
}

// GroupByDateAndProximity classifies concerts into home/nearby/away buckets
// and groups them by calendar date. Concerts are expected to be ordered by
// local_event_date ascending, which is preserved in the returned slice.
func GroupByDateAndProximity(concerts []*Concert, home *Home) []*ProximityGroup {
	if len(concerts) == 0 {
		return nil
	}

	groups := make(map[string]*ProximityGroup)
	var order []string // preserve date ordering

	for _, c := range concerts {
		dateKey := c.LocalDate.Format("2006-01-02")
		g, ok := groups[dateKey]
		if !ok {
			g = &ProximityGroup{Date: c.LocalDate}
			groups[dateKey] = g
			order = append(order, dateKey)
		}

		switch c.ProximityTo(home) {
		case ProximityHome:
			g.Home = append(g.Home, c)
		case ProximityNearby:
			g.Nearby = append(g.Nearby, c)
		default:
			g.Away = append(g.Away, c)
		}
	}

	result := make([]*ProximityGroup, 0, len(order))
	for _, key := range order {
		result = append(result, groups[key])
	}
	return result
}

// ConcertRepository defines the data access interface for Concerts.
type ConcertRepository interface {
	// ListByArtist retrieves all concerts where the given artist appears in
	// event_performers. if upcomingOnly is true, it only returns concerts with
	// LocalDate >= today.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the artist ID is empty.
	ListByArtist(ctx context.Context, artistID string, upcomingOnly bool) ([]*Concert, error)
	// ListByFollower retrieves all concerts for artists followed by the given user,
	// ordered by local_event_date ascending.
	ListByFollower(ctx context.Context, userID string) ([]*Concert, error)
	// ListByArtists retrieves concerts where any of the given artists appear in
	// event_performers, in a single query. Venue coordinates are included for
	// proximity classification. Results are ordered by local_event_date ascending.
	ListByArtists(ctx context.Context, artistIDs []string) ([]*Concert, error)
	// Create persists one or more concerts using bulk insert with UPSERT semantics.
	//
	// Each concert must already carry an embedded Series whose row has been
	// inserted (or will be inserted in the same transaction) via SeriesRepository.
	// Events are inserted with ON CONFLICT on the natural key
	// (series_id, local_event_date, venue_id). When a conflict is detected the
	// existing row is preserved and event_performers links are reconciled.
	//
	// Concert rows are only inserted for genuinely new events. Performer links
	// (event_performers) are inserted for every concert in the batch and use
	// ON CONFLICT DO NOTHING so re-runs are idempotent.
	//
	// Nil elements in the input slice are silently skipped.
	//
	// Returns the event IDs of concerts that were genuinely inserted (i.e.,
	// not deduplicated by natural-key UPSERT). Callers must not assume the
	// returned slice matches the input slice in length or order.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: If a foreign key constraint is violated (e.g., invalid series, venue, or performer).
	Create(ctx context.Context, concerts ...*Concert) ([]string, error)
	// ListByIDs retrieves concerts by their event IDs. Venues, parent Series,
	// and Performers are all populated so callers can render the response
	// without follow-up queries. IDs that do not match any row are silently
	// omitted from the result.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the ids slice is empty.
	ListByIDs(ctx context.Context, ids []string) ([]*Concert, error)
	// FindEventsByVenueAndDate returns existing events occurring at any of the
	// given (venue_id, local_event_date) pairs. The two slices are zipped
	// element-wise into pairs; an event matches when its (venue_id,
	// local_event_date) equals any supplied pair. Used by the discovery write
	// path to (1) adopt a parent series from already-persisted member events and
	// (2) detect a row first seen without a start_at so a later-announced time
	// fills it instead of inserting a duplicate.
	//
	// Only physical-identity and parentage fields are populated (ID, SeriesID,
	// VenueID, LocalDate, StartTime); Venue and Performers are not hydrated.
	// Returns an empty slice (no error) when the inputs are empty.
	FindEventsByVenueAndDate(ctx context.Context, venueIDs []string, dates []time.Time) ([]*Event, error)
	// FillEventStartTimes sets start_at / open_at on existing events identified
	// by eventIDs, only where the column is currently NULL (COALESCE), for the
	// case where a later discovery supplies a time the first discovery lacked.
	// The three slices are zipped element-wise; a nil time leaves the column
	// unchanged. Idempotent: a no-op when eventIDs is empty.
	FillEventStartTimes(ctx context.Context, eventIDs []string, startTimes, openTimes []*time.Time) error
	// List retrieves every published concert with Series, Venue, and Performers
	// hydrated, ordered by local_event_date ascending. Unlike ListByArtist /
	// ListByFollower it applies no audience filter — it returns the whole
	// published catalog for admin review and management.
	List(ctx context.Context) ([]*Concert, error)
	// Delete removes a published event by id. The delete cascades through the
	// database's foreign keys to every row referencing the event (event_performers,
	// concerts, tickets, ticket_journeys, ticket_emails, merkle_tree, and the
	// parent series' sales_phases). It is idempotent: deleting an id that no
	// longer exists is a no-op success.
	Delete(ctx context.Context, eventID string) error
}

// ConcertSearcher defines the interface for searching concerts from external sources.
type ConcertSearcher interface {
	// Search uses an external service (e.g., Gemini) to find concerts for an artist.
	// It relies on the artist's name and official site URL for grounding.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the artist or official site is invalid.
	//  - Unavailable: If the external service is down.
	//  - Internal: unexpected failure during search processing.
	Search(ctx context.Context, artist *Artist, officialSite *OfficialSite, from time.Time) ([]*ScrapedConcert, error)
}
