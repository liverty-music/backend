package pb

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	organizerticketsalev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/ticket_sale/v1"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// FromSeatType maps a SeatType to Protobuf.
func FromSeatType(s *entity.SeatType) *entityv1.SeatType {
	if s == nil {
		return nil
	}
	return &entityv1.SeatType{
		Id:       &entityv1.SeatTypeId{Value: string(s.ID)},
		SeriesId: &entityv1.SeriesId{Value: s.SeriesID},
		Name:     &entityv1.SeatTypeName{Value: s.Name},
		Price:    &entityv1.SeatTypePrice{Value: s.Price},
	}
}

// FromSeatTypes maps SeatTypes to Protobuf.
func FromSeatTypes(types []*entity.SeatType) []*entityv1.SeatType {
	out := make([]*entityv1.SeatType, 0, len(types))
	for _, s := range types {
		if pb := FromSeatType(s); pb != nil {
			out = append(out, pb)
		}
	}
	return out
}

// FromTicketStock maps a TicketStock, read with its counts, to Protobuf
// with its state and LowStock. withCounts sets the quantity and the sold and
// held counts, which only the owning Organizer may see; resaleOpen is whether
// its resale is open, which only the fan boundary evaluates.
func FromTicketStock(s *entity.TicketStock, withCounts, resaleOpen bool) *entityv1.TicketStock {
	if s == nil {
		return nil
	}
	pb := &entityv1.TicketStock{
		Id:         &entityv1.TicketStockId{Value: string(s.ID)},
		EventId:    &entityv1.EventId{Value: s.EventID},
		SeatTypeId: &entityv1.SeatTypeId{Value: string(s.SeatTypeID)},
		State:      ticketStockStateToProto(s.State()),
		LowStock:   s.IsLowStock(),
		ResaleOpen: resaleOpen,
	}
	if withCounts {
		quantity, sold, held := int32(s.Quantity), int32(s.SoldCount), int32(s.HeldCount)
		pb.Quantity, pb.SoldCount, pb.HeldCount = &quantity, &sold, &held
	}
	return pb
}

// FromOwnTicketStocks maps the Organizer's stocks with their counts, and
// the distinct SeatTypes they are of, to Protobuf.
func FromOwnTicketStocks(stocks []*entity.TicketStock) ([]*entityv1.TicketStock, []*entityv1.SeatType) {
	out := make([]*entityv1.TicketStock, 0, len(stocks))
	types := make([]*entity.SeatType, 0, len(stocks))
	for _, s := range stocks {
		if pb := FromTicketStock(s, true, false); pb != nil {
			out = append(out, pb)
			types = appendSeatType(types, s.SeatType)
		}
	}
	return out, FromSeatTypes(types)
}

// FromFanTicketStocks maps the stocks of an event as a fan sees them,
// without counts, and the distinct SeatTypes they are of, to Protobuf.
func FromFanTicketStocks(views []*usecase.TicketStockView) ([]*entityv1.TicketStock, []*entityv1.SeatType) {
	out := make([]*entityv1.TicketStock, 0, len(views))
	types := make([]*entity.SeatType, 0, len(views))
	for _, v := range views {
		if v == nil {
			continue
		}
		if pb := FromTicketStock(v.Stock, false, v.ResaleOpen); pb != nil {
			out = append(out, pb)
			types = appendSeatType(types, v.Stock.SeatType)
		}
	}
	return out, FromSeatTypes(types)
}

// appendSeatType appends s unless it is nil or already listed.
func appendSeatType(types []*entity.SeatType, s *entity.SeatType) []*entity.SeatType {
	if s == nil {
		return types
	}
	for _, t := range types {
		if t.ID == s.ID {
			return types
		}
	}
	return append(types, s)
}

// FromLotteryEntryStats maps the entry stats of each stock of each
// Lottery sale to Protobuf.
func FromLotteryEntryStats(stats []*usecase.StockEntryStats) []*organizerticketsalev1.LotteryEntryStats {
	out := make([]*organizerticketsalev1.LotteryEntryStats, 0, len(stats))
	for _, st := range stats {
		if st == nil || st.Stock == nil {
			continue
		}
		s := st.Stats
		out = append(out, &organizerticketsalev1.LotteryEntryStats{
			TicketSaleId:   &entityv1.TicketSaleId{Value: string(st.SaleID)},
			TicketStockId:  &entityv1.TicketStockId{Value: string(st.Stock.ID)},
			SeatTypeId:     &entityv1.SeatTypeId{Value: string(st.Stock.SeatTypeID)},
			EntryCount:     int32(s.EntryCount),
			TicketCount:    int32(s.TicketCount),
			WonEntryCount:  int32(s.WonEntryCount),
			WonTicketCount: int32(s.WonTicketCount),
			LostEntryCount: int32(s.LostEntryCount),
		})
	}
	return out
}

// ticketStockStateToProto maps a domain TicketStock state to Protobuf.
func ticketStockStateToProto(s entity.TicketStockState) entityv1.TicketStockState {
	switch s {
	case entity.TicketStockStateAvailable:
		return entityv1.TicketStockState_TICKET_STOCK_STATE_AVAILABLE
	case entity.TicketStockStateAllHeld:
		return entityv1.TicketStockState_TICKET_STOCK_STATE_ALL_HELD
	case entity.TicketStockStateSoldOut:
		return entityv1.TicketStockState_TICKET_STOCK_STATE_SOLD_OUT
	default:
		return entityv1.TicketStockState_TICKET_STOCK_STATE_UNSPECIFIED
	}
}
