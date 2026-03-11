package entity

import (
	"context"
	"time"

	"github.com/liverty-music/backend/pkg/geo"
)

// Concert represents a specific music live event.
//
// Corresponds to liverty_music.entity.v1.Concert.
type Concert struct {
	Event
	// ArtistID is the ID of the artist performing.
	ArtistID string
}

// ScrapedConcert represents raw concert information rediscovered from external sources.
// It lacks system-specific identifiers like ID or ArtistID.
type ScrapedConcert struct {
	// Title is the descriptive title of the scraped event.
	Title string
	// ListedVenueName is the raw venue name as listed in the source data.
	ListedVenueName string
	// AdminArea is the administrative area (prefecture, state, province) where the venue is located.
	// It is nil when the area could not be determined with confidence.
	AdminArea *string
	// LocalDate represents the calendar date of the event.
	// See entity.Concert.LocalDate for detailed specifications.
	LocalDate time.Time
	// StartTime is the specific starting time (optional).
	StartTime *time.Time
	// OpenTime is the time when doors open (optional).
	OpenTime *time.Time
	// SourceURL is the URL where this information was found.
	SourceURL string
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
	// Distant contains concerts beyond 200km, with unknown location, or when the user has no home set.
	Distant []*Concert
}

// GroupByDateAndProximity classifies concerts into home/nearby/distant buckets
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
			g.Distant = append(g.Distant, c)
		}
	}

	result := make([]*ProximityGroup, 0, len(order))
	for _, key := range order {
		result = append(result, groups[key])
	}
	return result
}

// DateVenueKey returns the "(date|venue)" deduplication key for a ScrapedConcert.
func (s *ScrapedConcert) DateVenueKey() string {
	return s.LocalDate.Format("2006-01-02") + "|" + s.ListedVenueName
}

// DedupeKey returns the full deduplication key for a ScrapedConcert.
// When StartTime is non-nil, the UTC-normalized time is appended to the DateVenueKey
// to form "(date|venue|start_at_utc)". When StartTime is nil, DateVenueKey is returned.
func (s *ScrapedConcert) DedupeKey() string {
	base := s.DateVenueKey()
	if s.StartTime == nil {
		return base
	}
	return base + "|" + s.StartTime.UTC().Format("15:04:05Z")
}

// ConcertRepository defines the data access interface for Concerts.
type ConcertRepository interface {
	// ListByArtist retrieves all concerts for a specific artist.
	// if upcomingOnly is true, it only returns concerts with LocalDate >= today.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the artist ID is empty.
	ListByArtist(ctx context.Context, artistID string, upcomingOnly bool) ([]*Concert, error)
	// ListByFollower retrieves all concerts for artists followed by the given user,
	// ordered by local_event_date ascending.
	ListByFollower(ctx context.Context, userID string) ([]*Concert, error)
	// Create creates one or more concerts using bulk insert with UPSERT semantics.
	//
	// Events are inserted with ON CONFLICT on the natural key
	// (venue_id, local_event_date, start_at). When a conflict is detected:
	//   - start_at is updated only if the existing value is NULL (COALESCE).
	//   - open_at is updated only if the existing value is NULL (COALESCE).
	//   - The existing row's non-NULL values are never overwritten.
	//
	// Concert rows are only inserted for genuinely new events. If the event
	// already existed (UPSERT conflict), the corresponding concert row is
	// skipped because the input UUID does not exist in the events table.
	//
	// Nil elements in the input slice are silently skipped.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: If a foreign key constraint is violated (e.g., invalid artist or venue).
	Create(ctx context.Context, concerts ...*Concert) error
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
	Search(ctx context.Context, artist *Artist, officialSite *OfficialSite, from time.Time) ([]*ScrapedConcert, error)
}
