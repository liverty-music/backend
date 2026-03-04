package mapper

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	concertv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/concert/v1"
	"github.com/liverty-music/backend/internal/usecase"
)

// searchStatusProtoMap maps usecase search status values to proto enum values.
var searchStatusProtoMap = map[usecase.SearchStatusValue]concertv1.SearchStatus{
	usecase.SearchStatusUnspecified: concertv1.SearchStatus_SEARCH_STATUS_UNSPECIFIED,
	usecase.SearchStatusPending:     concertv1.SearchStatus_SEARCH_STATUS_PENDING,
	usecase.SearchStatusCompleted:   concertv1.SearchStatus_SEARCH_STATUS_COMPLETED,
	usecase.SearchStatusFailed:      concertv1.SearchStatus_SEARCH_STATUS_FAILED,
}

// SearchStatusesToProto converts usecase search statuses to proto ArtistSearchStatus messages.
func SearchStatusesToProto(statuses []*usecase.SearchStatus) []*concertv1.ArtistSearchStatus {
	result := make([]*concertv1.ArtistSearchStatus, 0, len(statuses))
	for _, s := range statuses {
		result = append(result, &concertv1.ArtistSearchStatus{
			ArtistId: &entityv1.ArtistId{Value: s.ArtistID},
			Status:   searchStatusProtoMap[s.Status],
		})
	}
	return result
}
