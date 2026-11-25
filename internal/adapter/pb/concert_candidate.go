package pb

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	concertcandidatev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/concert_candidate/v1"
	"github.com/liverty-music/backend/internal/entity"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// FromConcertCandidate converts a domain ConcertCandidate and its resolved Artist
// performer into the concertcandidatev1.PendingConcert wire message shown to reviewers.
//
// The admin area (as discovery resolved it), the time zone and the resolved
// place id are each omitted when the candidate has none; no name or position
// read from the map catalog is returned. SourceUrl is omitted when the
// concert candidate carries no source URL. StartTime is omitted when the
// source did not state a start time.
func FromConcertCandidate(sc *entity.ConcertCandidate, performer *entity.Artist) *concertcandidatev1.ConcertCandidate {
	if sc == nil {
		return nil
	}

	proto := &concertcandidatev1.ConcertCandidate{
		Id:              &entityv1.ConcertCandidateId{Value: sc.ID},
		Performer:       FromArtist(performer),
		Title:           &entityv1.Title{Value: sc.Title},
		LocalDate:       &entityv1.LocalDate{Value: TimeToDate(sc.LocalDate)},
		ListedVenueName: &entityv1.ListedVenueName{Value: sc.ListedVenueName},
		FindTime:        timestamppb.New(sc.FindTime),
	}

	if sc.StartTime != nil {
		proto.StartTime = &entityv1.StartTime{Value: timestamppb.New(*sc.StartTime)}
	}

	if sc.OpenTime != nil {
		proto.OpenTime = &entityv1.OpenTime{Value: timestamppb.New(*sc.OpenTime)}
	}

	if sc.AdminArea != nil {
		proto.AdminArea = &entityv1.AdminArea{Value: *sc.AdminArea}
	}
	if sc.TimeZone != nil {
		proto.TimeZone = &entityv1.TimeZone{Value: *sc.TimeZone}
	}
	if sc.ResolvedPlaceID != nil {
		proto.ResolvedPlaceId = &entityv1.PlaceId{Value: *sc.ResolvedPlaceID}
	}

	if sc.SourceURL != nil && *sc.SourceURL != "" {
		proto.SourceUrl = &entityv1.Url{Value: *sc.SourceURL}
	}

	return proto
}
