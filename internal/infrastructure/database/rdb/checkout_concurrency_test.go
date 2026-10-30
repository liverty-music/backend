package rdb_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingCardAuthorizations is a CardAuthorizationRepository that counts every
// capture call: a charged Checkout must never be captured again, so the
// count must stay at one. Capture is slow, to widen the window for a racing
// caller. Get reports the card authorization Authorized for 6000 yen until it
// is captured, then Captured with its payment.
type countingCardAuthorizations struct {
	captures atomic.Int32
}

func (c *countingCardAuthorizations) Create(context.Context, entity.CardAuthorizationPurchase, int64) (*entity.CardAuthorization, error) {
	return &entity.CardAuthorization{ID: "pi_conc", Amount: 6000, Status: entity.CardAuthorizationStatusPending, Secret: "secret"}, nil
}

func (c *countingCardAuthorizations) Get(_ context.Context, id string) (*entity.CardAuthorization, error) {
	if c.captures.Load() == 0 {
		return &entity.CardAuthorization{ID: id, Amount: 6000, CardBrand: "visa", Status: entity.CardAuthorizationStatusAuthorized}, nil
	}
	return &entity.CardAuthorization{ID: id, Amount: 6000, CardBrand: "visa", Status: entity.CardAuthorizationStatusCaptured,
		CapturedPayment: &entity.CapturedPayment{Provider: entity.PaymentProviderStripe, ProviderPaymentID: id, AmountJPY: 6000, Currency: "JPY", CardBrand: "visa", CardLast4: "4242"}}, nil
}

func (c *countingCardAuthorizations) Capture(context.Context, string) error {
	c.captures.Add(1)
	time.Sleep(100 * time.Millisecond)
	return nil
}

func (c *countingCardAuthorizations) Release(context.Context, string) error { return nil }

// newCheckoutIssuance wires OrderUseCase on the real repositories and the
// counting card authorization repository, at a fixed time.
func newCheckoutIssuance(t *testing.T, cardAuthorizations *countingCardAuthorizations, now time.Time) usecase.OrderUseCase {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return usecase.NewOrderUseCase(usecase.OrderDeps{
		IssuanceRepo:       rdb.NewIssuanceRepository(testDB),
		OrderRepo:          orderRepository(t, rdb.NewOrderRepository(testDB)),
		EventOrganizerRepo: rdb.NewEventOrganizerRepository(testDB),
		OrganizerRepo:      rdb.NewOrganizerRepository(testDB),
		CheckoutRepo:       rdb.NewCheckoutRepository(testDB),
		TicketSaleRepo:     rdb.NewTicketSaleRepository(testDB),
		EventState:         rdb.NewEventPublishStateRepository(testDB),
		CardAuthorizations: cardAuthorizations,
		Clock:              func() time.Time { return now },
		Logger:             logger,
	})
}

// TestIssueFromCheckout_Concurrency places the same order from two callers
// at once against Postgres: the card is charged once and both callers end
// with the same Order.
func TestIssueFromCheckout_Concurrency(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	checkouts := rdb.NewCheckoutRepository(testDB)
	orders := rdb.NewOrderRepository(testDB)
	start := saleWindowStart.Add(time.Hour)

	authorized := func(t *testing.T) *entity.Checkout {
		t.Helper()
		c := heldCheckout(t, checkouts, 2, start)
		require.NoError(t, checkouts.SetCardAuthorization(ctx, c.ID, "pi_"+string(c.ID)))
		return c
	}

	t.Run("double tap on the action", func(t *testing.T) {
		// @spec components/usecase/order/issue-from-checkout "Double tap on the action"
		c := authorized(t)
		cardAuthorizations := &countingCardAuthorizations{}
		uc := newCheckoutIssuance(t, cardAuthorizations, start.Add(5*time.Minute))
		fan := c.Order.UserID

		var wg sync.WaitGroup
		got := make([]*entity.Order, 2)
		errs := make([]error, 2)
		for i := range 2 {
			wg.Go(func() { got[i], errs[i] = uc.IssueFromCheckout(ctx, c.ID, &fan) })
		}
		wg.Wait()

		require.NoError(t, errs[0])
		require.NoError(t, errs[1])
		assert.Equal(t, got[0].ID, got[1].ID)
		assert.Equal(t, int32(1), cardAuthorizations.captures.Load(), "the card is captured once")
		stored, err := orders.GetByCheckoutID(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, got[0].ID, stored.ID)
		completed, err := checkouts.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusCompleted, completed.Status)
	})

	t.Run("fan and the stalled-checkout job at once", func(t *testing.T) {
		// @spec components/usecase/order/issue-from-checkout "Fan and the stalled-checkout job at once"
		c := authorized(t)
		// The fan's first attempt committed but its capture never completed.
		_, err := checkouts.Commit(ctx, c.ID, start.Add(2*time.Minute))
		require.NoError(t, err)
		cardAuthorizations := &countingCardAuthorizations{}
		uc := newCheckoutIssuance(t, cardAuthorizations, start.Add(5*time.Minute))
		fan := c.Order.UserID

		var wg sync.WaitGroup
		var fanOrder *entity.Order
		var fanErr, jobErr error
		wg.Go(func() { fanOrder, fanErr = uc.IssueFromCheckout(ctx, c.ID, &fan) })
		wg.Go(func() { jobErr = uc.IssueDueCheckouts(ctx) })
		wg.Wait()

		require.NoError(t, fanErr)
		require.NoError(t, jobErr)
		assert.Equal(t, int32(1), cardAuthorizations.captures.Load(), "the card is captured once")
		stored, err := orders.GetByCheckoutID(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, fanOrder.ID, stored.ID, "both end with the same Order")
	})
}

// TestCheckoutRepository_GetOrCreateHeld_TwoStocksAtOnce runs two starts of
// one fan on two stocks of one event at the same time: the users row lock
// serializes them, so the fan never holds more than the event's limit.
func TestCheckoutRepository_GetOrCreateHeld_TwoStocksAtOnce(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewCheckoutRepository(testDB)
	orders := rdb.NewOrderRepository(testDB)
	at := saleWindowStart.Add(time.Hour)

	// @spec components/entity/checkout/get-or-create-held "Two stocks at once"
	for range 10 {
		sSeats := seedStock(t, entity.TicketSaleMethodFirstCome, 150, 4)
		aSeats := seedStockForEvent(t, sSeats.EventID, entity.TicketSaleMethodFirstCome, 150, 4)
		fan := nextFan(t)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, f := range []*stockFixture{sSeats, aSeats} {
			wg.Go(func() { _, errs[i] = repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 3, nil, at) })
		}
		wg.Wait()
		cancel()

		created := 0
		for _, err := range errs {
			if err == nil {
				created++
				continue
			}
			assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		}
		assert.Equal(t, 1, created, "at most one of the calls creates a checkout")
		held, err := orders.GetHeldForEvent(context.Background(), fan, sSeats.EventID, nil, at)
		require.NoError(t, err)
		assert.Equal(t, 3, held.Count)
		assert.LessOrEqual(t, held.Count, sSeats.Limit)
	}
}

// stockOrderCounts sums the ticket counts of the stock's Orders the way
// design D5 defines the counts, from the rows themselves: sold counts Paid
// and Refunded Orders and Pending Orders of a Committed Checkout or a Won
// entry; held counts Pending Orders of a Checkout holding at at.
func stockOrderCounts(t *testing.T, id entity.TicketStockID, at time.Time) (sold, held int) {
	t.Helper()
	require.NoError(t, testDB.Pool.QueryRow(context.Background(), `
		SELECT
			COALESCE(SUM(o.ticket_count) FILTER (WHERE o.status IN (1, 2) OR (o.status = 4 AND (c.status = 2 OR le.state = 2))), 0)::int,
			COALESCE(SUM(o.ticket_count) FILTER (WHERE o.status = 4 AND c.status = 1 AND c.hold_expire_at > $2), 0)::int
		FROM orders o
		LEFT JOIN checkouts c ON c.order_id = o.id
		LEFT JOIN lottery_entries le ON le.order_id = o.id
		WHERE o.ticket_stock_id = $1`, string(id), at).Scan(&sold, &held))
	return sold, held
}

// TestTicketStock_ConcurrentPaths runs, on one TicketStock, every path that
// changes its tickets at the same time (design D5): two fans starting a
// checkout for the last tickets, the commit of a holding checkout, the revert
// of a committed one and the outcome of a lottery draw of the stock. Each
// takes the stock's row lock and reads the counts after it, so the sold plus
// held count never exceeds the quantity, no call deadlocks, and the counts
// read afterwards equal the Orders' ticket counts.
//
// The draw records its outcome without checking the remaining count: it was
// read when the draw ran, and CheckoutUseCase.Start refuses a checkout while
// a closed lottery of the stock still has Entered entries (design D6). The
// starts therefore ask for 6, which leaves the winner's 2 tickets free
// before and after the draw; only one of the two fits, whether or not the
// revert has given 2 tickets back.
func TestTicketStock_ConcurrentPaths(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	checkouts := rdb.NewCheckoutRepository(testDB)
	entries := rdb.NewLotteryEntryRepository(testDB)
	stocks := rdb.NewTicketStockRepository(testDB)
	at := saleWindowStart.Add(time.Hour)
	const quantity, rounds = 12, 20

	for round := range rounds {
		setup := context.Background()
		f := seedStock(t, entity.TicketSaleMethodFirstCome, quantity, 10)
		lottery := seedSale(t, f.EventID, entity.TicketSaleMethodLottery,
			saleWindowStart.Add(-10*24*time.Hour), saleWindowStart.Add(-time.Hour))
		toCommit, err := checkouts.GetOrCreateHeld(setup, f.ID, f.SaleID, nextFan(t), 2, nil, at)
		require.NoError(t, err)
		toRevert, err := checkouts.GetOrCreateHeld(setup, f.ID, f.SaleID, nextFan(t), 2, nil, at)
		require.NoError(t, err)
		_, err = checkouts.Commit(setup, toRevert.ID, at)
		require.NoError(t, err)
		winner := seedLotteryEntryInSale(t, f, lottery, seedFanUser(t), entity.LotteryEntryStateEntered, 2)
		loser := seedLotteryEntryInSale(t, f, lottery, seedFanUser(t), entity.LotteryEntryStateEntered, 2)
		fans := []entity.UserID{nextFan(t), nextFan(t)}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		start := make(chan struct{})
		var wg sync.WaitGroup
		holdErrs := make([]error, len(fans))
		var commitOutcome entity.CommitOutcome
		var commitErr, revertErr, drawErr error
		for i, fan := range fans {
			wg.Go(func() {
				<-start
				_, holdErrs[i] = checkouts.GetOrCreateHeld(ctx, f.ID, f.SaleID, fan, 6, nil, at)
			})
		}
		wg.Go(func() {
			<-start
			commitOutcome, commitErr = checkouts.Commit(ctx, toCommit.ID, at.Add(time.Minute))
		})
		wg.Go(func() {
			<-start
			revertErr = checkouts.RevertCommit(ctx, toRevert.ID)
		})
		wg.Go(func() {
			<-start
			drawErr = entries.PersistDrawOutcome(ctx, lottery,
				[]entity.DrawWinnerRow{{LotteryEntryID: winner, CardAuthorizationID: "pi_" + string(winner), DrawPosition: 0}},
				[]entity.DrawLoserRow{{LotteryEntryID: loser, CardAuthorizationID: "pi_" + string(loser), DrawPosition: 1, LostReason: entity.LotteryEntryLostReasonNotDrawn}})
		})
		// A reader samples the counts while the paths run; each read is one
		// statement, so it sees a consistent snapshot.
		done := make(chan struct{})
		var sampleErr error
		var samples sync.WaitGroup
		samples.Go(func() {
			<-start
			for {
				select {
				case <-done:
					return
				default:
				}
				s, err := stocks.Get(ctx, f.ID, at.Add(time.Minute))
				if err != nil {
					sampleErr = err
					return
				}
				if s.SoldCount+s.HeldCount > quantity {
					sampleErr = fmt.Errorf("round %d: sold %d + held %d exceeds %d", round, s.SoldCount, s.HeldCount, quantity)
					return
				}
			}
		})
		close(start)
		wg.Wait()
		close(done)
		samples.Wait()
		cancel()

		require.NoError(t, commitErr, "round %d: commit", round)
		assert.Equal(t, entity.CommitOutcomeCommitted, commitOutcome)
		require.NoError(t, revertErr, "round %d: revert", round)
		require.NoError(t, drawErr, "round %d: draw outcome", round)
		require.NoError(t, sampleErr)
		held := 0
		for _, err := range holdErrs {
			if err == nil {
				held++
				continue
			}
			assert.ErrorIs(t, err, apperr.ErrResourceExhausted, "round %d", round)
		}
		assert.Equal(t, 1, held, "round %d: exactly one fan gets the last tickets", round)

		after := at.Add(time.Minute)
		s, err := stocks.Get(context.Background(), f.ID, after)
		require.NoError(t, err)
		sold, heldCount := stockOrderCounts(t, f.ID, after)
		assert.Equal(t, sold, s.SoldCount, "round %d: sold count equals the Orders'", round)
		assert.Equal(t, heldCount, s.HeldCount, "round %d: held count equals the Orders'", round)
		// Committed 2 and won 2 are sold; the reverted 2 are not; the fan
		// that got the last tickets holds 6.
		assert.Equal(t, 4, s.SoldCount, "round %d", round)
		assert.Equal(t, 6, s.HeldCount, "round %d", round)
		assert.LessOrEqual(t, s.SoldCount+s.HeldCount, quantity)
		assert.Equal(t, entity.OrderStatusVoid, orderStatusOf(t, toRevert.OrderID))
		assert.Equal(t, entity.OrderStatusVoid, orderStatusOf(t, entryOrderID(t, loser)))
		assert.Equal(t, entity.OrderStatusPending, orderStatusOf(t, entryOrderID(t, winner)))
	}
}
