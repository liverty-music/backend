package usecase

import (
	"context"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/analyticslog"
)

// WorkflowDrawDueSales is the stable name of the scheduled workflow that
// starts the draw of every Lottery sale due for one.
const WorkflowDrawDueSales = "draw-due-sales"

// DrawDueSalesSchedule runs [WorkflowDrawDueSales] at second 0 of every
// minute (a cron expression with seconds).
const DrawDueSalesSchedule = "0 * * * * *"

// WorkflowDraw is the stable name of the workflow that draws one sale.
const WorkflowDraw = "draw"

// DrawWorkflowID names the draw of a sale, so the sale is drawn by one
// workflow however often it is listed.
func DrawWorkflowID(saleID entity.TicketSaleID) string {
	return "draw:" + string(saleID)
}

// drawnEntry is one entry of a stock's draw order, as the ordering step
// records it. The tags pin the recorded names, which a resumed draw reads.
type drawnEntry struct {
	ID          entity.LotteryEntryID `json:"id"`
	TicketCount int                   `json:"ticket_count"`
}

// DrawDueSales implements [LotteryEntryUseCase].
func (uc *lotteryEntryUseCase) DrawDueSales(ctx context.Context, f Flow, at time.Time) (int, error) {
	due, err := Step(ctx, f, "list due sales", func(ctx context.Context) ([]entity.TicketSaleID, error) {
		sales, err := uc.saleRepo.ListDueForDraw(ctx, at)
		if err != nil {
			return nil, err
		}
		ids := make([]entity.TicketSaleID, len(sales))
		for i, s := range sales {
			ids[i] = s.ID
		}
		return ids, nil
	})
	if err != nil {
		return 0, err
	}
	for _, id := range due {
		// One workflow per sale: a sale listed again while its draw runs, or
		// after it failed, starts nothing; a failed draw is reported to an
		// admin and never stops the others.
		if err := f.Start(context.WithoutCancel(ctx), WorkflowDraw, DrawWorkflowID(id), id); err != nil {
			return 0, err
		}
	}
	return len(due), nil
}

// Draw implements [LotteryEntryUseCase].
func (uc *lotteryEntryUseCase) Draw(ctx context.Context, f Flow, saleID entity.TicketSaleID) (int, error) {
	eventIDs, err := Step(ctx, f, "get sale", func(ctx context.Context) ([]string, error) {
		sale, err := uc.saleRepo.Get(ctx, saleID)
		if err != nil {
			return nil, err
		}
		return sale.EventIDs, nil
	})
	if err != nil {
		return 0, err
	}
	stockIDs, err := Step(ctx, f, "list stocks", func(ctx context.Context) ([]entity.TicketStockID, error) {
		var ids []entity.TicketStockID
		for _, eventID := range eventIDs {
			stocks, err := uc.stockRepo.ListByEvent(ctx, eventID, uc.clock())
			if err != nil {
				return nil, err
			}
			for _, s := range stocks {
				ids = append(ids, s.ID)
			}
		}
		return ids, nil
	})
	if err != nil {
		return 0, err
	}

	orders := make([]entity.StockDrawOrder, 0, len(stockIDs))
	ordered := 0
	for _, stockID := range stockIDs {
		// The random order is recorded by its step, so a resumed draw records
		// with the order computed before the stop.
		entries, err := Step(ctx, f, "order "+string(stockID), func(ctx context.Context) ([]drawnEntry, error) {
			entered, err := uc.entryRepo.ListEnteredByTicketSaleAndTicketStock(ctx, saleID, stockID)
			if err != nil {
				return nil, err
			}
			draw := make([]entity.DrawEntry, len(entered))
			for i, e := range entered {
				draw[i] = entity.DrawEntry{ID: e.ID, TicketCount: e.Order.TicketCount}
			}
			shuffled := entity.ShuffleDrawOrder(draw, uc.newRand())
			out := make([]drawnEntry, len(shuffled))
			for i, e := range shuffled {
				out[i] = drawnEntry{ID: e.ID, TicketCount: e.TicketCount}
			}
			return out, nil
		})
		if err != nil {
			return 0, err
		}
		if len(entries) == 0 {
			continue
		}
		order := entity.StockDrawOrder{TicketStockID: stockID, Entries: make([]entity.DrawEntry, len(entries))}
		for i, e := range entries {
			order.Entries[i] = entity.DrawEntry{ID: e.ID, TicketCount: e.TicketCount}
		}
		orders = append(orders, order)
		ordered += len(entries)
	}
	if ordered == 0 {
		// No Entered entry: the closed sale is already drawn.
		return 0, nil
	}

	if _, err := Step(ctx, f, "persist outcome", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, uc.entryRepo.PersistDrawOutcome(ctx, saleID, orders)
	}); err != nil {
		return 0, err
	}
	uc.logger.Info(ctx, "draw recorded", analyticslog.Attrs(analyticslog.TopicPayment,
		slog.String("ticket_sale_id", string(saleID)),
		slog.Int("stock_count", len(orders)),
		slog.Int("entry_count", ordered),
	)...)
	return ordered, nil
}
