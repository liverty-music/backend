package pb

import (
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/liverty-music/backend/internal/entity"
)

// FromResaleEntry maps a domain ResaleEntry to Protobuf, with the time of its
// event's next draw when nextDraw is given; its Order is mapped on its own
// with [FromOrder].
func FromResaleEntry(e *entity.ResaleEntry, nextDraw *time.Time) *entityv1.ResaleEntry {
	if e == nil {
		return nil
	}
	pb := &entityv1.ResaleEntry{
		Id:         &entityv1.ResaleEntryId{Value: string(e.ID)},
		OrderId:    &entityv1.OrderId{Value: string(e.OrderID)},
		State:      resaleEntryStateToProto(e.State),
		CreateTime: timestamppb.New(e.CreateTime),
		LostReason: resaleEntryLostReasonToProto(e.LostReason),
	}
	if e.DrawTime != nil {
		pb.DrawTime = timestamppb.New(*e.DrawTime)
	}
	if e.WithdrawTime != nil {
		pb.WithdrawTime = timestamppb.New(*e.WithdrawTime)
	}
	if nextDraw != nil && e.State == entity.ResaleEntryStateEntered {
		pb.NextDrawTime = timestamppb.New(*nextDraw)
	}
	return pb
}

// ToResaleEntry maps a Protobuf ResaleEntry, as an announcement carries it,
// back to the domain entry without its Order.
func ToResaleEntry(pb *entityv1.ResaleEntry) *entity.ResaleEntry {
	if pb == nil {
		return nil
	}
	e := &entity.ResaleEntry{
		ID:         entity.ResaleEntryID(pb.GetId().GetValue()),
		OrderID:    entity.OrderID(pb.GetOrderId().GetValue()),
		State:      resaleEntryStateFromProto(pb.GetState()),
		LostReason: resaleEntryLostReasonFromProto(pb.GetLostReason()),
		CreateTime: pb.GetCreateTime().AsTime(),
	}
	if pb.GetDrawTime() != nil {
		t := pb.GetDrawTime().AsTime()
		e.DrawTime = &t
	}
	if pb.GetWithdrawTime() != nil {
		t := pb.GetWithdrawTime().AsTime()
		e.WithdrawTime = &t
	}
	return e
}

var resaleEntryStates = map[entity.ResaleEntryState]entityv1.ResaleEntryState{
	entity.ResaleEntryStateEntered:   entityv1.ResaleEntryState_RESALE_ENTRY_STATE_ENTERED,
	entity.ResaleEntryStateWon:       entityv1.ResaleEntryState_RESALE_ENTRY_STATE_WON,
	entity.ResaleEntryStateLost:      entityv1.ResaleEntryState_RESALE_ENTRY_STATE_LOST,
	entity.ResaleEntryStateLapsed:    entityv1.ResaleEntryState_RESALE_ENTRY_STATE_LAPSED,
	entity.ResaleEntryStateWithdrawn: entityv1.ResaleEntryState_RESALE_ENTRY_STATE_WITHDRAWN,
}

var resaleEntryLostReasons = map[entity.ResaleEntryLostReason]entityv1.ResaleEntryLostReason{
	entity.ResaleEntryLostReasonNoTicketLeft:                  entityv1.ResaleEntryLostReason_RESALE_ENTRY_LOST_REASON_NO_TICKET_LEFT,
	entity.ResaleEntryLostReasonConcertCancelled:              entityv1.ResaleEntryLostReason_RESALE_ENTRY_LOST_REASON_CONCERT_CANCELLED,
	entity.ResaleEntryLostReasonCardNotCharged:                entityv1.ResaleEntryLostReason_RESALE_ENTRY_LOST_REASON_CARD_NOT_CHARGED,
	entity.ResaleEntryLostReasonPaymentNotCompletedBeforeShow: entityv1.ResaleEntryLostReason_RESALE_ENTRY_LOST_REASON_PAYMENT_NOT_COMPLETED_BEFORE_SHOW,
	entity.ResaleEntryLostReasonListingCancelled:              entityv1.ResaleEntryLostReason_RESALE_ENTRY_LOST_REASON_LISTING_CANCELLED,
}

func resaleEntryStateToProto(s entity.ResaleEntryState) entityv1.ResaleEntryState {
	return resaleEntryStates[s]
}

func resaleEntryStateFromProto(s entityv1.ResaleEntryState) entity.ResaleEntryState {
	for d, p := range resaleEntryStates {
		if p == s {
			return d
		}
	}
	return entity.ResaleEntryStateUnspecified
}

// resaleEntryLostReasonToProto maps a domain lost reason to Protobuf;
// unspecified for an entry that is not Lost.
func resaleEntryLostReasonToProto(r entity.ResaleEntryLostReason) entityv1.ResaleEntryLostReason {
	return resaleEntryLostReasons[r]
}

func resaleEntryLostReasonFromProto(r entityv1.ResaleEntryLostReason) entity.ResaleEntryLostReason {
	for d, p := range resaleEntryLostReasons {
		if p == r {
			return d
		}
	}
	return entity.ResaleEntryLostReasonUnspecified
}
