package pb

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/entity"
)

// FromArtist maps a domain Artist entity to its Protobuf wire representation.
func FromArtist(a *entity.Artist) *entityv1.Artist {
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
	if a.OrganizerID != nil {
		proto.OrganizerId = &entityv1.OrganizerId{Value: *a.OrganizerID}
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
	if url := entity.BestByLikes(f.MusicBanner); url != "" {
		proto.MusicBanner = &entityv1.Url{Value: url}
	}

	return proto
}

// FromArtists maps a collection of domain Artist entities to a collection of Protobuf messages.
func FromArtists(artists []*entity.Artist) []*entityv1.Artist {
	protoArtists := make([]*entityv1.Artist, 0, len(artists))
	for _, a := range artists {
		protoArtists = append(protoArtists, FromArtist(a))
	}
	return protoArtists
}
