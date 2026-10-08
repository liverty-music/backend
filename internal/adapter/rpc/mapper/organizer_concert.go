package mapper

import (
	"strings"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	organizerconcertv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/concert/v1"
	"github.com/liverty-music/backend/internal/entity"
	gcsstorage "github.com/liverty-music/backend/internal/infrastructure/gcp/storage"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// MediaURLBuilder composes public CDN URLs for organizer series media
// variants (thumb/large). The CDN base is sourced from the
// ORGANIZER_MEDIA_CDN_BASE environment variable via config
// (config.ServerConfig.OrganizerMediaCDNBase) and injected here at DI wiring
// time (see internal/di/provider.go), so this adapter — not the entity
// package — owns the environment read.
type MediaURLBuilder struct {
	cdnBase string
}

// NewMediaURLBuilder creates a MediaURLBuilder for the given CDN base URL.
// Trailing slashes are trimmed so composed URLs never contain a double slash.
func NewMediaURLBuilder(cdnBase string) *MediaURLBuilder {
	return &MediaURLBuilder{cdnBase: strings.TrimRight(cdnBase, "/")}
}

// VariantURL composes the public CDN URL for one variant of a series media
// image: {cdnBase}/{VariantObjectKey}. Returns "" when no CDN base is
// configured, so callers never emit a malformed relative URL.
//
// variant must be one of "thumb" or "large".
func (b *MediaURLBuilder) VariantURL(organizerID, mediaID, variant string) string {
	if b == nil || b.cdnBase == "" {
		return ""
	}
	return b.cdnBase + "/" + gcsstorage.VariantObjectKey(organizerID, mediaID, variant)
}

// seriesMediaProto builds the entityv1.Media message for a series' cover media.
// Returns nil when the series has no cover media or when the CDN base is
// unset, so callers never emit a proto with empty URL fields.
func (b *MediaURLBuilder) seriesMediaProto(s *entity.Series) *entityv1.Media {
	if s.CoverMedia == nil {
		return nil
	}
	thumbURL := b.VariantURL(s.CoverMedia.OrganizerID, s.CoverMedia.ID, "thumb")
	largeURL := b.VariantURL(s.CoverMedia.OrganizerID, s.CoverMedia.ID, "large")
	if thumbURL == "" || largeURL == "" {
		// CDN base not configured — omit rather than emit broken URLs.
		return nil
	}
	return &entityv1.Media{
		Id:   &entityv1.MediaId{Value: s.CoverMedia.ID},
		Kind: entityv1.MediaKind_MEDIA_KIND_IMAGE,
		Attributes: &entityv1.MediaAttributes{
			Thumb: &entityv1.Url{Value: thumbURL},
			Large: &entityv1.Url{Value: largeURL},
		},
	}
}

// AuthoredConcertToProto converts the three-part authored concert tuple
// (series, events, artists) into the wire-format AuthoredConcert message.
func (b *MediaURLBuilder) AuthoredConcertToProto(s *entity.Series, events []*entity.Event, artists []*entity.Artist) *organizerconcertv1.AuthoredConcert {
	return &organizerconcertv1.AuthoredConcert{
		Series:     b.AuthoredSeriesToProto(s),
		Events:     AuthoredEventsToProto(events),
		Performers: ArtistsToProto(artists),
	}
}

// AuthoredSeriesToProto maps a domain Series (including authoring fields) to
// the entityv1.Series proto message.
func (b *MediaURLBuilder) AuthoredSeriesToProto(s *entity.Series) *entityv1.Series {
	return b.SeriesToProto(s)
}

// SeriesToProto maps a domain Series like [SeriesToProto] and additionally
// sets its cover media with CDN URLs when the Series has one.
func (b *MediaURLBuilder) SeriesToProto(s *entity.Series) *entityv1.Series {
	proto := SeriesToProto(s)
	if proto == nil {
		return nil
	}
	if m := b.seriesMediaProto(s); m != nil {
		proto.Media = m
	}
	return proto
}

// ConcertToProto maps a domain Concert like [ConcertToProto] and additionally
// sets its Series' cover media with CDN URLs when the Series has one.
func (b *MediaURLBuilder) ConcertToProto(c *entity.Concert) *entityv1.Concert {
	proto := ConcertToProto(c)
	if proto == nil || c.Series == nil {
		return proto
	}
	proto.Series = b.SeriesToProto(c.Series)
	return proto
}

// ConcertsToProto maps a slice of Concerts with [MediaURLBuilder.ConcertToProto].
func (b *MediaURLBuilder) ConcertsToProto(concerts []*entity.Concert) []*entityv1.Concert {
	out := make([]*entityv1.Concert, 0, len(concerts))
	for _, c := range concerts {
		if p := b.ConcertToProto(c); p != nil {
			out = append(out, p)
		}
	}
	return out
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
