// Package mapper provides conversion functions between domain entities and Protobuf messages.
package mapper

import (
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/entity"
	"google.golang.org/genproto/googleapis/type/date"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ConcertToProto converts domain Concert entity to protobuf.
func ConcertToProto(c *entity.Concert) *entityv1.Concert {
	if c == nil {
		return nil
	}

	proto := &entityv1.Concert{
		Id: &entityv1.ConcertId{
			Value: c.ID,
		},
		ArtistId: &entityv1.ArtistId{
			Value: c.ArtistID,
		},
		VenueId: &entityv1.VenueId{
			Value: c.VenueID,
		},
		LocalDate: &entityv1.LocalDate{
			Value: TimeToDate(c.LocalDate),
		},
		Title: &entityv1.Title{
			Value: c.Title,
		},
		Venue: VenueToProto(c.Venue),
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
	if c.SourceURL != "" {
		proto.SourceUrl = &entityv1.SourceUrl{
			Value: c.SourceURL,
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
