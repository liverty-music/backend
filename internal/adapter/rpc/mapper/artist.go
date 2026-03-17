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

	proto := &entityv1.Artist{
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

	if a.Fanart != nil {
		proto.Fanart = fanartToProto(a.Fanart)
	}

	return proto
}

// fanartToProto maps a domain Fanart entity to its Protobuf wire representation,
// selecting the best image (highest likes) for each image type.
func fanartToProto(f *entity.Fanart) *entityv1.Fanart {
	if f == nil {
		return nil
	}

	proto := &entityv1.Fanart{}

	if url := entity.BestByLikes(f.ArtistThumb); url != "" {
		proto.ArtistThumb = &entityv1.Url{Value: url}
	}
	if url := entity.BestByLikes(f.ArtistBackground); url != "" {
		proto.ArtistBackground = &entityv1.Url{Value: url}
	}
	if url := entity.BestByLikes(f.HDMusicLogo); url != "" {
		proto.HdMusicLogo = &entityv1.Url{Value: url}
	}
	if url := entity.BestByLikes(f.MusicLogo); url != "" {
		proto.MusicLogo = &entityv1.Url{Value: url}
	}
	if url := entity.BestByLikes(f.MusicBanner); url != "" {
		proto.MusicBanner = &entityv1.Url{Value: url}
	}

	if f.LogoColorProfile != nil {
		proto.LogoColorProfile = logoColorProfileToProto(f.LogoColorProfile)
	}

	return proto
}

// logoColorProfileToProto maps a domain LogoColorProfile to its Protobuf
// wire representation.
func logoColorProfileToProto(p *entity.LogoColorProfile) *entityv1.LogoColorProfile {
	if p == nil {
		return nil
	}

	pb := &entityv1.LogoColorProfile{
		DominantLightness: float32(p.DominantLightness),
		IsChromatic:       p.IsChromatic,
	}

	if p.DominantHue != nil {
		pb.SetDominantHue(float32(*p.DominantHue))
	}

	return pb
}

// ArtistsToProto maps a collection of domain Artist entities to a collection of Protobuf messages.
func ArtistsToProto(artists []*entity.Artist) []*entityv1.Artist {
	protoArtists := make([]*entityv1.Artist, 0, len(artists))
	for _, a := range artists {
		protoArtists = append(protoArtists, ArtistToProto(a))
	}
	return protoArtists
}
