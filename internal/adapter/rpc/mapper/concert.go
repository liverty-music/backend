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

// ConcertToProto converts domain Concert entity to protobuf.
//
// Transitional bridge: the BSR-published Concert proto still exposes the
// pre-Series fields (Title, SourceUrl, ArtistId). We fill them from the new
// entity locations — Series.Title, Series.SourceURL, and the first performer
// in Performers — so existing clients keep working until the new proto
// (Series-embedded + repeated performers) is published and consumed. When the
// new proto lands this mapper will be rewritten to populate the new fields
// directly (see openspec/changes/add-series-hierarchy task 9.2).
func ConcertToProto(c *entity.Concert) *entityv1.Concert {
	if c == nil {
		return nil
	}

	proto := &entityv1.Concert{
		Id: &entityv1.EventId{
			Value: c.ID,
		},
		VenueId: &entityv1.VenueId{
			Value: c.VenueID,
		},
		LocalDate: &entityv1.LocalDate{
			Value: TimeToDate(c.LocalDate),
		},
		Venue: VenueToProto(c.Venue),
	}

	if c.Series != nil {
		proto.Title = &entityv1.Title{Value: c.Series.Title}
		if c.Series.SourceURL != "" {
			proto.SourceUrl = &entityv1.Url{Value: c.Series.SourceURL}
		}
	}
	if len(c.Performers) > 0 && c.Performers[0] != nil {
		// Single-artist projection for the legacy proto field. Multi-performer
		// concerts (festivals, co-headliners) lose the supporting artists in
		// this response shape; clients that need the full lineup should wait
		// for the new proto with repeated performers.
		proto.ArtistId = &entityv1.ArtistId{Value: c.Performers[0].ID}
	}
	if c.StartTime != nil {
		proto.StartTime = &entityv1.StartTime{
			Value: timestamppb.New(*c.StartTime),
		}
	}
	if c.OpenTime != nil {
		proto.OpenTime = &entityv1.OpenTime{
			Value: timestamppb.New(*c.OpenTime),
		}
	}
	if c.ListedVenueName != nil {
		proto.ListedVenueName = &entityv1.ListedVenueName{
			Value: *c.ListedVenueName,
		}
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
