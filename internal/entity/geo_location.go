package entity

import (
	"math"
	"regexp"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// GeoLocation is the reference of a location search: a first-level Area and,
// when the Area has one, its reference point. HOME is the Area; NEARBY is
// measured from the point. The server builds it from an Area; no client sends
// a point.
type GeoLocation struct {
	// AdminArea is the code of the first-level Area (JP-13, HK).
	AdminArea string
	// Point is the Area's reference point; nil when the Area has none, and
	// then nothing is NEARBY.
	Point *Coordinates
}

// firstLevelCodeRe is the form of a first-level Area code.
var firstLevelCodeRe = regexp.MustCompile(`^[A-Z]{2}(-[A-Z0-9]{1,3})?$`)

// Validate checks the GeoLocation: an admin area in the form of a
// first-level Area code, and a point within range when there is one.
//
// # Possible errors
//
//   - InvalidArgument: the admin area or the point is malformed.
func (g *GeoLocation) Validate() error {
	if !firstLevelCodeRe.MatchString(g.AdminArea) {
		return apperr.New(codes.InvalidArgument, "the admin area must be a first-level Area code")
	}
	if g.Point != nil && (g.Point.Latitude < -90 || g.Point.Latitude > 90 || g.Point.Longitude < -180 || g.Point.Longitude > 180) {
		return apperr.New(codes.InvalidArgument, "the point is out of range")
	}
	return nil
}

// LocationOf returns the GeoLocation of a first-level Area.
func LocationOf(area *Area) *GeoLocation {
	return &GeoLocation{AdminArea: area.Code, Point: area.ReferencePoint}
}

// AsHome adapts this GeoLocation into a transient [Home] so it can be passed to
// the shared proximity classifier [GroupByDateAndProximity] / [Concert.ProximityTo].
// The returned Home is not a user record.
func (g *GeoLocation) AsHome() *Home {
	return &Home{Level1: g.AdminArea, ReferencePoint: g.Point}
}

const (
	// LocationBoxLatMargin is the latitude margin, in degrees, of the box
	// the location search reads first: about 200 km.
	LocationBoxLatMargin = 1.8
	// locationBoxLngKmDegrees is the longitude margin at the equator, in
	// degrees, widened by 1/cos(latitude) toward the poles.
	locationBoxLngKmDegrees = 2.0
	// locationBoxPolarLatitude is the latitude from which the box takes
	// every longitude.
	locationBoxPolarLatitude = 85.0
)

// LocationBox is the range the location search reads first around a
// reference point: one latitude range and one or two longitude ranges (two
// when it crosses ±180°), both ends included.
type LocationBox struct {
	LatMin, LatMax float64
	// Lng holds one or two [min, max] longitude ranges.
	Lng [][2]float64
}

// Box returns the box around the point, and false when there is no point.
// It holds every place within [NearbyThresholdKm] of the point at any
// latitude: its longitude margin is 2.0° / cos(min(|lat| + 1.8°, 85°)), and
// it takes every longitude beyond that.
func (g *GeoLocation) Box() (LocationBox, bool) {
	if g.Point == nil {
		return LocationBox{}, false
	}
	lat, lng := g.Point.Latitude, g.Point.Longitude
	box := LocationBox{
		LatMin: math.Max(lat-LocationBoxLatMargin, -90),
		LatMax: math.Min(lat+LocationBoxLatMargin, 90),
	}
	edge := math.Abs(lat) + LocationBoxLatMargin
	if edge >= locationBoxPolarLatitude {
		box.Lng = [][2]float64{{-180, 180}}
		return box, true
	}
	margin := locationBoxLngKmDegrees / math.Cos(edge*math.Pi/180)
	lo, hi := lng-margin, lng+margin
	switch {
	case lo < -180:
		box.Lng = [][2]float64{{lo + 360, 180}, {-180, hi}}
	case hi > 180:
		box.Lng = [][2]float64{{lo, 180}, {-180, hi - 360}}
	default:
		box.Lng = [][2]float64{{lo, hi}}
	}
	return box, true
}

// Contains reports whether the box holds the coordinates.
func (b LocationBox) Contains(c Coordinates) bool {
	if c.Latitude < b.LatMin || c.Latitude > b.LatMax {
		return false
	}
	for _, r := range b.Lng {
		if c.Longitude >= r[0] && c.Longitude <= r[1] {
			return true
		}
	}
	return false
}

// MayBeNear reports whether the location search reads a concert at venue v
// for this reference: the venue is in the admin area, or its coordinates are
// in [GeoLocation.Box]. The search then classifies each concert by distance
// ([LocationQuery.NearbyGroups]); a venue that is not MayBeNear is never HOME
// or NEARBY.
func (g *GeoLocation) MayBeNear(v *Venue) bool {
	if v == nil {
		return false
	}
	if v.AdminArea != nil && *v.AdminArea == g.AdminArea {
		return true
	}
	box, ok := g.Box()
	return ok && v.Coordinates != nil && box.Contains(*v.Coordinates)
}
