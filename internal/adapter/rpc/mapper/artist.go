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

// passionLevelToProto maps a domain PassionLevel to its Protobuf enum value.
var passionLevelToProto = map[entity.PassionLevel]entityv1.PassionLevel{
	entity.PassionLevelMustGo:    entityv1.PassionLevel_PASSION_LEVEL_MUST_GO,
	entity.PassionLevelLocalOnly: entityv1.PassionLevel_PASSION_LEVEL_LOCAL_ONLY,
	entity.PassionLevelKeepAnEye: entityv1.PassionLevel_PASSION_LEVEL_KEEP_AN_EYE,
}

// PassionLevelFromProto maps a Protobuf PassionLevel enum to its domain value.
var PassionLevelFromProto = map[entityv1.PassionLevel]entity.PassionLevel{
	entityv1.PassionLevel_PASSION_LEVEL_MUST_GO:     entity.PassionLevelMustGo,
	entityv1.PassionLevel_PASSION_LEVEL_LOCAL_ONLY:  entity.PassionLevelLocalOnly,
	entityv1.PassionLevel_PASSION_LEVEL_KEEP_AN_EYE: entity.PassionLevelKeepAnEye,
}

// FollowedArtistToProto maps a domain FollowedArtist to its Protobuf wire representation.
func FollowedArtistToProto(fa *entity.FollowedArtist) *rpcv1.FollowedArtist {
	if fa == nil {
		return nil
	}
	return &rpcv1.FollowedArtist{
		Artist:       ArtistToProto(fa.Artist),
		PassionLevel: passionLevelToProto[fa.PassionLevel],
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
