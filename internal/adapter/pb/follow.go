package pb

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/entity"
)

// hypeToProto maps a domain Hype value to its Protobuf HypeType enum value.
var hypeToProto = map[entity.Hype]entityv1.HypeType{
	entity.HypeWatch:  entityv1.HypeType_HYPE_TYPE_WATCH,
	entity.HypeHome:   entityv1.HypeType_HYPE_TYPE_HOME,
	entity.HypeNearby: entityv1.HypeType_HYPE_TYPE_NEARBY,
	entity.HypeAway:   entityv1.HypeType_HYPE_TYPE_AWAY,
}

// HypeFromProto maps a Protobuf HypeType enum to its domain Hype value.
var HypeFromProto = map[entityv1.HypeType]entity.Hype{
	entityv1.HypeType_HYPE_TYPE_WATCH:  entity.HypeWatch,
	entityv1.HypeType_HYPE_TYPE_HOME:   entity.HypeHome,
	entityv1.HypeType_HYPE_TYPE_NEARBY: entity.HypeNearby,
	entityv1.HypeType_HYPE_TYPE_AWAY:   entity.HypeAway,
}

// FromFollowedArtist maps a domain FollowedArtist to its Protobuf wire representation.
func FromFollowedArtist(fa *entity.FollowedArtist) *entityv1.FollowedArtist {
	if fa == nil {
		return nil
	}
	return &entityv1.FollowedArtist{
		Artist: FromArtist(fa.Artist),
		Hype:   hypeToProto[fa.Hype],
	}
}

// FromFollowedArtists maps a collection of domain FollowedArtist entities to Protobuf messages.
func FromFollowedArtists(followed []*entity.FollowedArtist) []*entityv1.FollowedArtist {
	result := make([]*entityv1.FollowedArtist, 0, len(followed))
	for _, fa := range followed {
		result = append(result, FromFollowedArtist(fa))
	}
	return result
}
