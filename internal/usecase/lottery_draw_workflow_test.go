package usecase_test

import (
	"context"
	mrand "math/rand/v2"
	"testing"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
)

// drawStock is a stock of a closed sale's event with its Entered entries.
type drawStock struct {
	stock   *entity.TicketStock
	entries []*entity.LotteryEntry
}

// stockWith returns a stock of the given id with quantity tickets of which
// sold are sold.
func stockWith(id entity.TicketStockID, quantity, sold int) *entity.TicketStock {
	return &entity.TicketStock{ID: id, EventID: "event-1", SeatTypeID: leSeatS.ID, Quantity: quantity, SoldCount: sold, SeatType: leSeatS}
}

// closedSaleWith returns a closed Lottery sale of event-1 whose stocks are the
// given ones, wiring the reads Draw makes.
func (f *lotteryFixture) closedSaleWith(id entity.TicketSaleID, stocks ...drawStock) *entity.TicketSale {
	sale := lotterySale(entity.VerificationRequirementNone)
	sale.ID = id
	listed := make([]*entity.TicketStock, 0, len(stocks))
	for _, s := range stocks {
		listed = append(listed, s.stock)
		f.entries.EXPECT().ListEnteredByTicketSaleAndTicketStock(mock.Anything, id, s.stock.ID).Return(s.entries, nil).Maybe()
	}
	f.sales.EXPECT().Get(mock.Anything, id).Return(sale, nil).Maybe()
	f.stocks.EXPECT().ListByEvent(mock.Anything, "event-1", mock.Anything).Return(listed, nil).Maybe()
	return sale
}

// recordsOutcome captures the draw orders Draw records for the sale.
func (f *lotteryFixture) recordsOutcome(saleID entity.TicketSaleID, out *[]entity.StockDrawOrder) {
	f.entries.EXPECT().PersistDrawOutcome(mock.Anything, saleID, mock.Anything).RunAndReturn(
		func(_ context.Context, _ entity.TicketSaleID, orders []entity.StockDrawOrder) error {
			*out = orders
			return nil
		})
}

// admitted returns the ids of the entries the record admits from order with
// remaining tickets, as PersistDrawOutcome does.
func admitted(order entity.StockDrawOrder, remaining int) (won, lost []entity.LotteryEntryID, positions map[entity.LotteryEntryID]int) {
	result := entity.AdmitInDrawOrder(order.Entries, remaining, nil)
	positions = map[entity.LotteryEntryID]int{}
	for _, w := range result.Winners {
		won = append(won, w.Entry.ID)
		positions[w.Entry.ID] = w.DrawPosition
	}
	for _, l := range result.Losers {
		lost = append(lost, l.Entry.ID)
		positions[l.Entry.ID] = l.DrawPosition
	}
	return won, lost, positions
}

// drawEntries returns the draw input of entries, in their listed order.
func drawEntries(entries []*entity.LotteryEntry) []entity.DrawEntry {
	out := make([]entity.DrawEntry, len(entries))
	for i, e := range entries {
		out[i] = entity.DrawEntry{ID: e.ID, TicketCount: e.Order.TicketCount}
	}
	return out
}

// seedWhere returns the first seed whose draw order of entries satisfies ok.
func seedWhere(entries []*entity.LotteryEntry, ok func([]entity.DrawEntry) bool) uint64 {
	for seed := uint64(1); ; seed++ {
		if ok(entity.ShuffleDrawOrder(drawEntries(entries), mrand.New(mrand.NewPCG(seed, seed+1)))) {
			return seed
		}
	}
}

func TestLotteryEntryUseCase_Draw(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("winners and losers recorded", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/draw "Winners and losers recorded"
		entries := []*entity.LotteryEntry{
			lotteryEntry("two-a", "stock-1", "fan-1", entity.LotteryEntryStateEntered, 2, "pi_a"),
			lotteryEntry("two-b", "stock-1", "fan-2", entity.LotteryEntryStateEntered, 2, "pi_b"),
			lotteryEntry("three", "stock-1", "fan-3", entity.LotteryEntryStateEntered, 3, "pi_c"),
		}
		// A random order that puts the two 2-ticket entries first.
		seed := seedWhere(entries, func(o []entity.DrawEntry) bool { return o[2].ID == "three" })
		f := newLotteryFixture(t, afterWindow, seed)
		sale := f.closedSaleWith("sale-l", drawStock{stockWith("stock-1", 4, 0), entries})
		var out []entity.StockDrawOrder
		f.recordsOutcome(sale.ID, &out)
		flow := &inlineFlow{}

		n, err := f.uc.Draw(ctx, flow, sale.ID)

		require.NoError(t, err)
		assert.Equal(t, 3, n)
		require.Len(t, out, 1)
		won, lost, _ := admitted(out[0], 4)
		assert.ElementsMatch(t, []entity.LotteryEntryID{"two-a", "two-b"}, won)
		assert.Equal(t, []entity.LotteryEntryID{"three"}, lost)
		// No card is charged or released by the draw: the card authorization
		// mock has no expectation, so any call would fail the test.
		f.cardAuthorizations.AssertNotCalled(t, "Capture", mock.Anything, mock.Anything)
		f.cardAuthorizations.AssertNotCalled(t, "Release", mock.Anything, mock.Anything)
		assert.Empty(t, flow.started, "the draw starts no other workflow")
	})

	t.Run("each stock is drawn against its own remaining tickets", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/draw "Each stock is drawn against its own remaining tickets"
		f := newLotteryFixture(t, afterWindow, 7)
		var sEntries, aEntries []*entity.LotteryEntry
		for i := range 20 {
			sEntries = append(sEntries, lotteryEntry(entity.LotteryEntryID("s-"+string(rune('a'+i))), "stock-s", entity.UserID("fan-s"+string(rune('a'+i))), entity.LotteryEntryStateEntered, 4, "pi_s"))
		}
		for i := range 6 {
			aEntries = append(aEntries, lotteryEntry(entity.LotteryEntryID("a-"+string(rune('a'+i))), "stock-a", entity.UserID("fan-a"+string(rune('a'+i))), entity.LotteryEntryStateEntered, 2, "pi_a"))
		}
		sale := f.closedSaleWith("sale-l",
			drawStock{stockWith("stock-s", 100, 0), sEntries},
			drawStock{stockWith("stock-a", 4, 0), aEntries},
		)
		var out []entity.StockDrawOrder
		f.recordsOutcome(sale.ID, &out)

		_, err := f.uc.Draw(ctx, &inlineFlow{}, sale.ID)

		require.NoError(t, err)
		require.Len(t, out, 2)
		assert.Equal(t, entity.TicketStockID("stock-s"), out[0].TicketStockID)
		assert.Len(t, out[0].Entries, 20, "80 S席 tickets requested")
		sWon, _, _ := admitted(out[0], 100)
		assert.Len(t, sWon, 20, "every S席 entry is Won")
		assert.Equal(t, entity.TicketStockID("stock-a"), out[1].TicketStockID)
		assert.Len(t, out[1].Entries, 6, "12 A席 tickets requested")
		aWon, _, _ := admitted(out[1], 4)
		assert.LessOrEqual(t, len(aWon)*2, 4, "the A席 Won entries hold at most 4 tickets")
	})

	t.Run("losers in draw order", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/draw "Losers in draw order"
		f := newLotteryFixture(t, afterWindow, 5)
		sale := f.closedSaleWith("sale-l", drawStock{stockWith("stock-1", 2, 0), []*entity.LotteryEntry{
			lotteryEntry("e-1", "stock-1", "fan-1", entity.LotteryEntryStateEntered, 2, "pi_1"),
			lotteryEntry("e-2", "stock-1", "fan-2", entity.LotteryEntryStateEntered, 2, "pi_2"),
			lotteryEntry("e-3", "stock-1", "fan-3", entity.LotteryEntryStateEntered, 2, "pi_3"),
		}})
		var out []entity.StockDrawOrder
		f.recordsOutcome(sale.ID, &out)

		_, err := f.uc.Draw(ctx, &inlineFlow{}, sale.ID)

		require.NoError(t, err)
		require.Len(t, out, 1)
		_, lost, positions := admitted(out[0], 2)
		require.Len(t, lost, 2)
		assert.Less(t, positions[lost[0]], positions[lost[1]], "the losers read in draw order")
		assert.Len(t, positions, 3, "each entry has its own position within its stock")
	})

	t.Run("no entries", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/draw "No entries"
		f := newLotteryFixture(t, afterWindow, 1)
		sale := f.closedSaleWith("sale-l", drawStock{stockWith("stock-1", 100, 0), nil})

		n, err := f.uc.Draw(ctx, &inlineFlow{}, sale.ID)

		require.NoError(t, err)
		assert.Zero(t, n)
		f.entries.AssertNotCalled(t, "PersistDrawOutcome", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("stopped before the outcome was recorded", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/draw "Stopped before the outcome was recorded"
		// Every run of the random source gives a new order, as a restarted
		// process would.
		var draws uint64
		f := &lotteryFixture{
			sales:              entitymocks.NewMockTicketSaleRepository(t),
			stocks:             entitymocks.NewMockTicketStockRepository(t),
			entries:            entitymocks.NewMockLotteryEntryRepository(t),
			cardAuthorizations: entitymocks.NewMockCardAuthorizationRepository(t),
		}
		f.uc = usecase.NewLotteryEntryUseCaseWithRand(usecase.LotteryEntryDeps{
			SaleRepo: f.sales, StockRepo: f.stocks, EntryRepo: f.entries, CardAuthorizations: f.cardAuthorizations,
			Clock: fixedClock(afterWindow), Logger: newTestLogger(t),
		}, func() *mrand.Rand { draws++; return mrand.New(mrand.NewPCG(draws, draws)) })
		var sEntries, aEntries []*entity.LotteryEntry
		for i := range 8 {
			sEntries = append(sEntries, lotteryEntry(entity.LotteryEntryID("s-"+string(rune('a'+i))), "stock-s", "fan", entity.LotteryEntryStateEntered, 2, "pi"))
			aEntries = append(aEntries, lotteryEntry(entity.LotteryEntryID("a-"+string(rune('a'+i))), "stock-a", "fan", entity.LotteryEntryStateEntered, 2, "pi"))
		}
		sale := f.closedSaleWith("sale-l", drawStock{stockWith("stock-s", 4, 0), sEntries}, drawStock{stockWith("stock-a", 4, 0), aEntries})
		flow := newReplayFlow()
		flow.stopAt = "persist outcome"

		_, err := f.uc.Draw(ctx, flow, sale.ID)
		require.ErrorIs(t, err, errStopped)
		for _, stock := range []entity.TicketStockID{"stock-s", "stock-a"} {
			raw, ok := flow.recorded["order "+string(stock)]
			require.True(t, ok, "the order of %s is recorded", stock)
			require.NotEmpty(t, raw)
		}
		// The order a fresh computation would give now differs.
		fresh := entity.ShuffleDrawOrder(drawEntries(sEntries), mrand.New(mrand.NewPCG(draws+1, draws+1)))

		flow.stopAt = ""
		var out []entity.StockDrawOrder
		f.recordsOutcome(sale.ID, &out)
		_, err = f.uc.Draw(ctx, flow, sale.ID)

		require.NoError(t, err)
		require.Len(t, out, 2)
		assert.Equal(t, uint64(2), draws, "no new order is computed on the resumed run")
		assert.NotEqual(t, fresh, out[0].Entries, "the recorded order, not a new one")
		for _, o := range out {
			assert.Len(t, o.Entries, 8)
		}
	})

	t.Run("unknown sale", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, afterWindow, 1)
		f.sales.EXPECT().Get(mock.Anything, entity.TicketSaleID("missing")).Return(nil, apperr.ErrNotFound)

		_, err := f.uc.Draw(ctx, &inlineFlow{}, "missing")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestLotteryEntryUseCase_DrawDueSales(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	sale := func(id entity.TicketSaleID) *entity.TicketSale {
		s := lotterySale(entity.VerificationRequirementNone)
		s.ID = id
		return s
	}

	t.Run("window closes", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/draw-due-sales "Window closes"
		f := newLotteryFixture(t, afterWindow, 1)
		f.sales.EXPECT().ListDueForDraw(mock.Anything, afterWindow).Return([]*entity.TicketSale{sale("sale-l")}, nil)
		flow := &inlineFlow{}

		n, err := f.uc.DrawDueSales(ctx, flow, afterWindow)

		require.NoError(t, err)
		assert.Equal(t, 1, n)
		assert.Equal(t, []string{usecase.DrawWorkflowID("sale-l")}, flow.started, "the sale's draw starts in this run")
	})

	t.Run("window still open", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/draw-due-sales "Window still open"
		// TicketSale.ListDueForDraw lists only sales whose end time has passed.
		f := newLotteryFixture(t, duringWindow, 1)
		f.sales.EXPECT().ListDueForDraw(mock.Anything, duringWindow).Return([]*entity.TicketSale{}, nil)
		flow := &inlineFlow{}

		_, err := f.uc.DrawDueSales(ctx, flow, duringWindow)

		require.NoError(t, err)
		assert.Empty(t, flow.started)
	})

	t.Run("drawn once", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/draw-due-sales "Drawn once"
		// A sale listed again while its draw runs is started under the same
		// name, which starts no second run; a drawn sale is no longer listed.
		f := newLotteryFixture(t, afterWindow, 1)
		f.sales.EXPECT().ListDueForDraw(mock.Anything, afterWindow).Return([]*entity.TicketSale{sale("sale-l")}, nil).Twice()
		f.sales.EXPECT().ListDueForDraw(mock.Anything, afterWindow.Add(2*60e9)).Return([]*entity.TicketSale{}, nil).Once()
		first, second, third := &inlineFlow{}, &inlineFlow{}, &inlineFlow{}

		_, err := f.uc.DrawDueSales(ctx, first, afterWindow)
		require.NoError(t, err)
		_, err = f.uc.DrawDueSales(ctx, second, afterWindow)
		require.NoError(t, err)
		_, err = f.uc.DrawDueSales(ctx, third, afterWindow.Add(2*60e9))
		require.NoError(t, err)

		assert.Equal(t, first.started, second.started, "the same workflow name")
		assert.Empty(t, third.started)
	})

	t.Run("one sale fails", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/draw-due-sales "One sale fails"
		// Each sale is drawn by its own workflow: a failing draw ends only its
		// own run, which is reported to an admin, and is not started again
		// under its name.
		f := newLotteryFixture(t, afterWindow, 1)
		f.sales.EXPECT().ListDueForDraw(mock.Anything, afterWindow).Return([]*entity.TicketSale{sale("sale-failing"), sale("sale-other")}, nil)
		flow := &inlineFlow{}

		_, err := f.uc.DrawDueSales(ctx, flow, afterWindow)

		require.NoError(t, err)
		assert.Equal(t, []string{usecase.DrawWorkflowID("sale-failing"), usecase.DrawWorkflowID("sale-other")}, flow.started)
	})

	t.Run("listing fails", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, afterWindow, 1)
		listErr := apperr.New(codes.Internal, "db down")
		f.sales.EXPECT().ListDueForDraw(mock.Anything, afterWindow).Return(nil, listErr)

		_, err := f.uc.DrawDueSales(ctx, &inlineFlow{}, afterWindow)

		assert.ErrorIs(t, err, listErr)
	})
}
