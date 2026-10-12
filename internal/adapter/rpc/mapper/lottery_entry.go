package mapper

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"

	"github.com/liverty-music/backend/internal/entity"
)

// LotteryEntryToProto maps a domain LotteryEntry to Protobuf. The
// authorization is set only once a hold is opened.
func LotteryEntryToProto(e *entity.LotteryEntry) *entityv1.LotteryEntry {
	if e == nil {
		return nil
	}
	pb := &entityv1.LotteryEntry{
		Id:           &entityv1.LotteryEntryId{Value: string(e.ID)},
		TicketTypeId: &entityv1.TicketTypeId{Value: string(e.TicketTypeID)},
		UserId:       &entityv1.UserId{Value: string(e.UserID)},
		TicketCount:  int32(e.TicketCount),
		State:        lotteryEntryStateToProto(e.State),
	}
	if e.CardAuthorizationID != "" {
		pb.CardAuthorizationId = &entityv1.CardAuthorizationId{Value: e.CardAuthorizationID}
	}
	if e.DrawPosition != nil {
		pos := int64(*e.DrawPosition)
		pb.DrawPosition = &pos
	}
	return pb
}

// LotteryEntriesToProto maps a list of LotteryEntries to Protobuf.
func LotteryEntriesToProto(entries []*entity.LotteryEntry) []*entityv1.LotteryEntry {
	out := make([]*entityv1.LotteryEntry, 0, len(entries))
	for _, e := range entries {
		if pb := LotteryEntryToProto(e); pb != nil {
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
	case entity.LotteryEntryStateWithdrawn:
		return entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_WITHDRAWN
	case entity.LotteryEntryStateDraft:
		return entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_DRAFT
	default:
		return entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_UNSPECIFIED
	}
}
