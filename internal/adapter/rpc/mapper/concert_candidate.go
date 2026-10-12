package mapper

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	concertcandidatev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/concert_candidate/v1"
	"github.com/liverty-music/backend/internal/entity"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ConcertCandidateToProto converts a domain ConcertCandidate and its resolved Artist
// performer into the concertcandidatev1.PendingConcert wire message shown to reviewers.
//
// ResolvedVenue is omitted when both ResolvedPlaceID and ResolvedVenueName are
// nil (venue was not resolved by Google Places). SourceUrl is omitted when the
// concert candidate carries no source URL. StartTime is omitted when the source did
// not state a start time.
func ConcertCandidateToProto(sc *entity.ConcertCandidate, performer *entity.Artist) *concertcandidatev1.ConcertCandidate {
	if sc == nil {
		return nil
	}

	proto := &concertcandidatev1.ConcertCandidate{
		Id:              &entityv1.ConcertCandidateId{Value: sc.ID},
		Performer:       ArtistToProto(performer),
		Title:           &entityv1.Title{Value: sc.Title},
		LocalDate:       &entityv1.LocalDate{Value: TimeToDate(sc.LocalDate)},
		ListedVenueName: &entityv1.ListedVenueName{Value: sc.ListedVenueName},
		DiscoveredTime:  timestamppb.New(sc.DiscoveredTime),
	}

	if sc.StartTime != nil {
		proto.StartTime = &entityv1.StartTime{Value: timestamppb.New(*sc.StartTime)}
	}

	if sc.OpenTime != nil {
		proto.OpenTime = &entityv1.OpenTime{Value: timestamppb.New(*sc.OpenTime)}
	}

	if sc.ResolvedPlaceID != nil || sc.ResolvedVenueName != nil {
		proto.ResolvedVenue = resolvedVenueToProto(sc)
	}

	if sc.SourceURL != nil && *sc.SourceURL != "" {
		proto.SourceUrl = &entityv1.Url{Value: *sc.SourceURL}
	}

	return proto
}

// resolvedVenueToProto builds the ResolvedVenue preview DTO from the resolved
// fields denormalised on the concert candidate row. Called only when at least one
// resolved field is non-nil.
func resolvedVenueToProto(sc *entity.ConcertCandidate) *concertcandidatev1.ResolvedVenue {
	rv := &concertcandidatev1.ResolvedVenue{}

	if sc.ResolvedVenueName != nil {
		rv.Name = &entityv1.VenueName{Value: *sc.ResolvedVenueName}
	}
	if sc.ResolvedAdminArea != nil {
		rv.AdminArea = &entityv1.AdminArea{Value: *sc.ResolvedAdminArea}
	}
	if sc.ResolvedPlaceID != nil {
		rv.PlaceId = &entityv1.PlaceId{Value: *sc.ResolvedPlaceID}
	}
	if sc.ResolvedLatitude != nil && sc.ResolvedLongitude != nil {
		rv.Coordinates = &entityv1.Coordinates{
			Latitude:  *sc.ResolvedLatitude,
			Longitude: *sc.ResolvedLongitude,
		}
	}

	return rv
}
