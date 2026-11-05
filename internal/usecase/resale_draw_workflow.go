package usecase

import (
	"context"
	"log/slog"
	mrand "math/rand/v2"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/analyticslog"
)

// WorkflowDrawResale is the stable name of the scheduled workflow that starts
// the resale draw of every TicketStock due for one.
const WorkflowDrawResale = "draw-resale"

// DrawResaleSchedule runs [WorkflowDrawResale] at second 0 of every minute
// (design D3).
const DrawResaleSchedule = "0 * * * * *"

// WorkflowDrawResaleStock is the stable name of the workflow that draws one
// TicketStock's resale at one time.
const WorkflowDrawResaleStock = "draw-resale-stock"

// DrawResaleStockWorkflowID names the draw of a stock at a time, so a stock is
// drawn by one workflow per scheduled time.
func DrawResaleStockWorkflowID(stockID entity.TicketStockID, at time.Time) string {
	return "draw-resale:" + string(stockID) + ":" + at.UTC().Format(time.RFC3339)
}

// ResaleStockDraw is the input of [WorkflowDrawResaleStock]. The tags pin the
// recorded names, which a resumed draw reads.
type ResaleStockDraw struct {
	StockID entity.TicketStockID `json:"ticket_stock_id"`
	At      time.Time            `json:"at"`
}

// DrawResale implements [ResaleEntryUseCase].
func (uc *resaleEntryUseCase) DrawResale(ctx context.Context, f Flow, at time.Time) (int, error) {
	due, err := Step(ctx, f, "list due stocks", func(ctx context.Context) ([]entity.TicketStockID, error) {
		stocks, err := uc.entryRepo.ListDueStocks(ctx)
		if err != nil {
			return nil, err
		}
		var ids []entity.TicketStockID
		for _, s := range stocks {
			if s.StartTime == nil {
				continue
			}
			if draw, _ := entity.ResaleDrawAt(at, *s.StartTime); draw {
				ids = append(ids, s.StockID)
			}
		}
		return ids, nil
	})
	if err != nil {
		return 0, err
	}
	for _, id := range due {
		// One workflow per stock and time: a stock whose draw fails is
		// reported to an admin and never stops the others (design D4).
		if err := f.Start(context.WithoutCancel(ctx), WorkflowDrawResaleStock, DrawResaleStockWorkflowID(id, at), ResaleStockDraw{StockID: id, At: at}); err != nil {
			return 0, err
		}
	}
	return len(due), nil
}

// DrawStock implements [ResaleEntryUseCase].
func (uc *resaleEntryUseCase) DrawStock(ctx context.Context, f Flow, in ResaleStockDraw) (int, error) {
	outcome, err := Step(ctx, f, "draw", func(ctx context.Context) (resaleDrawCounts, error) {
		stock, err := uc.stockRepo.Get(ctx, in.StockID, in.At)
		if err != nil {
			return resaleDrawCounts{}, err
		}
		event, err := uc.eventRepo.Get(ctx, stock.EventID)
		if err != nil {
			return resaleDrawCounts{}, err
		}
		if event.StartTime == nil {
			return resaleDrawCounts{}, nil
		}
		draw, last := entity.ResaleDrawAt(in.At, *event.StartTime)
		if !draw {
			return resaleDrawCounts{}, nil
		}
		published, err := uc.eventState.IsEventPublished(ctx, stock.EventID)
		if err != nil {
			return resaleDrawCounts{}, err
		}
		rnd := uc.newRand()
		recorded, err := uc.entryRepo.DrawUnderLock(ctx, in.StockID, in.At, func(entries []*entity.ResaleEntry, free []entity.ResaleListingID) entity.ResaleDrawOutcome {
			shuffled := shuffleResaleEntries(entries, rnd)
			return entity.DrawResale(shuffled, free, in.At, last, !published)
		})
		if err != nil {
			return resaleDrawCounts{}, err
		}
		return resaleDrawCounts{Won: len(recorded.Winners), Lapsed: len(recorded.Lapsed), Lost: len(recorded.Lost)}, nil
	})
	if err != nil {
		return 0, err
	}
	uc.logger.Info(ctx, "resale draw recorded", analyticslog.Attrs(analyticslog.TopicPayment,
		slog.String("ticket_stock_id", string(in.StockID)),
		slog.Time("draw_time", in.At),
		slog.Int("won", outcome.Won),
		slog.Int("lapsed", outcome.Lapsed),
		slog.Int("lost", outcome.Lost),
	)...)
	return outcome.Won + outcome.Lapsed + outcome.Lost, nil
}

// resaleDrawCounts is what one stock's draw recorded, as its step records it.
type resaleDrawCounts struct {
	Won    int `json:"won"`
	Lapsed int `json:"lapsed"`
	Lost   int `json:"lost"`
}

// shuffleResaleEntries returns the entries in a random order.
func shuffleResaleEntries(entries []*entity.ResaleEntry, rnd *mrand.Rand) []*entity.ResaleEntry {
	out := append([]*entity.ResaleEntry(nil), entries...)
	rnd.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}
