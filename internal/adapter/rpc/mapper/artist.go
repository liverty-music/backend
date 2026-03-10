package mapper

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	rpcv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/artist/v1"
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

// hypeToProto maps a domain Hype value to its Protobuf HypeType enum value.
var hypeToProto = map[entity.Hype]entityv1.HypeType{
	entity.HypeWatch:    entityv1.HypeType_HYPE_TYPE_WATCH,
	entity.HypeHome:     entityv1.HypeType_HYPE_TYPE_HOME,
	entity.HypeNearby:   entityv1.HypeType_HYPE_TYPE_NEARBY,
	entity.HypeAnywhere: entityv1.HypeType_HYPE_TYPE_ANYWHERE,
}

// HypeFromProto maps a Protobuf HypeType enum to its domain Hype value.
var HypeFromProto = map[entityv1.HypeType]entity.Hype{
	entityv1.HypeType_HYPE_TYPE_WATCH:    entity.HypeWatch,
	entityv1.HypeType_HYPE_TYPE_HOME:     entity.HypeHome,
	entityv1.HypeType_HYPE_TYPE_NEARBY:   entity.HypeNearby,
	entityv1.HypeType_HYPE_TYPE_ANYWHERE: entity.HypeAnywhere,
}

// FollowedArtistToProto maps a domain FollowedArtist to its Protobuf wire representation.
func FollowedArtistToProto(fa *entity.FollowedArtist) *rpcv1.FollowedArtist {
	if fa == nil {
		return nil
	}
	return &rpcv1.FollowedArtist{
		Artist: ArtistToProto(fa.Artist),
		Hype:   hypeToProto[fa.Hype],
	}
}

// FollowedArtistsToProto maps a collection of domain FollowedArtist entities to Protobuf messages.
func FollowedArtistsToProto(followed []*entity.FollowedArtist) []*rpcv1.FollowedArtist {
	result := make([]*rpcv1.FollowedArtist, 0, len(followed))
	for _, fa := range followed {
		result = append(result, FollowedArtistToProto(fa))
	}
	return result
}
