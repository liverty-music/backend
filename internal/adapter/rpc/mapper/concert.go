// Package mapper provides conversion functions between domain entities and Protobuf messages.
package mapper

import (
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/entity"
	"google.golang.org/genproto/googleapis/type/date"
	"google.golang.org/genproto/googleapis/type/timeofday"
)

// ConcertToProto converts domain Concert entity to protobuf.
func ConcertToProto(c *entity.Concert) *entityv1.Concert {
	if c == nil {
		return nil
	}

	return &entityv1.Concert{
		Id: &entityv1.ConcertId{
			Value: c.ID,
		},
		ArtistId: &entityv1.ArtistId{
			Value: c.ArtistID,
		},
		VenueId: &entityv1.VenueId{
			Value: c.VenueID,
		},
		Date: &date.Date{
			Year:  int32(c.LocalEventDate.Year()),
			Month: int32(c.LocalEventDate.Month()),
			Day:   int32(c.LocalEventDate.Day()),
		},
		StartTime: TimeToTimeOfDayProto(c.StartTime),
		OpenTime:  TimeToTimeOfDayProto(c.OpenTime),
		Title: &entityv1.ConcertTitle{
			Value: c.Title,
		},
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

// VenueToProto converts domain Venue entity to protobuf.
func VenueToProto(v *entity.Venue) *entityv1.Venue {
	if v == nil {
		return nil
	}

	return &entityv1.Venue{
		Id: &entityv1.VenueId{
			Value: v.ID,
		},
		Name: &entityv1.VenueName{
			Value: v.Name,
		},
	}
}

// TimeToTimeOfDayProto converts *time.Time to *timeofday.TimeOfDay.
func TimeToTimeOfDayProto(t *time.Time) *timeofday.TimeOfDay {
	if t == nil {
		return nil
	}
	return &timeofday.TimeOfDay{
		Hours:   int32(t.Hour()),
		Minutes: int32(t.Minute()),
		Seconds: int32(t.Second()),
	}
}
