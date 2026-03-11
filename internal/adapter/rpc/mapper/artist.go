package mapper

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/entity"
)

// ArtistToProto maps a domain Artist entity to its Protobuf wire representation.
func ArtistToProto(a *entity.Artist) *entityv1.Artist {
	if a == nil {
		return nil
	}

	return &entityv1.Artist{
		Id: &entityv1.ArtistId{
			Value: a.ID,
		},
		Name: &entityv1.ArtistName{
			Value: a.Name,
		},
		Mbid: &entityv1.Mbid{
			Value: a.MBID,
		},
	}
}

// ArtistsToProto maps a collection of domain Artist entities to a collection of Protobuf messages.
func ArtistsToProto(artists []*entity.Artist) []*entityv1.Artist {
	protoArtists := make([]*entityv1.Artist, 0, len(artists))
	for _, a := range artists {
		protoArtists = append(protoArtists, ArtistToProto(a))
	}
	return protoArtists
}
