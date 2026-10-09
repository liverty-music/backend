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

// countingCardHold is a ReservationAuthorizationPort that counts every
// capture call: a charged Reservation must never be captured again, so the
// count must stay at one. Capture is slow, to widen the window for a racing
// caller.
type countingCardHold struct {
	captures atomic.Int32
}

func (c *countingCardHold) CreateAuthorization(context.Context, int64, entity.AuthorizationMetadata) (string, string, error) {
	return "pi_conc", "secret", nil
}

func (c *countingCardHold) VerifyAuthorization(context.Context, string, int64) error { return nil }

func (c *countingCardHold) CaptureAuthorization(_ context.Context, ref string) (*entity.CapturedPayment, error) {
	c.captures.Add(1)
	time.Sleep(100 * time.Millisecond)
	return &entity.CapturedPayment{Provider: entity.PaymentProviderStripe, PaymentIntentRef: ref, AmountJPY: 6000, Currency: "JPY", CardBrand: "visa", CardLast4: "4242"}, nil
}

func (c *countingCardHold) CancelAuthorization(context.Context, string) error { return nil }

// newCheckoutIssuance wires IssuanceUseCase on the real repositories and the
// counting card hold, at a fixed time.
func newCheckoutIssuance(t *testing.T, cardHold entity.ReservationAuthorizationPort, now time.Time) usecase.IssuanceUseCase {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return usecase.NewIssuanceUseCase(usecase.IssuanceDeps{
		IssuanceRepo:       rdb.NewIssuanceRepository(testDB),
		OrderRepo:          rdb.NewOrderRepository(testDB),
		EventOrganizerRepo: rdb.NewEventOrganizerRepository(testDB),
		OrganizerRepo:      rdb.NewOrganizerRepository(testDB),
		ReservationRepo:    rdb.NewReservationRepository(testDB),
		TicketSaleRepo:     rdb.NewTicketSaleRepository(testDB),
		EventState:         rdb.NewEventPublishStateRepository(testDB),
		ReservationAuth:    cardHold,
		Clock:              func() time.Time { return now },
		Logger:             logger,
	})
}

// TestIssueFromReservation_Concurrency places the same order from two callers
// at once against Postgres: the card is charged once and both callers end
// with the same Order.
func TestIssueFromReservation_Concurrency(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	reservations := rdb.NewReservationRepository(testDB)
	orders := rdb.NewOrderRepository(testDB)
	start := saleWindowStart.Add(time.Hour)

	authorized := func(t *testing.T) *entity.Reservation {
		t.Helper()
		res := heldReservation(t, reservations, 2, start)
		require.NoError(t, reservations.SetAuthorization(ctx, res.ID, holderIdentity, "pi_"+string(res.ID)))
		return res
	}

	t.Run("double tap on the action", func(t *testing.T) {
		// @spec components/usecase/order/issue-from-reservation "Double tap on the action"
		res := authorized(t)
		cardHold := &countingCardHold{}
		uc := newCheckoutIssuance(t, cardHold, start.Add(5*time.Minute))
		fan := res.UserID

		var wg sync.WaitGroup
		got := make([]*entity.Order, 2)
		errs := make([]error, 2)
		for i := range 2 {
			wg.Go(func() { got[i], errs[i] = uc.IssueFromReservation(ctx, res.ID, &fan) })
		}
		wg.Wait()

		require.NoError(t, errs[0])
		require.NoError(t, errs[1])
		assert.Equal(t, got[0].ID, got[1].ID)
		assert.Equal(t, int32(1), cardHold.captures.Load(), "the card is captured once")
		stored, err := orders.GetByReservationID(ctx, res.ID)
		require.NoError(t, err)
		assert.Equal(t, got[0].ID, stored.ID)
		completed, err := reservations.Get(ctx, res.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ReservationStatusCompleted, completed.Status)
	})

	t.Run("fan and the stalled-checkout job at once", func(t *testing.T) {
		// @spec components/usecase/order/issue-from-reservation "Fan and the stalled-checkout job at once"
		res := authorized(t)
		// The fan's first attempt committed but its capture never completed.
		_, err := reservations.Commit(ctx, res.ID, start.Add(2*time.Minute))
		require.NoError(t, err)
		cardHold := &countingCardHold{}
		uc := newCheckoutIssuance(t, cardHold, start.Add(5*time.Minute))
		fan := res.UserID

		var wg sync.WaitGroup
		var fanOrder *entity.Order
		var fanErr, jobErr error
		wg.Go(func() { fanOrder, fanErr = uc.IssueFromReservation(ctx, res.ID, &fan) })
		wg.Go(func() { jobErr = uc.IssueDueReservations(ctx) })
		wg.Wait()

		require.NoError(t, fanErr)
		require.NoError(t, jobErr)
		assert.Equal(t, int32(1), cardHold.captures.Load(), "the card is captured once")
		stored, err := orders.GetByReservationID(ctx, res.ID)
		require.NoError(t, err)
		assert.Equal(t, fanOrder.ID, stored.ID, "both end with the same Order")
	})
}
