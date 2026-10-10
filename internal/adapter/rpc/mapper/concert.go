// Package mapper provides conversion functions between domain entities and Protobuf messages.
package mapper

import (
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	concertv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/concert/v1"
	"github.com/liverty-music/backend/internal/entity"
	"google.golang.org/genproto/googleapis/type/date"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// seriesTypeToProto maps the domain SeriesType string enum onto the generated
// protobuf SeriesType. An unrecognised value falls back to UNSPECIFIED — the
// proto-level not_in: [0] rule on Series.type means a Concert response carrying
// UNSPECIFIED will fail protovalidate, surfacing the bad mapping before it
// reaches the client.
func seriesTypeToProto(t entity.SeriesType) entityv1.SeriesType {
	switch t {
	case entity.SeriesTypeTour:
		return entityv1.SeriesType_SERIES_TYPE_TOUR
	case entity.SeriesTypeSingle:
		return entityv1.SeriesType_SERIES_TYPE_SINGLE
	case entity.SeriesTypeFestival:
		return entityv1.SeriesType_SERIES_TYPE_FESTIVAL
	default:
		return entityv1.SeriesType_SERIES_TYPE_UNSPECIFIED
	}
}

// SeriesToProto converts a domain Series entity to the protobuf Series message.
// Returns nil when the input is nil so callers can pass through optional values.
//
// The first-party attributes (organizer id, description, visibility and publish
// state) are set when the Series has them, so the fan app can tell first-party
// concerts from discovered ones. The cover media needs the CDN base and is set
// by [MediaURLBuilder.SeriesToProto]. The share token is never mapped.
func SeriesToProto(s *entity.Series) *entityv1.Series {
	if s == nil {
		return nil
	}
	proto := &entityv1.Series{
		Id:    &entityv1.SeriesId{Value: s.ID},
		Title: &entityv1.Title{Value: s.Title},
		Type:  seriesTypeToProto(s.Type),
	}
	if s.SourceURL != "" {
		proto.SourceUrl = &entityv1.Url{Value: s.SourceURL}
	}
	if s.Description != nil {
		proto.Description = &entityv1.Description{Value: *s.Description}
	}
	if s.Visibility != nil {
		proto.Visibility = visibilityToProto(*s.Visibility)
	}
	if s.PublishState != nil {
		proto.PublishState = publishStateToProto(*s.PublishState)
	}
	if s.OrganizerID != nil {
		proto.OrganizerId = &entityv1.OrganizerId{Value: *s.OrganizerID}
	}
	if s.Organizer != nil {
		// The seller details are the public 特商法 disclosure; the platform fee
		// rate is the Organizer's business with the platform and is never set.
		proto.Organizer = &entityv1.Organizer{
			Id:            &entityv1.OrganizerId{Value: s.Organizer.ID},
			Name:          &entityv1.OrganizerName{Value: s.Organizer.Name},
			SellerDetails: SellerDetailsToProto(s.Organizer.SellerDetails),
		}
	}
	return proto
}

// ConcertToProto converts a domain Concert to protobuf: its Event and the ids
// of its performing Artists. The Concert's Series and Artists are not
// embedded; a response returns them once each beside the Concerts (see
// [MediaURLBuilder.ReferencedSeries] and [ReferencedArtists]).
func ConcertToProto(c *entity.Concert) *entityv1.Concert {
	if c == nil {
		return nil
	}
	return &entityv1.Concert{
		Event:     EventToProto(&c.Event),
		ArtistIds: artistIDsToProto(c.ArtistIDs()),
	}
}

// ConcertsToProto converts a slice of domain Concert entities to protobuf.
func ConcertsToProto(concerts []*entity.Concert) []*entityv1.Concert {
	protoConcerts := make([]*entityv1.Concert, 0, len(concerts))
	for _, c := range concerts {
		protoConcerts = append(protoConcerts, ConcertToProto(c))
	}
	return protoConcerts
}

// EventToProto converts a domain Event to protobuf. The Venue is set when the
// Event carries it.
func EventToProto(e *entity.Event) *entityv1.Event {
	if e == nil {
		return nil
	}
	proto := &entityv1.Event{
		Id:        &entityv1.EventId{Value: e.ID},
		Venue:     VenueToProto(e.Venue),
		LocalDate: &entityv1.LocalDate{Value: TimeToDate(e.LocalDate)},
	}
	if e.SeriesID != "" {
		proto.SeriesId = &entityv1.SeriesId{Value: e.SeriesID}
	}
	if e.StartTime != nil {
		proto.StartTime = &entityv1.StartTime{Value: timestamppb.New(*e.StartTime)}
	}
	if e.OpenTime != nil {
		proto.OpenTime = &entityv1.OpenTime{Value: timestamppb.New(*e.OpenTime)}
	}
	if e.ListedVenueName != nil {
		proto.ListedVenueName = &entityv1.ListedVenueName{Value: *e.ListedVenueName}
	}
	return proto
}

// artistIDsToProto wraps artist ids in their proto message.
func artistIDsToProto(ids []string) []*entityv1.ArtistId {
	out := make([]*entityv1.ArtistId, 0, len(ids))
	for _, id := range ids {
		out = append(out, &entityv1.ArtistId{Value: id})
	}
	return out
}

// ReferencedArtists returns every Artist the concerts refer to, once each, in
// the order the concerts first refer to them.
func ReferencedArtists(concerts []*entity.Concert) []*entityv1.Artist {
	seen := make(map[string]bool)
	out := make([]*entityv1.Artist, 0)
	for _, c := range concerts {
		if c == nil {
			continue
		}
		for _, a := range c.Artists {
			if a == nil || seen[a.ID] {
				continue
			}
			seen[a.ID] = true
			out = append(out, ArtistToProto(a))
		}
	}
	return out
}

// ConcertsOfGroups flattens proximity groups into their concerts, in group
// order and, within a group, home, nearby, then away.
func ConcertsOfGroups(groups []*entity.ProximityGroup) []*entity.Concert {
	var out []*entity.Concert
	for _, g := range groups {
		out = append(out, g.Home...)
		out = append(out, g.Nearby...)
		out = append(out, g.Away...)
	}
	return out
}

// ProximityGroupsToProto converts a slice of domain ProximityGroup entities to protobuf.
func ProximityGroupsToProto(groups []*entity.ProximityGroup) []*concertv1.ProximityGroup {
	result := make([]*concertv1.ProximityGroup, 0, len(groups))
	for _, g := range groups {
		result = append(result, &concertv1.ProximityGroup{
			Date: &entityv1.LocalDate{
				Value: TimeToDate(g.Date),
			},
			Home:   ConcertsToProto(g.Home),
			Nearby: ConcertsToProto(g.Nearby),
			Away:   ConcertsToProto(g.Away),
		})
	}
	return result
}

// VenueToProto converts domain Venue entity to protobuf.
func VenueToProto(v *entity.Venue) *entityv1.Venue {
	if v == nil {
		return nil
	}

	result := &entityv1.Venue{
		Id: &entityv1.VenueId{
			Value: v.ID,
		},
		Name: &entityv1.VenueName{
			Value: v.Name,
		},
	}
	if v.AdminArea != nil {
		result.AdminArea = &entityv1.AdminArea{Value: *v.AdminArea}
	}
	return result
}

// TimeToDate converts time.Time to a google.type.Date proto.
func TimeToDate(t time.Time) *date.Date {
	return &date.Date{
		Year:  int32(t.Year()),
		Month: int32(t.Month()),
		Day:   int32(t.Day()),
	}
}

// DateToTime converts a google.type.Date proto to a time.Time at midnight UTC.
// A nil input yields the zero time. The date is a calendar date (venue-local, per
// LocalDate semantics); UTC midnight is a canonical, timezone-neutral anchor for
// the SQL date-range comparison, which compares against the DATE column
// local_event_date.
func DateToTime(d *date.Date) time.Time {
	if d == nil {
		return time.Time{}
	}
	return time.Date(int(d.GetYear()), time.Month(d.GetMonth()), int(d.GetDay()), 0, 0, 0, 0, time.UTC)
}

// DateToTimePtr converts an optional google.type.Date proto to a *time.Time at
// midnight UTC, preserving the absent case as nil. Unlike DateToTime (which maps
// nil to the zero time), this distinguishes "no date provided" from a real date,
// so callers can bind SQL NULL and let a COALESCE default (e.g. CURRENT_DATE)
// take effect for the today-onward ListByFollower behavior.
func DateToTimePtr(d *date.Date) *time.Time {
	if d == nil {
		return nil
	}
	t := DateToTime(d)
	return &t
}

// ProtoGeoLocationToEntity converts a liverty_music.entity.v1.GeoLocation proto to
// the domain GeoLocation. Returns nil when the input is nil.
func ProtoGeoLocationToEntity(pb *entityv1.GeoLocation) *entity.GeoLocation {
	if pb == nil {
		return nil
	}
	return &entity.GeoLocation{
		Latitude:  pb.GetLatitude(),
		Longitude: pb.GetLongitude(),
		AdminArea: pb.GetAdminArea(),
	}
}
