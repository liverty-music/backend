package mapper

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	organizerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/v1"
	"github.com/liverty-music/backend/internal/entity"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// AuthoredConcertToProto converts the three-part authored concert tuple
// (series, events, artists) into the wire-format AuthoredConcert message.
func AuthoredConcertToProto(s *entity.Series, events []*entity.Event, artists []*entity.Artist) *organizerv1.AuthoredConcert {
	return &organizerv1.AuthoredConcert{
		Series:     AuthoredSeriesToProto(s),
		Events:     AuthoredEventsToProto(events),
		Performers: ArtistsToProto(artists),
	}
}

// AuthoredSeriesToProto maps a domain Series (including authoring fields) to
// the entityv1.Series proto message.
func AuthoredSeriesToProto(s *entity.Series) *entityv1.Series {
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
	if s.CoverImageURL != nil {
		proto.CoverImage = &entityv1.Url{Value: *s.CoverImageURL}
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
	return proto
}

// AuthoredEventsToProto converts a slice of domain Event entities into the
// entityv1.Event proto messages carried by AuthoredConcert.Events.
func AuthoredEventsToProto(events []*entity.Event) []*entityv1.Event {
	out := make([]*entityv1.Event, 0, len(events))
	for _, ev := range events {
		if ev == nil {
			continue
		}
		proto := &entityv1.Event{
			Id:        &entityv1.EventId{Value: ev.ID},
			LocalDate: &entityv1.LocalDate{Value: TimeToDate(ev.LocalDate)},
		}
		if ev.SeriesID != "" {
			proto.SeriesId = &entityv1.SeriesId{Value: ev.SeriesID}
		}
		if ev.StartTime != nil {
			proto.StartTime = &entityv1.StartTime{Value: timestamppb.New(*ev.StartTime)}
		}
		if ev.OpenTime != nil {
			proto.OpenTime = &entityv1.OpenTime{Value: timestamppb.New(*ev.OpenTime)}
		}
		out = append(out, proto)
	}
	return out
}

// visibilityToProto maps a domain SeriesVisibility to the proto Visibility enum.
func visibilityToProto(v entity.SeriesVisibility) entityv1.Visibility {
	switch v {
	case entity.SeriesVisibilityPublic:
		return entityv1.Visibility_VISIBILITY_PUBLIC
	case entity.SeriesVisibilityUnlisted:
		return entityv1.Visibility_VISIBILITY_UNLISTED
	default:
		return entityv1.Visibility_VISIBILITY_UNSPECIFIED
	}
}

// publishStateToProto maps a domain SeriesPublishState to the proto PublishState enum.
func publishStateToProto(ps entity.SeriesPublishState) entityv1.PublishState {
	switch ps {
	case entity.SeriesPublishStateDraft:
		return entityv1.PublishState_PUBLISH_STATE_DRAFT
	case entity.SeriesPublishStatePublished:
		return entityv1.PublishState_PUBLISH_STATE_PUBLISHED
	case entity.SeriesPublishStateCancelled:
		return entityv1.PublishState_PUBLISH_STATE_CANCELLED
	default:
		return entityv1.PublishState_PUBLISH_STATE_UNSPECIFIED
	}
}

// SeriesTypeFromProto maps the proto SeriesType enum to the domain SeriesType.
func SeriesTypeFromProto(t entityv1.SeriesType) entity.SeriesType {
	switch t {
	case entityv1.SeriesType_SERIES_TYPE_TOUR:
		return entity.SeriesTypeTour
	case entityv1.SeriesType_SERIES_TYPE_SINGLE:
		return entity.SeriesTypeSingle
	case entityv1.SeriesType_SERIES_TYPE_FESTIVAL:
		return entity.SeriesTypeFestival
	default:
		return entity.SeriesTypeSingle
	}
}

// VisibilityFromProto maps the proto Visibility enum to domain SeriesVisibility.
func VisibilityFromProto(v entityv1.Visibility) entity.SeriesVisibility {
	switch v {
	case entityv1.Visibility_VISIBILITY_UNLISTED:
		return entity.SeriesVisibilityUnlisted
	default:
		return entity.SeriesVisibilityPublic
	}
}
