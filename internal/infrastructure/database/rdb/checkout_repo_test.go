package rdb_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFan seeds a user and returns its id; n keeps emails unique per test.
func newFan(t *testing.T, n string) entity.UserID {
	t.Helper()
	return entity.UserID(seedUser(t, "fan-"+n, "fan-"+n+"@example.com", "ext-fan-"+n))
}

// fanCounter makes fan names unique across the tests of this file.
var fanCounter struct {
	sync.Mutex
	n int
}

func nextFan(t *testing.T) entity.UserID {
	t.Helper()
	fanCounter.Lock()
	fanCounter.n++
	n := fanCounter.n
	fanCounter.Unlock()
	return newFan(t, fmt.Sprintf("checkout-%d", n))
}

// heldCount returns the ticket type's held count at at.
func heldCount(t *testing.T, id entity.TicketTypeID, at time.Time) int {
	t.Helper()
	_, tt, err := rdb.NewTicketSaleRepository(testDB).GetTicketType(context.Background(), id, at)
	require.NoError(t, err)
	return tt.HeldCount
}

// soldCount returns the ticket type's sold count.
func soldCount(t *testing.T, id entity.TicketTypeID) int {
	t.Helper()
	var sold int
	require.NoError(t, testDB.Pool.QueryRow(context.Background(),
		`SELECT sold_count FROM ticket_types WHERE id = $1`, string(id)).Scan(&sold))
	return sold
}

func TestCheckoutRepository_GetOrCreateHeld(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewCheckoutRepository(testDB)
	ctx := context.Background()
	at := saleWindowStart.Add(time.Hour)

	t.Run("first start", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "First start"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 150, 0)
		setTicketTypeSold(t, tt.ID, 106)

		c, err := repo.GetOrCreateHeld(ctx, tt.ID, nextFan(t), 2, at, nil)

		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusHeld, c.Status)
		assert.Equal(t, 2, c.TicketCount)
		assert.Equal(t, int64(6000), c.Amount)
		assert.Equal(t, at.Add(15*time.Minute), c.HoldExpireTime)
		assert.Equal(t, 2, heldCount(t, tt.ID, at))
	})

	t.Run("double tap", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Double tap"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 150, 0)
		fan := nextFan(t)

		var wg sync.WaitGroup
		results := make([]*entity.Checkout, 2)
		errs := make([]error, 2)
		for i := range 2 {
			wg.Go(func() { results[i], errs[i] = repo.GetOrCreateHeld(ctx, tt.ID, fan, 2, at, nil) })
		}
		wg.Wait()

		require.NoError(t, errs[0])
		require.NoError(t, errs[1])
		assert.Equal(t, results[0].ID, results[1].ID)
		assert.Equal(t, 2, heldCount(t, tt.ID, at))
	})

	t.Run("count changed", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Count changed"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 150, 0)
		fan := nextFan(t)
		first, err := repo.GetOrCreateHeld(ctx, tt.ID, fan, 2, at, nil)
		require.NoError(t, err)

		second, err := repo.GetOrCreateHeld(ctx, tt.ID, fan, 3, at.Add(time.Minute), nil)

		require.NoError(t, err)
		assert.NotEqual(t, first.ID, second.ID)
		assert.Equal(t, 3, second.TicketCount)
		replaced, err := repo.Get(ctx, first.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusReleased, replaced.Status)
	})

	t.Run("count raised when stock is short", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Count raised when stock is short"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 10, 4)
		setTicketTypeSold(t, tt.ID, 7)
		fan := nextFan(t)
		held, err := repo.GetOrCreateHeld(ctx, tt.ID, fan, 2, at, nil)
		require.NoError(t, err)

		_, err = repo.GetOrCreateHeld(ctx, tt.ID, fan, 4, at, nil)

		assert.ErrorIs(t, err, apperr.ErrResourceExhausted)
		still, err := repo.Get(ctx, held.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusHeld, still.Status)
	})

	t.Run("two fans, last ticket", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Two fans, last ticket"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 10, 0)
		setTicketTypeSold(t, tt.ID, 9)
		fans := []entity.UserID{nextFan(t), nextFan(t)}

		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := range 2 {
			wg.Go(func() { _, errs[i] = repo.GetOrCreateHeld(ctx, tt.ID, fans[i], 1, at, nil) })
		}
		wg.Wait()

		won := 0
		for _, err := range errs {
			if err == nil {
				won++
				continue
			}
			assert.ErrorIs(t, err, apperr.ErrResourceExhausted)
		}
		assert.Equal(t, 1, won)
		assert.Equal(t, 1, heldCount(t, tt.ID, at))
	})

	t.Run("over the per-account limit", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Over the per-account limit"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 150, 4)
		fan := nextFan(t)
		seedIssuedTickets(t, tt.EventID, string(fan), 3)

		_, err := repo.GetOrCreateHeld(ctx, tt.ID, fan, 2, at, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Equal(t, 0, heldCount(t, tt.ID, at), "nothing is held")
	})

	t.Run("presale winner in the general sale", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Presale winner in the general sale"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 150, 4)
		fan := nextFan(t)
		// seedIssuedTickets issues them through a lottery sale of the event.
		seedIssuedTickets(t, tt.EventID, string(fan), 4)

		_, err := repo.GetOrCreateHeld(ctx, tt.ID, fan, 1, at, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Equal(t, 0, heldCount(t, tt.ID, at), "nothing is held")
	})

	t.Run("charge still being issued", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Charge still being issued"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 150, 4)
		fan := nextFan(t)
		bought, err := repo.GetOrCreateHeld(ctx, tt.ID, fan, 4, at, nil)
		require.NoError(t, err)
		outcome, err := repo.Commit(ctx, bought.ID, at)
		require.NoError(t, err)
		require.Equal(t, entity.CommitOutcomeCommitted, outcome)

		_, err = repo.GetOrCreateHeld(ctx, tt.ID, fan, 1, at, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Equal(t, 0, heldCount(t, tt.ID, at), "nothing is held")
	})

	t.Run("issued checkout counts once", func(t *testing.T) {
		// A Completed checkout's tickets count as Issued tickets only.
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 150, 4)
		c := completedCheckoutOf(t, repo, rdb.NewIssuanceRepository(testDB), tt, nextFan(t), 2, at)

		_, err := repo.GetOrCreateHeld(ctx, tt.ID, c.UserID, 2, at, nil)

		assert.NoError(t, err)
	})

	t.Run("a lapsed hold is expired when the fan starts again", func(t *testing.T) {
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 150, 0)
		fan := nextFan(t)
		lapsed, err := repo.GetOrCreateHeld(ctx, tt.ID, fan, 2, at, nil)
		require.NoError(t, err)

		fresh, err := repo.GetOrCreateHeld(ctx, tt.ID, fan, 2, at.Add(16*time.Minute), nil)

		require.NoError(t, err)
		assert.NotEqual(t, lapsed.ID, fresh.ID)
		old, err := repo.Get(ctx, lapsed.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusExpired, old.Status)
	})

	t.Run("unknown ticket type", func(t *testing.T) {
		// @spec components/entity/checkout/get-or-create-held "Unknown ticket type"
		_, err := repo.GetOrCreateHeld(ctx, entity.TicketTypeID(entity.NewID()), nextFan(t), 1, at, nil)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

// heldCheckout starts a checkout for count tickets on a fresh ticket type.
func heldCheckout(t *testing.T, repo *rdb.CheckoutRepository, count int, at time.Time) *entity.Checkout {
	t.Helper()
	tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 150, 10)
	c, err := repo.GetOrCreateHeld(context.Background(), tt.ID, nextFan(t), count, at, nil)
	require.NoError(t, err)
	return c
}

func TestCheckoutRepository_Lifecycle(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewCheckoutRepository(testDB)
	issuance := rdb.NewIssuanceRepository(testDB)
	ctx := context.Background()
	start := saleWindowStart.Add(time.Hour)
	expiry := start.Add(15 * time.Minute)

	t.Run("get", func(t *testing.T) {
		// @spec components/entity/checkout/get "Unknown checkout"
		_, err := repo.Get(ctx, entity.CheckoutID(entity.NewID()))
		assert.ErrorIs(t, err, apperr.ErrNotFound)

		// @spec components/entity/checkout/get "Completed checkout"
		c := completedCheckout(t, repo, issuance, start)
		got, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusCompleted, got.Status)
	})

	t.Run("set authorization", func(t *testing.T) {
		c := heldCheckout(t, repo, 2, start)

		// @spec components/entity/checkout/set-card-authorization "First card authorization"
		require.NoError(t, repo.SetCardAuthorization(ctx, c.ID, "pi_auth_1"))
		got, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, "pi_auth_1", got.CardAuthorizationID)
		want := *c
		want.CardAuthorizationID = "pi_auth_1"
		assert.Equal(t, want.Status, got.Status)
		assert.Equal(t, want.TicketCount, got.TicketCount)
		assert.Equal(t, want.Amount, got.Amount)
		assert.True(t, want.HoldExpireTime.Equal(got.HoldExpireTime))

		// @spec components/entity/checkout/set-card-authorization "Same card authorization on retry"
		require.NoError(t, repo.SetCardAuthorization(ctx, c.ID, "pi_auth_1"))
		got, err = repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, "pi_auth_1", got.CardAuthorizationID)

		// @spec components/entity/checkout/set-card-authorization "Another card authorization"
		assert.ErrorIs(t, repo.SetCardAuthorization(ctx, c.ID, "pi_other"), apperr.ErrFailedPrecondition)
		got, err = repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, "pi_auth_1", got.CardAuthorizationID, "nothing changes")

		// @spec components/entity/checkout/set-card-authorization "Expired checkout"
		lapsed := heldCheckout(t, repo, 1, start)
		_, err = repo.Release(ctx, lapsed.ID, expiry)
		require.NoError(t, err)
		assert.ErrorIs(t, repo.SetCardAuthorization(ctx, lapsed.ID, "pi_x"), apperr.ErrFailedPrecondition)

		assert.ErrorIs(t, repo.SetCardAuthorization(ctx, entity.CheckoutID(entity.NewID()), "pi_y"), apperr.ErrNotFound)
	})

	t.Run("commit", func(t *testing.T) {
		// @spec components/entity/checkout/commit "Within the hold"
		c := heldCheckout(t, repo, 2, start)
		outcome, err := repo.Commit(ctx, c.ID, start.Add(5*time.Minute))
		require.NoError(t, err)
		assert.Equal(t, entity.CommitOutcomeCommitted, outcome)
		got, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusCommitted, got.Status)
		assert.Equal(t, start.Add(5*time.Minute), got.CommitTime.UTC())
		assert.Equal(t, 2, soldCount(t, c.TicketTypeID))
		assert.Equal(t, 0, heldCount(t, c.TicketTypeID, start))

		// @spec components/entity/checkout/commit "Repeated commit"
		outcome, err = repo.Commit(ctx, c.ID, start.Add(6*time.Minute))
		require.NoError(t, err)
		assert.Equal(t, entity.CommitOutcomeCommitted, outcome)
		assert.Equal(t, 2, soldCount(t, c.TicketTypeID), "a repeated commit adds nothing")

		// @spec components/entity/checkout/commit "Hold lapsed"
		lapsed := heldCheckout(t, repo, 2, start)
		outcome, err = repo.Commit(ctx, lapsed.ID, expiry)
		require.NoError(t, err)
		assert.Equal(t, entity.CommitOutcomeNotHeld, outcome)
		got, err = repo.Get(ctx, lapsed.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusHeld, got.Status)
	})

	t.Run("release", func(t *testing.T) {
		// @spec components/entity/checkout/release "Still holding"
		c := heldCheckout(t, repo, 2, start)
		changed, err := repo.Release(ctx, c.ID, expiry.Add(-time.Minute))
		require.NoError(t, err)
		assert.False(t, changed)

		// @spec components/entity/checkout/release "Hold lapsed"
		changed, err = repo.Release(ctx, c.ID, expiry.Add(time.Minute))
		require.NoError(t, err)
		assert.True(t, changed)
		got, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusExpired, got.Status)

		// @spec components/entity/checkout/release "Paid checkout"
		paid := completedCheckout(t, repo, issuance, start)
		changed, err = repo.Release(ctx, paid.ID, expiry.Add(time.Hour))
		require.NoError(t, err)
		assert.False(t, changed)
	})

	t.Run("committed just before the sweep", func(t *testing.T) {
		// @spec components/entity/checkout/release "Committed just before the sweep"
		// A commit just before the expiry and the sweep just after it race;
		// whatever the interleaving, at most one of them changes the row, and
		// a committed row stays Committed.
		for range 10 {
			c := heldCheckout(t, repo, 1, start)
			var wg sync.WaitGroup
			var outcome entity.CommitOutcome
			var released bool
			var commitErr, releaseErr error
			wg.Go(func() { outcome, commitErr = repo.Commit(ctx, c.ID, expiry.Add(-time.Second)) })
			wg.Go(func() { released, releaseErr = repo.Release(ctx, c.ID, expiry.Add(time.Second)) })
			wg.Wait()
			require.NoError(t, commitErr)
			require.NoError(t, releaseErr)

			got, err := repo.Get(ctx, c.ID)
			require.NoError(t, err)
			if outcome == entity.CommitOutcomeCommitted {
				assert.False(t, released)
				assert.Equal(t, entity.CheckoutStatusCommitted, got.Status)
			} else {
				assert.True(t, released)
				assert.Equal(t, entity.CheckoutStatusExpired, got.Status)
			}
		}
	})

	t.Run("revert commit", func(t *testing.T) {
		// @spec components/entity/checkout/revert-commit "Card closed after the commit"
		c := heldCheckout(t, repo, 2, start)
		committedAt := start.Add(10 * time.Minute)
		_, err := repo.Commit(ctx, c.ID, committedAt)
		require.NoError(t, err)
		require.NoError(t, repo.RevertCommit(ctx, c.ID))
		got, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusReleased, got.Status)
		assert.Equal(t, committedAt, got.CommitTime.UTC())
		assert.Equal(t, 0, soldCount(t, c.TicketTypeID))

		// @spec components/entity/checkout/revert-commit "Already charged"
		charged := heldCheckout(t, repo, 2, start)
		_, err = repo.Commit(ctx, charged.ID, committedAt)
		require.NoError(t, err)
		require.NoError(t, repo.RecordCapture(ctx, charged.ID, committedAt))
		assert.ErrorIs(t, repo.RevertCommit(ctx, charged.ID), apperr.ErrFailedPrecondition)
		got, err = repo.Get(ctx, charged.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.CheckoutStatusCommitted, got.Status)
	})

	t.Run("record capture", func(t *testing.T) {
		c := heldCheckout(t, repo, 2, start)
		_, err := repo.Commit(ctx, c.ID, start.Add(time.Minute))
		require.NoError(t, err)
		chargedAt := start.Add(14 * time.Minute)

		// @spec components/entity/checkout/record-capture "First charge"
		require.NoError(t, repo.RecordCapture(ctx, c.ID, chargedAt))
		got, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, chargedAt, got.CaptureTime.UTC())

		// @spec components/entity/checkout/record-capture "Recorded twice"
		require.NoError(t, repo.RecordCapture(ctx, c.ID, chargedAt.Add(time.Hour)))
		got, err = repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, chargedAt, got.CaptureTime.UTC())

		held := heldCheckout(t, repo, 1, start)
		assert.ErrorIs(t, repo.RecordCapture(ctx, held.ID, chargedAt), apperr.ErrFailedPrecondition)
	})

	t.Run("record authorization release", func(t *testing.T) {
		// @spec components/entity/checkout/record-card-authorization-release "Card authorization given back"
		c := heldCheckout(t, repo, 1, start)
		require.NoError(t, repo.SetCardAuthorization(ctx, c.ID, "pi_rel"))
		_, err := repo.Release(ctx, c.ID, expiry)
		require.NoError(t, err)
		releasedAt := expiry.Add(time.Minute)

		require.NoError(t, repo.RecordCardAuthorizationRelease(ctx, c.ID, releasedAt))
		got, err := repo.Get(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, releasedAt, got.CardAuthorizationReleaseTime.UTC())
		require.NoError(t, repo.RecordCardAuthorizationRelease(ctx, c.ID, releasedAt.Add(time.Hour)), "already set: no change")

		held := heldCheckout(t, repo, 1, start)
		assert.ErrorIs(t, repo.RecordCardAuthorizationRelease(ctx, held.ID, releasedAt), apperr.ErrFailedPrecondition)
	})
}

func TestCheckoutRepository_ListDue(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewCheckoutRepository(testDB)
	ctx := context.Background()
	t0 := saleWindowStart.Add(time.Hour)
	now := t0.Add(30 * time.Minute)

	// @spec components/entity/checkout/list-due "One of each"
	lapsed := heldCheckout(t, repo, 1, t0.Add(5*time.Minute)) // expired at t0+20m
	released := heldCheckout(t, repo, 1, t0)
	require.NoError(t, repo.SetCardAuthorization(ctx, released.ID, "pi_released"))
	_, err := repo.Release(ctx, released.ID, t0.Add(16*time.Minute))
	require.NoError(t, err)
	stalled := heldCheckout(t, repo, 1, t0.Add(20*time.Minute))
	_, err = repo.Commit(ctx, stalled.ID, now.Add(-2*time.Minute))
	require.NoError(t, err)

	// @spec components/entity/checkout/list-due "Fresh commit"
	fresh := heldCheckout(t, repo, 1, t0.Add(20*time.Minute))
	_, err = repo.Commit(ctx, fresh.ID, now.Add(-30*time.Second))
	require.NoError(t, err)

	// @spec components/entity/checkout/list-due "Card authorization already given back"
	givenBack := heldCheckout(t, repo, 1, t0)
	require.NoError(t, repo.SetCardAuthorization(ctx, givenBack.ID, "pi_given_back"))
	_, err = repo.Release(ctx, givenBack.ID, t0.Add(16*time.Minute))
	require.NoError(t, err)
	require.NoError(t, repo.RecordCardAuthorizationRelease(ctx, givenBack.ID, t0.Add(17*time.Minute)))

	due, err := repo.ListDue(ctx, now)

	require.NoError(t, err)
	ids := map[entity.CheckoutID]bool{}
	for _, r := range due {
		ids[r.ID] = true
	}
	assert.True(t, ids[lapsed.ID], "lapsed hold")
	assert.True(t, ids[released.ID], "card authorization to give back")
	assert.True(t, ids[stalled.ID], "stalled commit")
	assert.False(t, ids[fresh.ID], "fresh commit")
	assert.False(t, ids[givenBack.ID], "card authorization already given back")
	assert.Len(t, due, 3)
}

func TestCheckoutRepository_Serialize(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	repo := rdb.NewCheckoutRepository(testDB)
	ctx := context.Background()
	id := entity.CheckoutID(entity.NewID())

	// Two calls for the same checkout never overlap.
	var mu sync.Mutex
	inside, overlapped := 0, false
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			err := repo.Serialize(ctx, id, func(context.Context) error {
				mu.Lock()
				inside++
				if inside > 1 {
					overlapped = true
				}
				mu.Unlock()
				time.Sleep(50 * time.Millisecond)
				mu.Lock()
				inside--
				mu.Unlock()
				return nil
			})
			assert.NoError(t, err)
		})
	}
	wg.Wait()
	assert.False(t, overlapped)

	// fn's error is returned unchanged.
	want := apperr.ErrUnavailable
	assert.ErrorIs(t, repo.Serialize(ctx, id, func(context.Context) error { return want }), want)
}

// completedCheckout drives a checkout for 2 tickets on a fresh ticket type
// through commit, capture and issuance, returning it Completed.
func completedCheckout(t *testing.T, repo *rdb.CheckoutRepository, issuance *rdb.IssuanceRepository, start time.Time) *entity.Checkout {
	t.Helper()
	tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 150, 10)
	return completedCheckoutOf(t, repo, issuance, tt, nextFan(t), 2, start)
}

// completedCheckoutOf drives a checkout of fan for count tickets of tt
// through commit, capture and issuance, returning it Completed.
func completedCheckoutOf(t *testing.T, repo *rdb.CheckoutRepository, issuance *rdb.IssuanceRepository, tt *entity.TicketType, fan entity.UserID, count int, start time.Time) *entity.Checkout {
	t.Helper()
	ctx := context.Background()
	c, err := repo.GetOrCreateHeld(ctx, tt.ID, fan, count, start, nil)
	require.NoError(t, err)
	require.NoError(t, repo.SetCardAuthorization(ctx, c.ID, "pi_"+string(c.ID)))
	_, err = repo.Commit(ctx, c.ID, start.Add(time.Minute))
	require.NoError(t, err)
	require.NoError(t, repo.RecordCapture(ctx, c.ID, start.Add(2*time.Minute)))
	order, tickets, settlement := checkoutOrder(t, c, start.Add(2*time.Minute))
	require.NoError(t, issuance.Issue(ctx, order, tickets, settlement))
	return c
}

// checkoutOrder builds the Order, Tickets and Settlement of a charged
// checkout, as OrderUseCase does.
func checkoutOrder(t *testing.T, c *entity.Checkout, now time.Time) (*entity.Order, []*entity.Ticket, *entity.Settlement) {
	t.Helper()
	var eventID string
	require.NoError(t, testDB.Pool.QueryRow(context.Background(),
		`SELECT event_id FROM ticket_types WHERE id = $1`, string(c.TicketTypeID)).Scan(&eventID))
	order := &entity.Order{
		ID:         entity.OrderID(entity.NewID()),
		UserID:     c.UserID,
		CheckoutID: c.ID,
		Payment:    entity.Payment{Provider: entity.PaymentProviderStripe, ProviderPaymentID: "pi_" + string(c.ID)},
		Status:     entity.OrderStatusPaid,
		Amount:     c.Amount,
		Currency:   "JPY",
		PayTime:    now,
	}
	tickets := make([]*entity.Ticket, 0, c.TicketCount)
	for range c.TicketCount {
		tickets = append(tickets, &entity.Ticket{
			ID: entity.TicketID(entity.NewID()), OrderID: order.ID, UserID: c.UserID, EventID: eventID,
			ResaleWithoutConsentProhibited: true,
			Status:                         entity.TicketStatusIssued, IssueTime: now,
		})
	}
	settlement := entity.NewHeldSettlement(order, seedOrganizer(t), eventID, 800, now)
	return order, tickets, settlement
}

func TestCheckoutRepository_GetByCardAuthorizationID(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	repo := rdb.NewCheckoutRepository(testDB)
	ctx := context.Background()
	start := saleWindowStart.Add(time.Hour)

	t.Run("charged checkout", func(t *testing.T) {
		// @spec components/entity/checkout/get-by-card-authorization-id "Charged checkout"
		c := heldCheckout(t, repo, 1, start)
		ref := "pi_lookup_" + string(c.ID)
		require.NoError(t, repo.SetCardAuthorization(ctx, c.ID, ref))

		got, err := repo.GetByCardAuthorizationID(ctx, ref)

		require.NoError(t, err)
		assert.Equal(t, c.ID, got.ID)
	})

	t.Run("unknown card authorization", func(t *testing.T) {
		// @spec components/entity/checkout/get-by-card-authorization-id "Unknown card authorization"
		_, err := repo.GetByCardAuthorizationID(ctx, "pi_unknown_"+entity.NewID())

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

// insertCheckoutOf stores a checkout of one ticket of the ticket type for the
// user in the given status, held until holdExpire, with the card
// authorization, commit and capture times that status requires, and returns
// its id.
func insertCheckoutOf(t *testing.T, ticketTypeID entity.TicketTypeID, userID entity.UserID, status entity.CheckoutStatus, holdExpire time.Time) entity.CheckoutID {
	t.Helper()
	id := entity.NewID()
	var authorization, commitAt, captureAt any
	switch status {
	case entity.CheckoutStatusCommitted:
		authorization, commitAt = "pi_"+id, holdExpire.Add(-time.Minute)
	case entity.CheckoutStatusCompleted:
		authorization, commitAt, captureAt = "pi_"+id, holdExpire.Add(-time.Minute), holdExpire
	}
	_, err := testDB.Pool.Exec(context.Background(), `
		INSERT INTO checkouts (id, ticket_type_id, user_id, ticket_count, amount, card_authorization_id, status, hold_expire_at, commit_at, capture_at)
		VALUES ($1, $2, $3, 1, 3000, $4, $5, $6, $7, $8)
	`, id, string(ticketTypeID), string(userID), authorization, int16(status), holdExpire, commitAt, captureAt)
	require.NoError(t, err)
	return entity.CheckoutID(id)
}

func TestCheckoutRepository_ListByUser(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewCheckoutRepository(testDB)
	ctx := context.Background()

	t.Run("two checkouts", func(t *testing.T) {
		// @spec components/entity/checkout/list-by-user "Two checkouts"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 100, 4)
		userID := entity.UserID(seedFanUser(t))
		now := time.Now()
		completed := insertCheckoutOf(t, tt.ID, userID, entity.CheckoutStatusCompleted, now.AddDate(0, -1, 0))
		held := insertCheckoutOf(t, tt.ID, userID, entity.CheckoutStatusHeld, now.Add(14*time.Minute))
		insertCheckoutOf(t, tt.ID, entity.UserID(seedFanUser(t)), entity.CheckoutStatusHeld, now.Add(14*time.Minute)) // another user

		got, err := repo.ListByUser(ctx, userID)

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, held, got[0].ID)
		assert.Equal(t, entity.CheckoutStatusHeld, got[0].Status)
		assert.Equal(t, completed, got[1].ID)
		assert.Equal(t, entity.CheckoutStatusCompleted, got[1].Status)
	})

	t.Run("never checked out", func(t *testing.T) {
		// @spec components/entity/checkout/list-by-user "Never checked out"
		got, err := repo.ListByUser(ctx, entity.UserID(seedFanUser(t)))

		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})
}
