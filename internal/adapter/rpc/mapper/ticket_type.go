package mapper

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	organizerticketsalev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/ticket_sale/v1"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// TicketTypeToProto maps a TicketType, with where it stood when read, to
// Protobuf. A view with a state is a FirstCome TicketType (the use case sets
// the state only for FirstCome); only then are the state, LowStock and, with
// withCounts, the sold and held counts set. withCounts sets the quantity,
// which only the owning Organizer may see.
func TicketTypeToProto(v *usecase.TicketTypeView, withCounts bool) *entityv1.TicketType {
	if v == nil || v.TicketType == nil {
		return nil
	}
	t := v.TicketType
	pb := &entityv1.TicketType{
		Id:              &entityv1.TicketTypeId{Value: string(t.ID)},
		TicketSaleId:    &entityv1.TicketSaleId{Value: string(t.TicketSaleID)},
		EventId:         &entityv1.EventId{Value: t.EventID},
		Price:           &entityv1.TicketTypePrice{Value: t.Price},
		PerAccountLimit: int32(t.PerAccountLimit),
	}
	firstCome := v.State != entity.TicketTypeStateUnspecified
	if firstCome {
		pb.State = ticketTypeStateToProto(v.State)
		pb.LowStock = v.LowStock
	}
	if withCounts {
		quantity := int32(t.Quantity)
		pb.Quantity = &quantity
		if firstCome {
			sold, held := int32(t.SoldCount), int32(t.HeldCount)
			pb.SoldCount, pb.HeldCount = &sold, &held
		}
	}
	return pb
}

// LotteryEntryStatsToProto returns the entry tallies of every Lottery
// TicketType of the sales, for their Organizer; a TicketType without tallies
// is left out.
func LotteryEntryStatsToProto(views []*usecase.TicketSaleView) []*organizerticketsalev1.LotteryEntryStats {
	out := []*organizerticketsalev1.LotteryEntryStats{}
	for _, v := range views {
		if v == nil {
			continue
		}
		for _, tv := range v.TicketTypes {
			if tv == nil || tv.TicketType == nil || tv.Stats == nil {
				continue
			}
			s := tv.Stats
			out = append(out, &organizerticketsalev1.LotteryEntryStats{
				TicketTypeId:   &entityv1.TicketTypeId{Value: string(tv.TicketType.ID)},
				EntryCount:     int32(s.EntryCount),
				TicketCount:    int32(s.TicketCount),
				WonEntryCount:  int32(s.WonEntryCount),
				WonTicketCount: int32(s.WonTicketCount),
				LostEntryCount: int32(s.LostEntryCount),
			})
		}
	}
	return out
}

// ticketTypeStateToProto maps a domain TicketType state to Protobuf.
func ticketTypeStateToProto(s entity.TicketTypeState) entityv1.TicketTypeState {
	switch s {
	case entity.TicketTypeStateNotYetOnSale:
		return entityv1.TicketTypeState_TICKET_TYPE_STATE_NOT_YET_ON_SALE
	case entity.TicketTypeStateOnSale:
		return entityv1.TicketTypeState_TICKET_TYPE_STATE_ON_SALE
	case entity.TicketTypeStateAllHeld:
		return entityv1.TicketTypeState_TICKET_TYPE_STATE_ALL_HELD
	case entity.TicketTypeStateSoldOut:
		return entityv1.TicketTypeState_TICKET_TYPE_STATE_SOLD_OUT
	case entity.TicketTypeStateEnded:
		return entityv1.TicketTypeState_TICKET_TYPE_STATE_ENDED
	default:
		return entityv1.TicketTypeState_TICKET_TYPE_STATE_UNSPECIFIED
	}
}
