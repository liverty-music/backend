package event

import (
	"errors"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
)

// announcementID returns the id of the announcement msg carries: its
// CloudEvents id, the same on every delivery, or the message UUID of a JSON
// message published before the outbox.
func announcementID(msg *message.Message) string {
	if id := msg.Metadata.Get("ce_id"); id != "" {
		return id
	}
	return msg.UUID
}

// parseArtistCreated reads an ARTIST.created announcement: the new Artist, or
// the JSON of one published before the outbox.
func parseArtistCreated(msg *message.Message) (entity.ArtistCreatedData, error) {
	if !messaging.IsProtobuf(msg) {
		var data entity.ArtistCreatedData
		err := messaging.ParseCloudEventData(msg, &data)
		return data, err
	}
	var artist entityv1.Artist
	if err := messaging.ParseProtobufData(msg, &artist); err != nil {
		return entity.ArtistCreatedData{}, err
	}
	data := entity.ArtistCreatedData{
		ArtistID:   artist.GetId().GetValue(),
		ArtistName: artist.GetName().GetValue(),
		MBID:       artist.GetMbid().GetValue(),
	}
	if data.ArtistID == "" {
		return entity.ArtistCreatedData{}, errors.New("the Artist has no id")
	}
	return data, nil
}

// parseArtistFollowed reads an ARTIST.followed announcement: the new Follow,
// or the JSON of one published before the outbox.
func parseArtistFollowed(msg *message.Message) (entity.ArtistFollowedData, error) {
	if !messaging.IsProtobuf(msg) {
		var data entity.ArtistFollowedData
		err := messaging.ParseCloudEventData(msg, &data)
		return data, err
	}
	var follow entityv1.Follow
	if err := messaging.ParseProtobufData(msg, &follow); err != nil {
		return entity.ArtistFollowedData{}, err
	}
	data := entity.ArtistFollowedData{UserID: follow.GetUserId().GetValue(), ArtistID: follow.GetArtistId().GetValue()}
	if data.ArtistID == "" {
		return entity.ArtistFollowedData{}, errors.New("the Follow names no Artist")
	}
	return data, nil
}

// parseTicketSaleDiscovered reads a SALES_PHASE.discovered announcement: the
// new TicketSale, or the JSON of one published before the outbox.
func parseTicketSaleDiscovered(msg *message.Message) (entity.TicketSaleDiscoveredData, error) {
	if !messaging.IsProtobuf(msg) {
		var data entity.TicketSaleDiscoveredData
		err := messaging.ParseCloudEventData(msg, &data)
		return data, err
	}
	var sale entityv1.TicketSale
	if err := messaging.ParseProtobufData(msg, &sale); err != nil {
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

// parseResaleListingRefunded reads an ORDER.resale_listing_refunded
// announcement: the refunded ResaleListing, or the JSON of one published
// before the outbox.
func parseResaleListingRefunded(msg *message.Message) (entity.ResaleListingRefundedData, error) {
	if !messaging.IsProtobuf(msg) {
		var data entity.ResaleListingRefundedData
		err := messaging.ParseCloudEventData(msg, &data)
		return data, err
	}
	var listing entityv1.ResaleListing
	if err := messaging.ParseProtobufData(msg, &listing); err != nil {
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
