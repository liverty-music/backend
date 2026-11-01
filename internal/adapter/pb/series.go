package pb

import (
	"strings"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	organizervenuev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/venue/v1"
	"github.com/liverty-music/backend/internal/entity"
	gcsstorage "github.com/liverty-music/backend/internal/infrastructure/gcp/storage"
)

// SeriesImageURLBuilder composes public CDN URLs for organizer series image
// variants (thumb/large). The CDN base is sourced from the
// CDN_BASE_URL environment variable via config
// (config.ServerConfig.CDNBaseURL) and injected here at DI wiring
// time (see internal/di/provider.go), so this adapter — not the entity
// package — owns the environment read.
type SeriesImageURLBuilder struct {
	cdnBase string
}

// NewSeriesImageURLBuilder creates a SeriesImageURLBuilder for the given CDN base URL.
// Trailing slashes are trimmed so composed URLs never contain a double slash.
func NewSeriesImageURLBuilder(cdnBase string) *SeriesImageURLBuilder {
	return &SeriesImageURLBuilder{cdnBase: strings.TrimRight(cdnBase, "/")}
}

// VariantURL composes the public CDN URL for one variant of a series image
// image: {cdnBase}/{VariantObjectKey}. Returns "" when no CDN base is
// configured, so callers never emit a malformed relative URL.
//
// variant must be one of "thumb" or "large".
func (b *SeriesImageURLBuilder) VariantURL(organizerID, seriesImageID, variant string) string {
	if b == nil || b.cdnBase == "" {
		return ""
	}
	return b.cdnBase + "/" + gcsstorage.VariantObjectKey(organizerID, seriesImageID, variant)
}

// FromSeriesImage maps a series image to Protobuf with its id and Organizer,
// without its served addresses: the processing request carries it before the
// variants exist.
func FromSeriesImage(img *entity.SeriesImage) *entityv1.SeriesImage {
	if img == nil {
		return nil
	}
	return &entityv1.SeriesImage{
		Id:          &entityv1.SeriesImageId{Value: img.ID},
		OrganizerId: &entityv1.OrganizerId{Value: img.OrganizerID},
	}
}

// seriesImageProto builds the entityv1.SeriesImage message for a series' cover image.
// Returns nil when the series has no cover image or when the CDN base is
// unset, so callers never emit a proto with empty URL fields.
func (b *SeriesImageURLBuilder) seriesImageProto(s *entity.Series) *entityv1.SeriesImage {
	if s.CoverImage == nil {
		return nil
	}
	thumbURL := b.VariantURL(s.CoverImage.OrganizerID, s.CoverImage.ID, "thumb")
	largeURL := b.VariantURL(s.CoverImage.OrganizerID, s.CoverImage.ID, "large")
	if thumbURL == "" || largeURL == "" {
		// CDN base not configured — omit rather than emit broken URLs.
		return nil
	}
	return &entityv1.SeriesImage{
		Id:          &entityv1.SeriesImageId{Value: s.CoverImage.ID},
		Thumb:       &entityv1.Url{Value: thumbURL},
		Large:       &entityv1.Url{Value: largeURL},
		OrganizerId: &entityv1.OrganizerId{Value: s.CoverImage.OrganizerID},
	}
}

// FromSeries maps a domain Series like [FromSeries] and additionally
// sets its cover image with CDN URLs when the Series has one.
func (b *SeriesImageURLBuilder) FromSeries(s *entity.Series) *entityv1.Series {
	proto := FromSeries(s)
	if proto == nil {
		return nil
	}
	if m := b.seriesImageProto(s); m != nil {
		proto.CoverImage = m
	}
	return proto
}

// ReferencedSeries returns every Series the concerts refer to, once each, in
// the order the concerts first refer to them, each with its cover image.
func (b *SeriesImageURLBuilder) ReferencedSeries(concerts []*entity.Concert) []*entityv1.Series {
	seen := make(map[string]bool)
	out := make([]*entityv1.Series, 0)
	for _, c := range concerts {
		if c == nil || c.Series == nil || seen[c.Series.ID] {
			continue
		}
		seen[c.Series.ID] = true
		out = append(out, b.FromSeries(c.Series))
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

// publishStateToProto maps a domain PublishState to the proto PublishState enum.
func publishStateToProto(ps entity.PublishState) entityv1.PublishState {
	switch ps {
	case entity.PublishStateDraft:
		return entityv1.PublishState_PUBLISH_STATE_DRAFT
	case entity.PublishStatePublished:
		return entityv1.PublishState_PUBLISH_STATE_PUBLISHED
	case entity.PublishStateCancelled:
		return entityv1.PublishState_PUBLISH_STATE_CANCELLED
	default:
		return entityv1.PublishState_PUBLISH_STATE_UNSPECIFIED
	}
}

// ToSeriesType maps the proto SeriesType enum to the domain SeriesType.
func ToSeriesType(t entityv1.SeriesType) entity.SeriesType {
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

// ToVisibility maps the proto Visibility enum to domain SeriesVisibility.
func ToVisibility(v entityv1.Visibility) entity.SeriesVisibility {
	switch v {
	case entityv1.Visibility_VISIBILITY_UNLISTED:
		return entity.SeriesVisibilityUnlisted
	default:
		return entity.SeriesVisibilityPublic
	}
}

// FromVenueCandidates maps the map catalog's candidate places for a venue
// search to the organizer VenueService.Search response candidates, keeping their order.
func FromVenueCandidates(candidates []*entity.MapPlaceCandidate) []*organizervenuev1.VenueCandidate {
	out := make([]*organizervenuev1.VenueCandidate, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, &organizervenuev1.VenueCandidate{
			PlaceId: &entityv1.PlaceId{Value: c.PlaceID},
			Name:    &entityv1.VenueName{Value: c.Name},
			Address: c.Address,
		})
	}
	return out
}
