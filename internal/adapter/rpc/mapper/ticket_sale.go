package mapper

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/entity"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TicketSaleToProto maps a domain TicketSale to Protobuf. withCounts sets the
// quantity and the sold count, which only the owning Organizer may see.
func TicketSaleToProto(s *entity.TicketSale, withCounts bool) *entityv1.TicketSale {
	if s == nil {
		return nil
	}
	pb := &entityv1.TicketSale{
		Id:              &entityv1.TicketSaleId{Value: string(s.ID)},
		EventId:         &entityv1.EventId{Value: s.EventID},
		Method:          ticketSaleMethodToProto(s.Method),
		SaleStartTime:   timestamppb.New(s.SaleStartTime),
		SaleEndTime:     timestamppb.New(s.SaleEndTime),
		Price:           s.Price,
		PerAccountLimit: int32(s.PerAccountLimit),
	}
	if withCounts {
		quantity, sold := int32(s.Quantity), int32(s.SoldCount)
		pb.Quantity, pb.SoldCount = &quantity, &sold
	}
	return pb
}

func ticketSaleMethodToProto(m entity.TicketSaleMethod) entityv1.TicketSaleMethod {
	if m == entity.TicketSaleMethodFirstCome {
		return entityv1.TicketSaleMethod_TICKET_SALE_METHOD_FIRST_COME
	}
	return entityv1.TicketSaleMethod_TICKET_SALE_METHOD_UNSPECIFIED
}

// TicketSaleStateToProto maps a domain sale state to Protobuf.
func TicketSaleStateToProto(s entity.TicketSaleState) entityv1.TicketSaleState {
	switch s {
	case entity.TicketSaleStateNotYetOnSale:
		return entityv1.TicketSaleState_TICKET_SALE_STATE_NOT_YET_ON_SALE
	case entity.TicketSaleStateOnSale:
		return entityv1.TicketSaleState_TICKET_SALE_STATE_ON_SALE
	case entity.TicketSaleStateAllHeld:
		return entityv1.TicketSaleState_TICKET_SALE_STATE_ALL_HELD
	case entity.TicketSaleStateSoldOut:
		return entityv1.TicketSaleState_TICKET_SALE_STATE_SOLD_OUT
	case entity.TicketSaleStateEnded:
		return entityv1.TicketSaleState_TICKET_SALE_STATE_ENDED
	default:
		return entityv1.TicketSaleState_TICKET_SALE_STATE_UNSPECIFIED
	}
}

// ReservationToProto maps a domain Reservation to Protobuf.
func ReservationToProto(r *entity.Reservation) *entityv1.Reservation {
	if r == nil {
		return nil
	}
	pb := &entityv1.Reservation{
		Id:             &entityv1.ReservationId{Value: string(r.ID)},
		TicketSaleId:   &entityv1.TicketSaleId{Value: string(r.TicketSaleID)},
		TicketCount:    int32(r.TicketCount),
		Amount:         r.Amount,
		Status:         reservationStatusToProto(r.Status),
		HoldExpireTime: timestamppb.New(r.HoldExpireTime),
	}
	if r.CommitTime != nil {
		pb.CommitTime = timestamppb.New(*r.CommitTime)
	}
	if r.CaptureTime != nil {
		pb.CaptureTime = timestamppb.New(*r.CaptureTime)
	}
	return pb
}

func reservationStatusToProto(s entity.ReservationStatus) entityv1.ReservationStatus {
	switch s {
	case entity.ReservationStatusHeld:
		return entityv1.ReservationStatus_RESERVATION_STATUS_HELD
	case entity.ReservationStatusCommitted:
		return entityv1.ReservationStatus_RESERVATION_STATUS_COMMITTED
	case entity.ReservationStatusCompleted:
		return entityv1.ReservationStatus_RESERVATION_STATUS_COMPLETED
	case entity.ReservationStatusExpired:
		return entityv1.ReservationStatus_RESERVATION_STATUS_EXPIRED
	case entity.ReservationStatusReleased:
		return entityv1.ReservationStatus_RESERVATION_STATUS_RELEASED
	default:
		return entityv1.ReservationStatus_RESERVATION_STATUS_UNSPECIFIED
	}
}

// HolderIdentityToProto maps a domain holder identity to Protobuf, or nil.
func HolderIdentityToProto(h *entity.HolderIdentity) *entityv1.HolderIdentity {
	if h == nil {
		return nil
	}
	return holderIdentityToProto(*h)
}
