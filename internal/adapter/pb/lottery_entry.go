package pb

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"

	"github.com/liverty-music/backend/internal/entity"
)

// FromLotteryEntry maps a domain LotteryEntry to Protobuf; its Order is
// mapped on its own with [FromOrder].
func FromLotteryEntry(e *entity.LotteryEntry) *entityv1.LotteryEntry {
	if e == nil {
		return nil
	}
	pb := &entityv1.LotteryEntry{
		Id:           &entityv1.LotteryEntryId{Value: string(e.ID)},
		OrderId:      &entityv1.OrderId{Value: string(e.OrderID)},
		TicketSaleId: &entityv1.TicketSaleId{Value: string(e.TicketSaleID)},
		State:        lotteryEntryStateToProto(e.State),
	}
	if e.DrawPosition != nil {
		pos := int64(*e.DrawPosition)
		pb.DrawPosition = &pos
	}
	pb.LostReason = lotteryEntryLostReasonToProto(e.LostReason)
	return pb
}

// lotteryEntryLostReasonToProto maps a domain lost reason to Protobuf;
// unspecified for an entry that is not Lost.
func lotteryEntryLostReasonToProto(r entity.LotteryEntryLostReason) entityv1.LotteryEntryLostReason {
	switch r {
	case entity.LotteryEntryLostReasonNotDrawn:
		return entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_NOT_DRAWN
	case entity.LotteryEntryLostReasonCardNotCharged:
		return entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_CARD_NOT_CHARGED
	default:
		return entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_UNSPECIFIED
	}
}

// FromLotteryEntries maps a list of LotteryEntries to Protobuf.
func FromLotteryEntries(entries []*entity.LotteryEntry) []*entityv1.LotteryEntry {
	out := make([]*entityv1.LotteryEntry, 0, len(entries))
	for _, e := range entries {
		if pb := FromLotteryEntry(e); pb != nil {
			out = append(out, pb)
		}
	}
	return out
}

// lotteryEntryStateToProto maps a domain LotteryEntry state to Protobuf.
func lotteryEntryStateToProto(s entity.LotteryEntryState) entityv1.LotteryEntryState {
	switch s {
	case entity.LotteryEntryStateEntered:
		return entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_ENTERED
	case entity.LotteryEntryStateWon:
		return entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_WON
	case entity.LotteryEntryStateLost:
		return entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_LOST
	default:
		return entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_UNSPECIFIED
	}
}
