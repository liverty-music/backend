package event

import (
	"errors"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/eventmsg"
)

// announcementID returns the id of the announcement msg carries: its
// CloudEvents id, the same on every delivery, or the message UUID when the
// message has no ce_id.
func announcementID(msg *message.Message) string {
	if id := msg.Metadata.Get("ce_id"); id != "" {
		return id
	}
	return msg.UUID
}

// parseArtistCreated reads an ARTIST.created announcement: the new Artist.
func parseArtistCreated(msg *message.Message) (artistCreated, error) {
	var artist entityv1.Artist
	if err := eventmsg.ParseProtobufData(msg, &artist); err != nil {
		return artistCreated{}, err
	}
	data := artistCreated{
		ArtistID:   artist.GetId().GetValue(),
		ArtistName: artist.GetName().GetValue(),
		MBID:       artist.GetMbid().GetValue(),
	}
	if data.ArtistID == "" {
		return artistCreated{}, errors.New("the Artist has no id")
	}
	return data, nil
}

// parseFollowCreated reads a FOLLOW.created announcement: the new Follow.
func parseFollowCreated(msg *message.Message) (followCreated, error) {
	var follow entityv1.Follow
	if err := eventmsg.ParseProtobufData(msg, &follow); err != nil {
		return followCreated{}, err
	}
	data := followCreated{UserID: follow.GetUserId().GetValue(), ArtistID: follow.GetArtistId().GetValue()}
	if data.ArtistID == "" {
		return followCreated{}, errors.New("the Follow names no Artist")
	}
	return data, nil
}

// parseTicketSaleDiscovered reads a TICKET_SALE.discovered announcement: the
// new TicketSale.
func parseTicketSaleDiscovered(msg *message.Message) (entity.TicketSaleDiscoveredData, error) {
	var sale entityv1.TicketSale
	if err := eventmsg.ParseProtobufData(msg, &sale); err != nil {
		return entity.TicketSaleDiscoveredData{}, err
	}
	data := entity.TicketSaleDiscoveredData{
		TicketSaleID:  sale.GetId().GetValue(),
		SeriesID:      sale.GetSeriesId().GetValue(),
		Method:        int16(pb.ToTicketSaleMethod(sale.GetMethod())),
		SaleStartTime: sale.GetSaleStartTime().AsTime(),
	}
	if data.TicketSaleID == "" || data.SeriesID == "" {
		return entity.TicketSaleDiscoveredData{}, errors.New("the TicketSale has no id or no Series")
	}
	return data, nil
}

// parseResaleListingRefunded reads an RESALE_LISTING.refunded
// announcement: the refunded ResaleListing.
func parseResaleListingRefunded(msg *message.Message) (entity.ResaleListingRefundedData, error) {
	var listing entityv1.ResaleListing
	if err := eventmsg.ParseProtobufData(msg, &listing); err != nil {
		return entity.ResaleListingRefundedData{}, err
	}
	data := entity.ResaleListingRefundedData{
		ResaleListingID: listing.GetId().GetValue(),
		UserID:          listing.GetUserId().GetValue(),
		OrderID:         listing.GetOrderId().GetValue(),
		EventID:         listing.GetEventId().GetValue(),
	}
	if data.ResaleListingID == "" {
		return entity.ResaleListingRefundedData{}, errors.New("the ResaleListing has no id")
	}
	return data, nil
}

// artistCreated is what the receivers of an ARTIST.created announcement
// read from the new Artist.
type artistCreated struct {
	// ArtistID is the internal UUID of the artist.
	ArtistID string
	// ArtistName is the display name of the artist.
	ArtistName string
	// MBID is the MusicBrainz identifier for canonical identity.
	MBID string
}

// followCreated is what the receivers of a FOLLOW.created announcement
// read from the new Follow.
type followCreated struct {
	// UserID is the platform-internal user identifier of the follower.
	UserID string
	// ArtistID is the internal UUID of the followed artist.
	ArtistID string
}
