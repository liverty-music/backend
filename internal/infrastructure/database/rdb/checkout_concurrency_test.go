package rdb_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingCardHold is a CardAuthorizationRepository that counts every
// capture call: a charged Checkout must never be captured again, so the
// count must stay at one. Capture is slow, to widen the window for a racing
// caller. Get reports the card authorization Authorized for 6000 yen until it
// is captured, then Captured with its payment.
type countingCardHold struct {
	captures atomic.Int32
}

func (c *countingCardHold) Create(context.Context, entity.CardAuthorizationPurchase, int64) (*entity.CardAuthorization, error) {
	return &entity.CardAuthorization{ID: "pi_conc", Amount: 6000, Status: entity.CardAuthorizationStatusPending, Secret: "secret"}, nil
}

func (c *countingCardHold) Get(_ context.Context, id string) (*entity.CardAuthorization, error) {
	if c.captures.Load() == 0 {
		return &entity.CardAuthorization{ID: id, Amount: 6000, CardBrand: "visa", Status: entity.CardAuthorizationStatusAuthorized}, nil
	}
	return &entity.CardAuthorization{ID: id, Amount: 6000, CardBrand: "visa", Status: entity.CardAuthorizationStatusCaptured,
		CapturedPayment: &entity.CapturedPayment{Provider: entity.PaymentProviderStripe, ProviderPaymentID: id, AmountJPY: 6000, Currency: "JPY", CardBrand: "visa", CardLast4: "4242"}}, nil
}

func (c *countingCardHold) Capture(context.Context, string) error {
	c.captures.Add(1)
	time.Sleep(100 * time.Millisecond)
	return nil
}

func (c *countingCardHold) Release(context.Context, string) error { return nil }

// newCheckoutIssuance wires OrderUseCase on the real repositories and the
// counting card hold, at a fixed time.
func newCheckoutIssuance(t *testing.T, cardHold *countingCardHold, now time.Time) usecase.OrderUseCase {
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
		CardAuthorizations: cardHold,
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
		cardHold := &countingCardHold{}
		uc := newCheckoutIssuance(t, cardHold, start.Add(5*time.Minute))
		fan := c.UserID

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
		assert.Equal(t, int32(1), cardHold.captures.Load(), "the card is captured once")
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
		cardHold := &countingCardHold{}
		uc := newCheckoutIssuance(t, cardHold, start.Add(5*time.Minute))
		fan := c.UserID

		var wg sync.WaitGroup
		var fanOrder *entity.Order
		var fanErr, jobErr error
		wg.Go(func() { fanOrder, fanErr = uc.IssueFromCheckout(ctx, c.ID, &fan) })
		wg.Go(func() { jobErr = uc.IssueDueCheckouts(ctx) })
		wg.Wait()

		require.NoError(t, fanErr)
		require.NoError(t, jobErr)
		assert.Equal(t, int32(1), cardHold.captures.Load(), "the card is captured once")
		stored, err := orders.GetByCheckoutID(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, fanOrder.ID, stored.ID, "both end with the same Order")
	})
}
