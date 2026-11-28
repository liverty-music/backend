package pb

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/entity"
	"google.golang.org/genproto/googleapis/type/date"
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

// ToMBIDStatus maps an ArtistMbidStatus to the entity's MBIDStatus. It returns
// false for UNSPECIFIED or an unknown value.
func ToMBIDStatus(s entityv1.ArtistMbidStatus) (entity.MBIDStatus, bool) {
	switch s {
	case entityv1.ArtistMbidStatus_ARTIST_MBID_STATUS_UNVERIFIED:
		return entity.MBIDStatusUnverified, true
	case entityv1.ArtistMbidStatus_ARTIST_MBID_STATUS_VERIFIED:
		return entity.MBIDStatusVerified, true
	case entityv1.ArtistMbidStatus_ARTIST_MBID_STATUS_AMBIGUOUS:
		return entity.MBIDStatusAmbiguous, true
	default:
		return "", false
	}
}

// FromCatalogArtist maps a CatalogArtist to its entity/v1 message.
func FromCatalogArtist(c *entity.CatalogArtist) *entityv1.CatalogArtist {
	if c == nil {
		return nil
	}
	msg := &entityv1.CatalogArtist{
		Mbid:          &entityv1.Mbid{Value: c.MBID},
		Name:          &entityv1.ArtistName{Value: c.Name},
		Begin:         fromActivityDate(c.Begin),
		End:           fromActivityDate(c.End),
		ActivityEnded: c.ActivityEnded,
	}
	if c.Country != nil {
		msg.Country = &entityv1.CountryCode{Value: *c.Country}
	}
	if c.Disambiguation != nil {
		msg.Disambiguation = &entityv1.Disambiguation{Value: *c.Disambiguation}
	}
	if c.OfficialSiteURL != nil {
		msg.OfficialSiteUrl = &entityv1.Url{Value: *c.OfficialSiteURL}
	}
	return msg
}

// FromCatalogArtists maps CatalogArtists to their entity/v1 messages, in order.
func FromCatalogArtists(cs []*entity.CatalogArtist) []*entityv1.CatalogArtist {
	msgs := make([]*entityv1.CatalogArtist, 0, len(cs))
	for _, c := range cs {
		msgs = append(msgs, FromCatalogArtist(c))
	}
	return msgs
}

// fromActivityDate maps an ActivityDate to its message; nil, and a date
// outside what google.type.Date holds, map to nil.
func fromActivityDate(d *entity.ActivityDate) *entityv1.ActivityDate {
	if d == nil || d.Year < 1 || d.Year > 9999 || d.Month < 0 || d.Month > 12 || d.Day < 0 || d.Day > 31 {
		return nil
	}
	return &entityv1.ActivityDate{Value: &date.Date{Year: int32(d.Year), Month: int32(d.Month), Day: int32(d.Day)}}
}
