// Package mapper provides conversion functions between domain entities and Protobuf messages.
package mapper

import (
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/entity"
	"google.golang.org/genproto/googleapis/type/date"
	"google.golang.org/genproto/googleapis/type/timeofday"
)

// ArtistToProto converts domain Artist entity to protobuf.
func ArtistToProto(a *entity.Artist) *entityv1.Artist {
	if a == nil {
		return nil
	}

	var protoMedia []*entityv1.Media
	for _, m := range a.Media {
		protoMedia = append(protoMedia, MediaToProto(m))
	}

	return &entityv1.Artist{
		Id: &entityv1.ArtistId{
			Value: a.ID,
		},
		Name: &entityv1.ArtistName{
			Value: a.Name,
		},
		Media: protoMedia,
	}
}

// MediaToProto converts domain Media entity to protobuf.
func MediaToProto(m *entity.Media) *entityv1.Media {
	if m == nil {
		return nil
	}

	return &entityv1.Media{
		Id: &entityv1.MediaId{
			Value: m.ID,
		},
		Type: MapMediaTypeToProto(m.Type),
		Url: &entityv1.MediaUrl{
			Value: m.URL,
		},
	}
}

// MapMediaTypeToProto maps domain MediaType to proto Type.
func MapMediaTypeToProto(t entity.MediaType) entityv1.Media_Type {
	switch t {
	case entity.MediaTypeWeb:
		return entityv1.Media_TYPE_WEB
	case entity.MediaTypeTwitter:
		return entityv1.Media_TYPE_TWITTER
	case entity.MediaTypeInstagram:
		return entityv1.Media_TYPE_INSTAGRAM
	default:
		return entityv1.Media_TYPE_UNSPECIFIED
	}
}

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
			Year:  int32(c.Date.Year()),
			Month: int32(c.Date.Month()),
			Day:   int32(c.Date.Day()),
		},
		StartTime: &timeofday.TimeOfDay{
			Hours:   int32(c.StartTime.Hour()),
			Minutes: int32(c.StartTime.Minute()),
			Seconds: int32(c.StartTime.Second()),
		},
		OpenTime: TimeToTimeOfDayProto(c.OpenTime),
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

// MapProtoTypeToEntity maps proto Type to domain MediaType.
func MapProtoTypeToEntity(t entityv1.Media_Type) entity.MediaType {
	switch t {
	case entityv1.Media_TYPE_WEB:
		return entity.MediaTypeWeb
	case entityv1.Media_TYPE_TWITTER:
		return entity.MediaTypeTwitter
	case entityv1.Media_TYPE_INSTAGRAM:
		return entity.MediaTypeInstagram
	default:
		return entity.MediaTypeWeb
	}
}
