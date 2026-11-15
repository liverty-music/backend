package entity

import (
	"log/slog"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// MaxLocationDateRange is the longest span of a [LocationQuery]'s dates. The
// proto constraint only enforces from <= to (protovalidate CEL has no
// calendar-day arithmetic for google.type.Date), so the cap is checked here.
const MaxLocationDateRange = 30 * 24 * time.Hour

// LocationQuery asks for the concerts near a reference point on the dates
// From to To (the All Nearby view).
type LocationQuery struct {
	// Location is the reference point.
	Location *GeoLocation
	// From is the first date asked for.
	From time.Time
	// To is the last date asked for.
	To time.Time
}

// Check reports whether the query may run: its dates span at most
// [MaxLocationDateRange].
//
// # Possible errors
//
//   - InvalidArgument: the dates span more than 30 days.
func (q LocationQuery) Check() error {
	if q.To.Sub(q.From) > MaxLocationDateRange {
		return apperr.New(codes.InvalidArgument,
			"date range must not exceed 30 days",
			slog.Time("from", q.From),
			slog.Time("to", q.To),
		)
	}
	return nil
}

// NearbyGroups groups concerts by date and proximity to the reference point
// and keeps only what the All Nearby view shows: the HOME and NEARBY concerts
// of each date. A date with neither is dropped, and AWAY concerts are never
// returned (the bounding-box prefilter of the search admits venues the
// distance cut then classifies as AWAY).
func (q LocationQuery) NearbyGroups(concerts []*Concert) []*ProximityGroup {
	groups := GroupByDateAndProximity(concerts, q.Location.AsHome())
	nearby := groups[:0]
	for _, g := range groups {
		if len(g.Home)+len(g.Nearby) == 0 {
			continue
		}
		g.Away = nil
		nearby = append(nearby, g)
	}
	return nearby
}
