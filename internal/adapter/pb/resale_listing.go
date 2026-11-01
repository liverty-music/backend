package pb

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// FromResaleListing maps a listing, with its deadline and its status when
// read, to Protobuf. Only the fan's own side is mapped: never anything about
// the fan who bought it.
func FromResaleListing(v *usecase.ResaleListingAt) *entityv1.ResaleListing {
	if v == nil || v.Listing == nil {
		return nil
	}
	l := v.Listing
	pb := &entityv1.ResaleListing{
		Id:            &entityv1.ResaleListingId{Value: string(l.ID)},
		TicketId:      &entityv1.TicketId{Value: string(l.TicketID)},
		Price:         &entityv1.SeatTypePrice{Value: l.Price},
		Fee:           l.Fee(),
		RefundAmount:  l.RefundAmount(),
		Status:        resaleListingStatusToProto(v.Status),
		CreateTime:    timestamppb.New(l.CreateTime),
		DeadlineTime:  timestamppb.New(v.Deadline),
		TicketStockId: &entityv1.TicketStockId{Value: string(l.TicketStockID)},
	}
	if l.SoldTime != nil {
		pb.SoldTime = timestamppb.New(*l.SoldTime)
	}
	if l.RefundTime != nil {
		pb.RefundTime = timestamppb.New(*l.RefundTime)
	}
	return pb
}

// FromResaleListings maps listings to Protobuf; an empty slice for none.
func FromResaleListings(views []*usecase.ResaleListingAt) []*entityv1.ResaleListing {
	out := make([]*entityv1.ResaleListing, 0, len(views))
	for _, v := range views {
		out = append(out, FromResaleListing(v))
	}
	return out
}

func resaleListingStatusToProto(s entity.ResaleListingStatus) entityv1.ResaleListingStatus {
	switch s {
	case entity.ResaleListingStatusListed:
		return entityv1.ResaleListingStatus_RESALE_LISTING_STATUS_LISTED
	case entity.ResaleListingStatusSold:
		return entityv1.ResaleListingStatus_RESALE_LISTING_STATUS_SOLD
	case entity.ResaleListingStatusWithdrawn:
		return entityv1.ResaleListingStatus_RESALE_LISTING_STATUS_WITHDRAWN
	case entity.ResaleListingStatusCancelled:
		return entityv1.ResaleListingStatus_RESALE_LISTING_STATUS_CANCELLED
	case entity.ResaleListingStatusExpired:
		return entityv1.ResaleListingStatus_RESALE_LISTING_STATUS_EXPIRED
	default:
		return entityv1.ResaleListingStatus_RESALE_LISTING_STATUS_UNSPECIFIED
	}
}
