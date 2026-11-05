package usecase_test

import (
	"context"
	mrand "math/rand/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

var (
	// reDrawAt is a daily draw, 12:00 on 6 November, before the fixtures'
	// event on 10 November.
	reDrawAt = time.Date(2026, 11, 6, 12, 0, 0, 0, reJST)
	// reLastDraw is the event's last draw, 12:00 on its day.
	reLastDraw = time.Date(2026, 11, 10, 12, 0, 0, 0, reJST)
)

// newDrawFixture builds the use case with a fixed draw order source.
func newDrawFixture(t *testing.T) *resaleEntryFixture {
	t.Helper()
	f := newResaleEntryFixture(t)
	f.uc = usecase.NewResaleEntryUseCaseWithRand(usecase.ResaleEntryDeps{
		EntryRepo: f.entries, StockRepo: f.stocks, SaleRepo: f.sales, OrderRepo: f.orders, EventRepo: f.events,
		EventState: f.eventState, IdentityVerificationRepo: f.identityVerification, UserRepo: f.users,
		CardAuthorizations: f.cardAuthorizations, Clock: fixedClock(reNow), Logger: newTestLogger(t),
	}, func() *mrand.Rand { return mrand.New(mrand.NewPCG(1, 2)) })
	return f
}

// candidate is an Entered entry for count tickets whose card authorization
// expires at expire.
func candidate(id entity.ResaleEntryID, count int, expire time.Time) *entity.ResaleEntry {
	return &entity.ResaleEntry{ID: id, State: entity.ResaleEntryStateEntered, CardAuthorizationExpireTime: &expire,
		Order: &entity.Order{ID: entity.OrderID("order-" + id), TicketCount: count, Status: entity.OrderStatusPending}}
}

// expectDraw stubs the stock's draw at at with the candidates and free
// listings and returns the outcome the use case decided.
func expectDraw(f *resaleEntryFixture, at time.Time, published bool, entries []*entity.ResaleEntry, free []entity.ResaleListingID) *entity.ResaleDrawOutcome {
	var decided entity.ResaleDrawOutcome
	f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-s"), at).Return(soldOutStock("stock-s", reSeatS, true), nil)
	f.events.EXPECT().Get(mock.Anything, "event-1").Return(resaleEvent(), nil)
	f.eventState.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(published, nil)
	f.entries.EXPECT().DrawUnderLock(mock.Anything, entity.TicketStockID("stock-s"), at, mock.Anything).
		RunAndReturn(func(_ context.Context, _ entity.TicketStockID, _ time.Time, decide func([]*entity.ResaleEntry, []entity.ResaleListingID) entity.ResaleDrawOutcome) (entity.ResaleDrawOutcome, error) {
			decided = decide(entries, free)
			return decided, nil
		})
	return &decided
}

func TestResaleEntryUseCase_DrawStock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	week := reDrawAt.Add(7 * 24 * time.Hour)
	in := func(at time.Time) usecase.ResaleStockDraw { return usecase.ResaleStockDraw{StockID: "stock-s", At: at} }

	t.Run("more applications than listings", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-entry/draw "More applications than listings"
		f := newDrawFixture(t)
		a, b, c := candidate("a", 2, week), candidate("b", 2, week), candidate("c", 1, week)
		decided := expectDraw(f, reDrawAt, true, []*entity.ResaleEntry{a, b, c}, []entity.ResaleListingID{"l1", "l2", "l3"})
		flow := &inlineFlow{}

		_, err := f.uc.DrawStock(ctx, flow, in(reDrawAt))

		require.NoError(t, err)
		allocated := 0
		for _, w := range decided.Winners {
			assert.Len(t, w.Listings, w.Entry.Order.TicketCount, "all or nothing")
			allocated += len(w.Listings)
		}
		assert.LessOrEqual(t, allocated, 3)
		assert.Len(t, decided.Winners, 2, "whatever the order, one entry for 2 and the entry for 1 fit")
		assert.Empty(t, decided.Lost, "not the last draw: the rest stays Entered")
		assert.Empty(t, flow.started, "the draw starts no other workflow")
	})

	t.Run("card authorization about to lapse", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-entry/draw "Card authorization about to lapse"
		f := newDrawFixture(t)
		e := candidate("a", 1, time.Date(2026, 11, 7, 9, 0, 0, 0, reJST))
		decided := expectDraw(f, reDrawAt, true, []*entity.ResaleEntry{e}, []entity.ResaleListingID{"l1"})

		_, err := f.uc.DrawStock(ctx, &inlineFlow{}, in(reDrawAt))

		require.NoError(t, err)
		assert.Equal(t, []*entity.ResaleEntry{e}, decided.Lapsed, "Lapsed; the release and the message follow its announcement")
		assert.Empty(t, decided.Winners)
	})

	t.Run("last draw", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-entry/draw "Last draw"
		f := newDrawFixture(t)
		a, b := candidate("a", 1, week), candidate("b", 2, week)
		f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-s"), reLastDraw).Return(soldOutStock("stock-s", reSeatS, true), nil)
		f.events.EXPECT().Get(mock.Anything, "event-1").Return(resaleEvent(), nil)
		f.eventState.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		var decided entity.ResaleDrawOutcome
		f.entries.EXPECT().DrawUnderLock(mock.Anything, entity.TicketStockID("stock-s"), reLastDraw, mock.Anything).
			RunAndReturn(func(_ context.Context, _ entity.TicketStockID, _ time.Time, decide func([]*entity.ResaleEntry, []entity.ResaleListingID) entity.ResaleDrawOutcome) (entity.ResaleDrawOutcome, error) {
				decided = decide([]*entity.ResaleEntry{a, b}, []entity.ResaleListingID{"l1"})
				return decided, nil
			})

		_, err := f.uc.DrawStock(ctx, &inlineFlow{}, in(reLastDraw))

		require.NoError(t, err)
		require.Len(t, decided.Winners, 1)
		assert.Same(t, a, decided.Winners[0].Entry, "only the entry for 1 fits the last listing")
		assert.Equal(t, []*entity.ResaleEntry{b}, decided.Lost)
		assert.Equal(t, entity.ResaleEntryLostReasonNoTicketLeft, decided.LostReason)
	})

	t.Run("no listing yet", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-entry/draw "No listing yet"
		f := newDrawFixture(t)
		decided := expectDraw(f, reDrawAt, true, []*entity.ResaleEntry{candidate("a", 1, week), candidate("b", 2, week)}, nil)

		_, err := f.uc.DrawStock(ctx, &inlineFlow{}, in(reDrawAt))

		require.NoError(t, err)
		assert.Empty(t, decided.Winners)
		assert.Empty(t, decided.Lost)
		assert.Empty(t, decided.Lapsed, "every candidate stays Entered")
	})

	t.Run("concert cancelled", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/resale-entry/draw "Concert cancelled"
		f := newDrawFixture(t)
		a, b := candidate("a", 1, week), candidate("b", 1, week)
		decided := expectDraw(f, reDrawAt, false, []*entity.ResaleEntry{a, b}, []entity.ResaleListingID{"l1", "l2"})

		_, err := f.uc.DrawStock(ctx, &inlineFlow{}, in(reDrawAt))

		require.NoError(t, err)
		assert.Empty(t, decided.Winners, "no listing is allocated")
		assert.ElementsMatch(t, []*entity.ResaleEntry{a, b}, decided.Lost)
		assert.Equal(t, entity.ResaleEntryLostReasonConcertCancelled, decided.LostReason)
	})

	t.Run("not a draw time of the event", func(t *testing.T) {
		t.Parallel()
		f := newDrawFixture(t)
		at := reDrawAt.Add(time.Minute)
		f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-s"), at).Return(soldOutStock("stock-s", reSeatS, true), nil)
		f.events.EXPECT().Get(mock.Anything, "event-1").Return(resaleEvent(), nil)

		n, err := f.uc.DrawStock(ctx, &inlineFlow{}, in(at))

		require.NoError(t, err)
		assert.Zero(t, n)
	})
}

func TestResaleEntryUseCase_DrawResale(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	start := reStart
	matinee := time.Date(2026, 11, 6, 14, 0, 0, 0, reJST)

	t.Run("each due stock is drawn in its own workflow", func(t *testing.T) {
		t.Parallel()
		f := newDrawFixture(t)
		f.entries.EXPECT().ListDueStocks(mock.Anything).Return([]entity.ResaleDrawStock{
			{StockID: "stock-s", EventID: "event-1", StartTime: &start},
			{StockID: "stock-a", EventID: "event-1", StartTime: &start},
			{StockID: "stock-m", EventID: "event-2", StartTime: &matinee},
		}, nil)
		flow := &inlineFlow{}

		n, err := f.uc.DrawResale(ctx, flow, reDrawAt)

		require.NoError(t, err)
		assert.Equal(t, 2, n, "the matinee on 6 November drew at 11:00, not 12:00")
		assert.Equal(t, []string{
			usecase.DrawResaleStockWorkflowID("stock-s", reDrawAt),
			usecase.DrawResaleStockWorkflowID("stock-a", reDrawAt),
		}, flow.started)
	})

	t.Run("a minute without a draw starts nothing", func(t *testing.T) {
		t.Parallel()
		f := newDrawFixture(t)
		f.entries.EXPECT().ListDueStocks(mock.Anything).Return([]entity.ResaleDrawStock{{StockID: "stock-s", EventID: "event-1", StartTime: &start}}, nil)
		flow := &inlineFlow{}

		_, err := f.uc.DrawResale(ctx, flow, reDrawAt.Add(time.Minute))

		require.NoError(t, err)
		assert.Empty(t, flow.started)
	})
}
