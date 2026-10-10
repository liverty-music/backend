package mapper

import (
	"strings"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/entity"
	gcsstorage "github.com/liverty-music/backend/internal/infrastructure/gcp/storage"
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

// ReferencedSeries returns every Series the concerts refer to, once each, in
// the order the concerts first refer to them, each with its cover media.
func (b *MediaURLBuilder) ReferencedSeries(concerts []*entity.Concert) []*entityv1.Series {
	seen := make(map[string]bool)
	out := make([]*entityv1.Series, 0)
	for _, c := range concerts {
		if c == nil || c.Series == nil || seen[c.Series.ID] {
			continue
		}
		seen[c.Series.ID] = true
		out = append(out, b.SeriesToProto(c.Series))
	}
	return out
}

// AuthoredConcerts builds the Concerts of an authored Series: one per date,
// each performed by the Series' artists. A DRAFT Series' dates are its
// DraftEvents and its artists the draft performers.
func AuthoredConcerts(s *entity.Series, events []*entity.Event, artists []*entity.Artist) []*entity.Concert {
	out := make([]*entity.Concert, 0, len(events))
	for _, ev := range events {
		if ev == nil {
			continue
		}
		out = append(out, &entity.Concert{Event: *ev, Series: s, Artists: artists})
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
