package entity

import (
	"context"
	"time"

	"github.com/liverty-music/backend/pkg/geo"
)

// Concert is the music kind of [Event]: one Event together with the artists
// performing at it. Its id is the Event's id, and it has no title, type or
// source page of its own; they are read from its [Series].
//
// Reads populate Series and Artists so a usecase has the whole Concert. On the
// wire (liverty_music.entity.v1.Concert) a Concert carries only the Event and
// the artist ids; the RPC mapper returns each referenced Series and Artist once
// beside the Concerts.
type Concert struct {
	Event
	// Series is the parent series that aggregates this concert with any sibling
	// events sharing the same tour, festival, or multi-day run. Populated by
	// the repository layer on read; required when building a Concert for write.
	Series *Series
	// Artists are the artists performing at this concert, in display order.
	// Always contains at least one artist; more than one covers festivals,
	// co-headliners, and support acts. Populated by the repository layer on
	// read.
	Artists []*Artist
}

// IsVisibleToFans reports whether the concert is visible to fans: exactly when
// its Event is publicly visible (see [Event.IsPubliclyVisible]). Its publish
// state is its Event's.
func (c *Concert) IsVisibleToFans() bool {
	return c.IsPubliclyVisible(c.Series)
}

// EventsOf returns the Event of each concert, in order.
func EventsOf(concerts []*Concert) []*Event {
	events := make([]*Event, len(concerts))
	for i, c := range concerts {
		events[i] = &c.Event
	}
	return events
}

// Performers returns every artist performing at any of the concerts, once
// each, in the order the concerts first name them.
func Performers(concerts []*Concert) []*Artist {
	var artists []*Artist
	seen := make(map[string]bool)
	for _, c := range concerts {
		for _, a := range c.Artists {
			if a == nil || seen[a.ID] {
				continue
			}
			seen[a.ID] = true
			artists = append(artists, a)
		}
	}
	return artists
}

// VenueName is the venue's canonical name, falling back to the listed one.
func (c *Concert) VenueName() string {
	if c.Venue != nil && c.Venue.Name != "" {
		return c.Venue.Name
	}
	if c.ListedVenueName != nil {
		return *c.ListedVenueName
	}
	return ""
}

// ArtistIDs returns the IDs of all artists performing at this concert.
// Convenient for callers that only need identifiers (e.g. mock comparisons,
// repository writes, or hype checks against followed_artists).
//
// Nil entries in Artists (which the type system permits even though the
// supported insert path rejects them) are skipped silently rather than
// triggering a nil-pointer panic, so read-side callers can safely call this
// on any Concert hydrated from external code paths or test fixtures.
func (c *Concert) ArtistIDs() []string {
	if len(c.Artists) == 0 {
		return nil
	}
	ids := make([]string, 0, len(c.Artists))
	for _, p := range c.Artists {
		if p == nil {
			continue
		}
		ids = append(ids, p.ID)
	}
	return ids
}

// DiscoveredEvent is one physical show (a single date at a single venue) within
// a discovered series. It is the leaf of the series-grouped discovery model:
// series-level metadata (title, type, source URL) lives on the parent
// [DiscoveredSeries], never on the event, so it cannot be duplicated per date.
//
// JSON tags support serialization as the create-from-discovered workflow input.
type DiscoveredEvent struct {
	// ListedVenueName is the raw venue name as listed in the source data.
	ListedVenueName string `json:"listed_venue_name"`
	// AdminArea is the administrative area (prefecture, state, province) where the
	// venue is located. Nil when it could not be determined with confidence.
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
}

// DiscoveredEventKey is what makes two discovered events of one search the same
// show: the same date, the same venue ([VenueRepeatKey]) and the same start
// time. See [DiscoveredEvent.RepeatKey].
type DiscoveredEventKey struct {
	LocalDate string
	Venue     string
	// StartTime is the start time in Unix seconds, 0 when it is unknown.
	StartTime int64
}

// RepeatKey returns the key under which a search drops the repeats of an
// event: its local date, its venue as [VenueRepeatKey] gives it and its start
// time. The start time is part of it so that two shows on the same date at
// the same venue (a 1st and a 2nd stage) stay distinct events. Two events that
// share the date and venue and both lack a start time are one: the search
// dedups conservatively when the disambiguator is missing, which loses two
// genuinely distinct shows announced before their start times are published.
func (e *DiscoveredEvent) RepeatKey() DiscoveredEventKey {
	key := DiscoveredEventKey{LocalDate: e.LocalDate.Format("2006-01-02"), Venue: VenueRepeatKey(e.ListedVenueName)}
	if !e.StartTime.IsZero() {
		key.StartTime = e.StartTime.Unix()
	}
	return key
}

// HasPassedAt reports whether the event's local date is before the current
// date at its Venue at t ([CurrentDateAtVenue]): the whole event day still
// counts as upcoming, and at 23:30 UTC the Venue is already on the next day.
func (e *DiscoveredEvent) HasPassedAt(t time.Time) bool {
	return e.LocalDate.Before(CurrentDateAtVenue(t))
}

// ClassifyType sets the series' type from its venues, following the SeriesType
// definitions: TOUR when its events are at two or more venues (compared by
// [VenueRepeatKey], ignoring venues not yet announced), SINGLE otherwise, a
// one-off show or several days at one venue.
func (s *DiscoveredSeries) ClassifyType() {
	venues := make(map[string]struct{}, len(s.Events))
	for _, ev := range s.Events {
		if v := VenueRepeatKey(ev.ListedVenueName); v != "" {
			venues[v] = struct{}{}
		}
	}
	s.Type = SeriesTypeSingle
	if len(venues) >= 2 {
		s.Type = SeriesTypeTour
	}
}

// ToConcertUnderSeries builds a persist-ready Concert for this event under an
// already-existing series identified by seriesID. Unlike
// [DiscoveredSeries.ToConcert] it embeds no Series shell: the series row already
// exists (created when the group's series_id was resolved), and the repository
// insert reads only SeriesID — so there is no series metadata to carry or risk
// overwriting. Used by both the discovery auto-publish path and the admin
// approval path.
func (e *DiscoveredEvent) ToConcertUnderSeries(artistID, seriesID, eventID, venueID string) *Concert {
	listedName := e.ListedVenueName
	c := &Concert{
		ID:              eventID,
		SeriesID:        seriesID,
		VenueID:         venueID,
		ListedVenueName: &listedName,
		LocalDate:       e.LocalDate,
		Artists:         []*Artist{{ID: artistID}},
	}
	c.StartTime = NullableTime(e.StartTime)
	c.OpenTime = NullableTime(e.OpenTime)
	return c
}

// ConcertDiscoveredData is the input of the create-from-discovered workflow.
// It carries the newly discovered concerts for one artist (post-deduplication),
// grouped by series so a tour's dates persist under one series by construction.
// Started by SearchNewConcerts after the external API call and dedup.
type ConcertDiscoveredData struct {
	// ArtistID is the internal UUID of the artist.
	ArtistID string `json:"artist_id"`
	// ArtistName is the display name of the artist (for notification context).
	ArtistName string `json:"artist_name"`
	// Series is the list of newly discovered, deduplicated series, each carrying
	// its member events. A <tour> block is one TOUR series with many events; a
	// standalone show is one SINGLE series with a single event.
	Series []*DiscoveredSeries `json:"series"`
}

// EventCount returns the total number of member events across all series in the
// batch. Used for logging/metrics where the flat event count is meaningful.
func (d ConcertDiscoveredData) EventCount() int {
	return SumSeriesEvents(d.Series)
}

// DiscoveredSeries groups the events of one discovered tour or standalone show
// under shared series-level metadata. Grouping is structural — every event in
// Events belongs to this one series — so per-event fragmentation is impossible
// by construction. The searcher sets Type from the venues: TOUR when the events
// are at two or more venues, SINGLE otherwise.
//
// JSON tags support serialization as the create-from-discovered workflow input.
type DiscoveredSeries struct {
	// Title is the series title (tour or show name), shared across all events.
	Title string `json:"title"`
	// Type classifies the series: TOUR for a multi-venue/multi-date run, SINGLE
	// for a standalone show.
	Type SeriesType `json:"type"`
	// SourceURL is the series-level official URL (tour/feature page or the
	// specific announcement article). Empty when no canonical landing page is
	// known. Stored once per series, never duplicated per event.
	SourceURL string `json:"source_url,omitempty"`
	// Events are the member events of this series, one per date.
	Events []*DiscoveredEvent `json:"events"`
}

// ToConcert converts one member event of a discovered series into a
// fully-populated Concert entity.
//
// The caller supplies the parent seriesID plus the event and venue IDs. The
// returned Concert embeds the Series shell — with the title, type, and source
// URL copied from the discovered series — so the caller can pass the same Series
// instance into SeriesRepository.Create alongside ConcertRepository.Create.
//
// Two usage patterns exist:
//
//   - Search path (concert_uc): pass empty strings for eventID and venueID. The
//     returned Concert is for immediate return to callers and is never persisted;
//     the embedded Series is non-canonical.
//   - Creation path (concert_creation_uc): pass UUIDs for all IDs. The returned
//     Concert is bulk-inserted into the database, and the Series row is inserted
//     in the same use-case run via SeriesRepository.
func (s *DiscoveredSeries) ToConcert(ev *DiscoveredEvent, artistID, seriesID, eventID, venueID string) *Concert {
	listedName := ev.ListedVenueName
	series := &Series{
		ID:        seriesID,
		Title:     s.Title,
		Type:      s.Type,
		SourceURL: s.SourceURL,
	}
	c := &Concert{
		ID:              eventID,
		SeriesID:        seriesID,
		VenueID:         venueID,
		ListedVenueName: &listedName,
		LocalDate:       ev.LocalDate,
		Series:          series,
		Artists:         []*Artist{{ID: artistID}},
	}
	c.StartTime = NullableTime(ev.StartTime)
	c.OpenTime = NullableTime(ev.OpenTime)
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

// NaturalKey is the physical identity of an event: its Venue, local date and
// start time, where an unknown start time keys as the empty string
// (`(venue_id, local_event_date, start_at) NULLS NOT DISTINCT`; see
// [StartKey]). Two events with the same key are the same show.
func (c *Concert) NaturalKey() string {
	return c.VenueID + "|" + c.LocalDate.Format("2006-01-02") + "|" + StartKey(c.StartTime)
}

// DedupByNaturalKey returns concerts without nil entries and without the
// repeats of a natural key, keeping the first of each, and the repeats it
// dropped. One statement cannot upsert two rows of one natural key
// ("ON CONFLICT DO UPDATE command cannot affect row a second time"), which two
// scraped entries for one physical venue under different listed names reach
// once they resolve to the same Venue.
func DedupByNaturalKey(concerts []*Concert) (kept, dropped []*Concert) {
	seen := make(map[string]struct{}, len(concerts))
	for _, c := range concerts {
		if c == nil {
			continue
		}
		key := c.NaturalKey()
		if _, dup := seen[key]; dup {
			dropped = append(dropped, c)
			continue
		}
		seen[key] = struct{}{}
		kept = append(kept, c)
	}
	return kept, dropped
}

// NotifiableEventIDs returns the events a concert creation has something new
// to tell followers about: the events inserted, and the events a performer was
// newly linked to, each once, in that order. A new event is in both sets; an
// event an earlier run created and a co-headliner was newly attached to is only
// in the second; a re-delivery, where the event and every link already exist,
// is in neither.
func NotifiableEventIDs(inserted, linked []string) []string {
	out := make([]string, 0, len(inserted)+len(linked))
	seen := make(map[string]bool, len(inserted)+len(linked))
	for _, ids := range [][]string{inserted, linked} {
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// FilterNewSeries drops events that already conflict with existing concerts (or
// duplicate another event within this same batch) from each discovered series,
// then drops any series left with no events. It is a best-effort upstream filter
// aligned with the events physical natural key
// `(venue_id, local_event_date, start_at)`, applied per event while preserving
// the series grouping so a tour that partially overlaps known dates still
// persists its new dates under one series.
//
// The key uses ListedVenueName rather than the resolved venue_id because
// discovered events have not yet been venue-resolved; ListedVenueName is the
// upstream identity that survives both sides of the comparison. The name
// component is compared through NormalizeVenueName so Gemini's formatting drift
// across runs (full/half-width, whitespace, "大阪・"/"大阪公演 ＠" location prefixes)
// does not defeat the match; the (date, start_at) components are unchanged.
//
// start_time disambiguates within a (date, venue), but asymmetrically so the
// downstream resolution stays correct:
//   - two KNOWN start times that differ → distinct shows (matinee/evening —
//     昼夜2公演) → both kept.
//   - an event with NO start time, when anything is already known at that
//     (date, venue) → it adds no information → dropped.
//   - an event WITH a start time, when only an unknown-start row exists at that
//     (date, venue) → kept, so the creation path can fill the announced time
//     onto the existing row instead of dropping it here.
//
// The creation path resolves event identity authoritatively (exact dedup, fill,
// or insert); this filter only avoids redundant publish/UPSERT round-trips.
//
// Returns nil if no new events remain in any series after filtering.
func FilterNewSeries(series []*DiscoveredSeries, existing []*Concert) []*DiscoveredSeries {
	// seen maps (date, venue) → the set of StartKey values already recorded
	// there, with "" representing an unknown-start row. The asymmetric rule
	// reads off this one map: an unknown-start entry is redundant iff anything
	// is recorded (len>0); a known-start entry is redundant only iff that exact
	// StartKey is recorded (the "" sentinel never matches a known start, so an
	// unknown-start row never absorbs a known start the creation path must fill).
	seen := make(map[string]map[string]bool)
	mark := func(k, start string) {
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
		// with the empty-venue event skip below.
		if ex.ListedVenueName == nil {
			continue
		}
		mark(DateVenueKey(ex.LocalDate, *ex.ListedVenueName), StartKey(ex.StartTime))
	}

	var result []*DiscoveredSeries
	for _, s := range series {
		if s == nil {
			continue
		}
		var kept []*DiscoveredEvent
		for _, ev := range s.Events {
			if ev == nil {
				continue
			}
			// Drop blank-venue (TBA) events entirely. They have no natural-key
			// identity to dedup against on the creation path (CreateFromDiscovered
			// skips them with a Warn before insert) AND no useful content for the
			// search path (ToConcert produces an empty VenueID / event ID, so the
			// client would receive malformed concert rows with empty wrappers).
			// Dropping here is the single chokepoint for both consumers.
			if ev.ListedVenueName == "" {
				continue
			}
			k := DateVenueKey(ev.LocalDate, ev.ListedVenueName)
			start := StartKey(NullableTime(ev.StartTime))
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
			kept = append(kept, ev)
		}
		if len(kept) == 0 {
			continue
		}
		// Copy the series shell with only the surviving events so the caller's
		// input is not mutated.
		ns := *s
		ns.Events = kept
		result = append(result, &ns)
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

// EarliestConcert returns the earliest concert in the slice, ordered by local
// date ascending and tie-broken by start time ascending. A known start time
// precedes an unknown (nil) one on the same date; any remaining tie is broken by
// ID so the result is deterministic. Nil elements are skipped. Returns nil for
// an empty slice or a slice of only nil elements.
//
// This is the deep-link target for a new-concert push notification: the earliest
// concert of the recipient's hype-matched subset.
func EarliestConcert(concerts []*Concert) *Concert {
	var earliest *Concert
	for _, c := range concerts {
		if c == nil {
			continue
		}
		if earliest == nil || concertEarlier(c, earliest) {
			earliest = c
		}
	}
	return earliest
}

// concertEarlier reports whether concert a sorts before concert b under the
// [EarliestConcert] ordering: local date asc, then start time asc (known before
// unknown), then ID asc as a stable final tie-break.
func concertEarlier(a, b *Concert) bool {
	if !a.LocalDate.Equal(b.LocalDate) {
		return a.LocalDate.Before(b.LocalDate)
	}
	switch as, bs := a.StartTime, b.StartTime; {
	case as != nil && bs != nil:
		if !as.Equal(*bs) {
			return as.Before(*bs)
		}
	case as != nil && bs == nil:
		return true
	case as == nil && bs != nil:
		return false
	}
	return a.ID < b.ID
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
	// ListByArtist returns every Concert whose performers include the artist,
	// of every date, ordered by local date ascending, each with its Venue
	// (name and admin area), Series and performers. Concerts that are not
	// visible to fans are left out: a DRAFT or CANCELLED event of a
	// first-party series, and every event of an UNLISTED one. An artist with
	// no concerts, or an unknown artist, gives an empty list.
	// [ConcertRepository.ListUpcomingByArtist] lists only the concerts from
	// the current date onward.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the artist ID is empty.
	ListByArtist(ctx context.Context, artistID string) ([]*Concert, error)
	// ListUpcomingByArtist returns every Concert whose performers include the
	// artist and whose local date is on or after the current date at its
	// Venue at now ([CurrentDateAtVenue]), ordered by local date ascending,
	// each with its Venue (name and admin area), Series and performers.
	// Concerts that are not visible to fans are left out, as in
	// [ConcertRepository.ListByArtist]. An artist with no upcoming concerts,
	// or an unknown artist, gives an empty list.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the artist ID is empty.
	ListUpcomingByArtist(ctx context.Context, artistID string, now time.Time) ([]*Concert, error)
	// ListByFollower returns, once each, every Concert at which an artist the
	// user follows performs and whose local date is on or after from, ordered
	// by local date ascending, each with its Venue with coordinates when
	// known. A nil from means the current date at the Venue
	// ([CurrentDateAtVenue]). Concerts that are not visible to fans are left
	// out, as in [ConcertRepository.ListByArtist].
	ListByFollower(ctx context.Context, userID string, from *time.Time) ([]*Concert, error)
	// ListByArtists returns, once each, every Concert whose performers
	// include any of the given artists, past and upcoming, in a single query,
	// ordered by local date ascending, each with its Venue with coordinates
	// when known for proximity classification. Concerts that are not visible
	// to fans are left out, as in [ConcertRepository.ListByArtist].
	ListByArtists(ctx context.Context, artistIDs []string) ([]*Concert, error)
	// ListByLocation returns the candidate Concerts dated from from to to
	// inclusive whose Venue shares the location's admin area or could lie
	// within 200 km of its point, ordered by local date ascending, each with
	// its Venue with coordinates when known. Concerts farther than 200 km can
	// be among the candidates (the caller applies the exact distance cut),
	// but none in the same admin area or within 200 km is left out. Concerts
	// that are not visible to fans are left out, as in
	// [ConcertRepository.ListByArtist].
	ListByLocation(ctx context.Context, location *GeoLocation, from, to time.Time) ([]*Concert, error)
	// Create stores the given Concerts under Series that already exist, all
	// of them with their performers or none.
	//
	// A Concert's slot is its Venue, local date and start time; an unknown
	// start time matches only an unknown start time. With no event at the
	// slot, Create stores a new event. With one there, Create keeps that
	// event and its id, fills its start and open time only where they are
	// unknown, never overwrites a known time, and adds the Concert's
	// performers to it. Concerts in one call that share a slot are stored
	// once. A DRAFT event holds no slot and is never merged into.
	//
	// Nil elements in the input slice are skipped.
	//
	// It returns the ids of the events that are new, together with the ids of
	// existing events to which one of the Concerts' performers was newly
	// added, each id once. An event that already had every given performer
	// is not returned, so the result need not match the input in length or
	// order.
	//
	// # Possible errors
	//
	//  - InvalidArgument: A Concert has no id, no Venue, no Series or no
	//    performer. Nothing is stored.
	//  - FailedPrecondition: A referenced Series, Venue or Artist does not
	//    exist. Nothing is stored.
	//  - Unavailable: An announcement could not be recorded; nothing is
	//    stored.
	//
	// # Announces
	//
	// CONCERT.created once for each Artist newly linked to events of one Series,
	// with those Concerts (ce_subject the Artist id), together with the change.
	// Nothing when no link is new.
	Create(ctx context.Context, concerts ...*Concert) ([]string, error)
	// ListByIDs returns the Concert for each given id that exists, ordered by
	// local date ascending, each with its Venue with coordinates when known,
	// Series and performers, whatever its visibility to fans. An id that
	// matches no Concert is left out without error; the id of a DRAFT event
	// matches no Concert.
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
	// VenueID, LocalDate, StartTime); Venue and Artists are not hydrated.
	// A DRAFT event is not a Concert and is never returned. Returns an empty
	// slice (no error) when the inputs are empty or no event matches.
	FindEventsByVenueAndDate(ctx context.Context, venueIDs []string, dates []time.Time) ([]*Event, error)
	// FindEventsByArtistAndDate returns existing events on any of the given local
	// event dates at which the artist already performs (joined through
	// concert_artists). Used by the discovery write path to resolve a whole
	// discovered series' identity BEFORE any Places API call: a full
	// local_event_date makes an (artist, exact-date) collision across two
	// different series practically impossible, so any matching event already
	// belongs to this tour and its series_id is adopted for the group.
	//
	// Only physical-identity and parentage fields are populated (ID, SeriesID,
	// VenueID, ListedVenueName, LocalDate, StartTime); Venue and Artists are
	// not hydrated. A DRAFT event is not a Concert and is never returned.
	// Returns an empty slice (no error) when the inputs are empty or no event
	// matches.
	FindEventsByArtistAndDate(ctx context.Context, artistID string, dates []time.Time) ([]*Event, error)
	// ListEventsBySeries retrieves every event belonging to the given series,
	// ordered by local_event_date ascending and, for equal dates, by start_at
	// ascending with a NULL start_at ordered last. Used to resolve which event
	// of a series a series-level notification should deep-link to (see
	// TicketSaleAnnouncementUseCase and TicketSaleReminderUseCase).
	//
	// Only physical-identity fields are populated (ID, SeriesID, LocalDate,
	// StartTime); Venue and Artists are not hydrated. A DRAFT event is not a
	// Concert and is never returned. Returns an empty slice (no error) when
	// the series has no such event.
	ListEventsBySeries(ctx context.Context, seriesID string) ([]*Event, error)
	// FillEventStartTimes sets start_at / open_at on existing events identified
	// by eventIDs, only where the column is currently NULL (COALESCE), for the
	// case where a later discovery supplies a time the first discovery lacked.
	// The three slices are zipped element-wise; a nil time leaves the column
	// unchanged. An unknown event id is ignored, and so is the id of a DRAFT
	// event, which is not a Concert: its times are not filled. Idempotent: a
	// no-op when eventIDs is empty.
	FillEventStartTimes(ctx context.Context, eventIDs []string, startTimes, openTimes []*time.Time) error
	// UpdateEventListedVenueName overwrites the listed_venue_name display field of
	// an existing event. Used by the admin approval "adopt the candidate" reconciliation
	// to replace the stored display name with the concert candidate's, leaving venue_id
	// and google_place_id untouched. Idempotent on a missing id (no-op success).
	UpdateEventListedVenueName(ctx context.Context, eventID string, listedVenueName string) error
	// List retrieves every published concert with Series, Venue, and Artists
	// hydrated, ordered by local_event_date ascending. Unlike ListByArtist /
	// ListByFollower it applies no audience filter — it returns the whole
	// published catalog for admin review and management.
	List(ctx context.Context) ([]*Concert, error)
	// ListCatalogByArtist retrieves every published concert the artist
	// performs in, hydrated like List and with no audience filter: past and
	// upcoming, of every series whatever its visibility, CANCELLED ones
	// included, ordered by local_event_date ascending. It returns an empty
	// list when the artist performs in none or no artist has the id.
	//
	// # Possible errors
	//
	//  - InvalidArgument: no artist is given.
	//  - Internal: database query failure.
	ListCatalogByArtist(ctx context.Context, artistID string) ([]*Concert, error)
	// Delete removes a published event by id. The delete cascades through the
	// database's ON DELETE CASCADE foreign keys to every row scoped to the
	// event alone (the 1:1 concerts row, concert_artists, ticket_journeys,
	// ticket_sale_events and ticket_stocks). Rows that outlive the event but
	// still reference it — orders, tickets and settlements — are protected by
	// ON DELETE RESTRICT, so the delete fails instead of orphaning them when
	// any exist. The event's series and the series' discovered ticket sales
	// are untouched either way: a discovered sale names no event, so deleting
	// one event never removes it. It is idempotent: an id that no longer
	// exists is a no-op success, and so is the id of a DRAFT event, which is
	// not a Concert and is not removed.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: an Order, Ticket or Settlement still refers to
	//    the event. Nothing is removed.
	Delete(ctx context.Context, eventID string) error
	// DeleteAndSuppress removes a published event by id together with every
	// record that depends only on it: its performers, Concert and ticket
	// journeys; its TicketStocks with their Void and Refunded Orders and those
	// Orders' lottery entries and Checkouts; the Tickets of those Orders and
	// the Tickets' ResaleListings; the Reversed Settlements of those Orders;
	// the event's Scanners with their admissions and rejected scans; every
	// Organizer's TicketSale the removal leaves naming no event; and the
	// event's first-party Series, with its SeatTypes, when it is left with no
	// event. A discovered Series and its discovered TicketSales stay. In the
	// same transaction it records a suppression entry for the event's
	// (venue_id, local_event_date, start_at) so a later discovery run does not
	// auto-publish or re-stage it.
	//
	// It is idempotent: an id that no longer exists, or a DRAFT event, is a
	// no-op success that records no suppression, and a repeated suppression of
	// the same key is absorbed.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the event has a Paid Order, a Settlement that is
	//    not Reversed, or a purchase in progress (a Pending Order).
	DeleteAndSuppress(ctx context.Context, eventID string) error
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
	Search(ctx context.Context, artist *Artist, officialSite *OfficialSite, from time.Time) ([]*DiscoveredSeries, error)
}
