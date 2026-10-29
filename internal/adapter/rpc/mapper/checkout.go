package mapper

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/liverty-music/backend/internal/entity"
)

// CheckoutToProto maps a domain Checkout to Protobuf; its Order is mapped
// on its own with [OrderToProto].
func CheckoutToProto(c *entity.Checkout) *entityv1.Checkout {
	if c == nil {
		return nil
	}
	pb := &entityv1.Checkout{
		Id:             &entityv1.CheckoutId{Value: string(c.ID)},
		OrderId:        &entityv1.OrderId{Value: string(c.OrderID)},
		TicketSaleId:   &entityv1.TicketSaleId{Value: string(c.TicketSaleID)},
		Status:         checkoutStatusToProto(c.Status),
		HoldExpireTime: timestamppb.New(c.HoldExpireTime),
	}
	if c.CommitTime != nil {
		pb.CommitTime = timestamppb.New(*c.CommitTime)
	}
	if c.CaptureTime != nil {
		pb.CaptureTime = timestamppb.New(*c.CaptureTime)
	}
	return pb
}

func checkoutStatusToProto(s entity.CheckoutStatus) entityv1.CheckoutStatus {
	switch s {
	case entity.CheckoutStatusHeld:
		return entityv1.CheckoutStatus_CHECKOUT_STATUS_HELD
	case entity.CheckoutStatusCommitted:
		return entityv1.CheckoutStatus_CHECKOUT_STATUS_COMMITTED
	case entity.CheckoutStatusCompleted:
		return entityv1.CheckoutStatus_CHECKOUT_STATUS_COMPLETED
	case entity.CheckoutStatusExpired:
		return entityv1.CheckoutStatus_CHECKOUT_STATUS_EXPIRED
	case entity.CheckoutStatusReleased:
		return entityv1.CheckoutStatus_CHECKOUT_STATUS_RELEASED
	default:
		return entityv1.CheckoutStatus_CHECKOUT_STATUS_UNSPECIFIED
	}
}
