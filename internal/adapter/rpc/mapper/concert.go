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
	return proto
}

// ConcertToProto converts a domain Concert entity to protobuf.
//
// The Concert proto now embeds the full Series parent and exposes the
// performing artists via the repeated `performers` field — see the new
// schema published in liverty-music/specification v0.41.0. Series and
// Performers MUST be populated by the repository before this mapper runs;
// ConcertRepository.ListByIDs and friends do this via hydratePerformers.
func ConcertToProto(c *entity.Concert) *entityv1.Concert {
	if c == nil {
		return nil
	}

	performers := make([]*entityv1.Artist, 0, len(c.Performers))
	for _, a := range c.Performers {
		if a == nil {
			continue
		}
		performers = append(performers, ArtistToProto(a))
	}

	proto := &entityv1.Concert{
		Id:         &entityv1.EventId{Value: c.ID},
		VenueId:    &entityv1.VenueId{Value: c.VenueID},
		Venue:      VenueToProto(c.Venue),
		LocalDate:  &entityv1.LocalDate{Value: TimeToDate(c.LocalDate)},
		Series:     SeriesToProto(c.Series),
		Performers: performers,
	}
	if c.StartTime != nil {
		proto.StartTime = &entityv1.StartTime{Value: timestamppb.New(*c.StartTime)}
	}
	if c.OpenTime != nil {
		proto.OpenTime = &entityv1.OpenTime{Value: timestamppb.New(*c.OpenTime)}
	}
	if c.ListedVenueName != nil {
		proto.ListedVenueName = &entityv1.ListedVenueName{Value: *c.ListedVenueName}
	}

	return proto
}

// ConcertsToProto converts a slice of domain Concert entities to protobuf.
func ConcertsToProto(concerts []*entity.Concert) []*entityv1.Concert {
	protoConcerts := make([]*entityv1.Concert, 0, len(concerts))
	for _, c := range concerts {
		protoConcerts = append(protoConcerts, ConcertToProto(c))
	}
	return protoConcerts
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
