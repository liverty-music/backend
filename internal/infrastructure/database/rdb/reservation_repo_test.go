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
	return newFan(t, fmt.Sprintf("res-%d", n))
}

var (
	holderIdentity = entity.HolderIdentity{FullName: "山田 花子", PhoneNumber: "+819012345678"}
	capturedPay    = &entity.CapturedPayment{PaymentIntentRef: "pi_test", CardBrand: "visa", CardLast4: "4242"}
)

func TestReservationRepository_GetOrCreateHeld(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewReservationRepository(testDB)
	sales := rdb.NewTicketSaleRepository(testDB)
	ctx := context.Background()
	at := saleWindowStart.Add(time.Hour)

	t.Run("first start", func(t *testing.T) {
		// @spec components/entity/reservation/get-or-create-held "First start"
		sale := seedTicketSale(t, 150, 0)
		setSold(t, sale.ID, 106)

		res, err := repo.GetOrCreateHeld(ctx, sale.ID, nextFan(t), 2, at, "trace-1")

		require.NoError(t, err)
		assert.Equal(t, entity.ReservationStatusHeld, res.Status)
		assert.Equal(t, 2, res.TicketCount)
		assert.Equal(t, int64(6000), res.Amount)
		assert.Equal(t, at.Add(15*time.Minute), res.HoldExpireTime)
		stored, err := repo.Get(ctx, res.ID)
		require.NoError(t, err)
		assert.Equal(t, "trace-1", stored.TraceID)
		got, err := sales.Get(ctx, sale.ID, at)
		require.NoError(t, err)
		assert.Equal(t, 2, got.HeldCount)
	})

	t.Run("double tap", func(t *testing.T) {
		// @spec components/entity/reservation/get-or-create-held "Double tap"
		sale := seedTicketSale(t, 150, 0)
		fan := nextFan(t)

		var wg sync.WaitGroup
		results := make([]*entity.Reservation, 2)
		errs := make([]error, 2)
		for i := range 2 {
			wg.Go(func() { results[i], errs[i] = repo.GetOrCreateHeld(ctx, sale.ID, fan, 2, at, "") })
		}
		wg.Wait()

		require.NoError(t, errs[0])
		require.NoError(t, errs[1])
		assert.Equal(t, results[0].ID, results[1].ID)
		got, err := sales.Get(ctx, sale.ID, at)
		require.NoError(t, err)
		assert.Equal(t, 2, got.HeldCount)
	})

	t.Run("count changed", func(t *testing.T) {
		// @spec components/entity/reservation/get-or-create-held "Count changed"
		sale := seedTicketSale(t, 150, 0)
		fan := nextFan(t)
		first, err := repo.GetOrCreateHeld(ctx, sale.ID, fan, 2, at, "")
		require.NoError(t, err)

		second, err := repo.GetOrCreateHeld(ctx, sale.ID, fan, 3, at.Add(time.Minute), "")

		require.NoError(t, err)
		assert.NotEqual(t, first.ID, second.ID)
		assert.Equal(t, 3, second.TicketCount)
		replaced, err := repo.Get(ctx, first.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ReservationStatusReleased, replaced.Status)
	})

	t.Run("count raised when stock is short", func(t *testing.T) {
		// @spec components/entity/reservation/get-or-create-held "Count raised when stock is short"
		sale := seedTicketSale(t, 10, 4)
		setSold(t, sale.ID, 7)
		fan := nextFan(t)
		held, err := repo.GetOrCreateHeld(ctx, sale.ID, fan, 2, at, "")
		require.NoError(t, err)

		_, err = repo.GetOrCreateHeld(ctx, sale.ID, fan, 4, at, "")

		assert.ErrorIs(t, err, apperr.ErrResourceExhausted)
		still, err := repo.Get(ctx, held.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ReservationStatusHeld, still.Status)
	})

	t.Run("two fans, last ticket", func(t *testing.T) {
		// @spec components/entity/reservation/get-or-create-held "Two fans, last ticket"
		sale := seedTicketSale(t, 10, 0)
		setSold(t, sale.ID, 9)
		fans := []entity.UserID{nextFan(t), nextFan(t)}

		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := range 2 {
			wg.Go(func() { _, errs[i] = repo.GetOrCreateHeld(ctx, sale.ID, fans[i], 1, at, "") })
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
		got, err := sales.Get(ctx, sale.ID, at)
		require.NoError(t, err)
		assert.Equal(t, 1, got.HeldCount)
	})

	t.Run("over the per-account limit", func(t *testing.T) {
		// @spec components/entity/reservation/get-or-create-held "Over the per-account limit"
		sale := seedTicketSale(t, 150, 4)
		fan := nextFan(t)
		bought, err := repo.GetOrCreateHeld(ctx, sale.ID, fan, 3, at, "")
		require.NoError(t, err)
		outcome, err := repo.Commit(ctx, bought.ID, at)
		require.NoError(t, err)
		require.Equal(t, entity.CommitOutcomeCommitted, outcome)

		_, err = repo.GetOrCreateHeld(ctx, sale.ID, fan, 2, at, "")

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("a lapsed hold is expired when the fan starts again", func(t *testing.T) {
		sale := seedTicketSale(t, 150, 0)
		fan := nextFan(t)
		lapsed, err := repo.GetOrCreateHeld(ctx, sale.ID, fan, 2, at, "")
		require.NoError(t, err)

		fresh, err := repo.GetOrCreateHeld(ctx, sale.ID, fan, 2, at.Add(16*time.Minute), "")

		require.NoError(t, err)
		assert.NotEqual(t, lapsed.ID, fresh.ID)
		old, err := repo.Get(ctx, lapsed.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ReservationStatusExpired, old.Status)
	})

	t.Run("unknown sale", func(t *testing.T) {
		_, err := repo.GetOrCreateHeld(ctx, entity.TicketSaleID(entity.NewID()), nextFan(t), 1, at, "")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

// heldReservation starts a checkout for count tickets on a fresh sale.
func heldReservation(t *testing.T, repo *rdb.ReservationRepository, count int, at time.Time) *entity.Reservation {
	t.Helper()
	sale := seedTicketSale(t, 150, 10)
	res, err := repo.GetOrCreateHeld(context.Background(), sale.ID, nextFan(t), count, at, "")
	require.NoError(t, err)
	return res
}

func TestReservationRepository_Lifecycle(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewReservationRepository(testDB)
	sales := rdb.NewTicketSaleRepository(testDB)
	issuance := rdb.NewIssuanceRepository(testDB)
	ctx := context.Background()
	start := saleWindowStart.Add(time.Hour)
	expiry := start.Add(15 * time.Minute)

	t.Run("get", func(t *testing.T) {
		// @spec components/entity/reservation/get "Unknown reservation"
		_, err := repo.Get(ctx, entity.ReservationID(entity.NewID()))
		assert.ErrorIs(t, err, apperr.ErrNotFound)

		// @spec components/entity/reservation/get "Completed checkout"
		res := completedReservation(t, repo, issuance, start)
		got, err := repo.Get(ctx, res.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ReservationStatusCompleted, got.Status)
	})

	t.Run("set authorization", func(t *testing.T) {
		res := heldReservation(t, repo, 2, start)

		// @spec components/entity/reservation/set-authorization "First authorization"
		require.NoError(t, repo.SetAuthorization(ctx, res.ID, holderIdentity, "pi_auth_1"))
		got, err := repo.Get(ctx, res.ID)
		require.NoError(t, err)
		assert.Equal(t, "pi_auth_1", got.AuthorizationRef)
		assert.Equal(t, &holderIdentity, got.HolderIdentity)

		// @spec components/entity/reservation/set-authorization "Name corrected on retry"
		corrected := entity.HolderIdentity{FullName: "山田 花", PhoneNumber: holderIdentity.PhoneNumber}
		require.NoError(t, repo.SetAuthorization(ctx, res.ID, corrected, "pi_auth_1"))
		got, err = repo.Get(ctx, res.ID)
		require.NoError(t, err)
		assert.Equal(t, "山田 花", got.HolderIdentity.FullName)

		assert.ErrorIs(t, repo.SetAuthorization(ctx, res.ID, holderIdentity, "pi_other"), apperr.ErrFailedPrecondition)
		assert.ErrorIs(t, repo.SetAuthorization(ctx, res.ID, entity.HolderIdentity{FullName: "x", PhoneNumber: "090-1234-5678"}, "pi_auth_1"),
			apperr.ErrInvalidArgument)

		// @spec components/entity/reservation/set-authorization "Expired checkout"
		lapsed := heldReservation(t, repo, 1, start)
		_, err = repo.Release(ctx, lapsed.ID, expiry)
		require.NoError(t, err)
		assert.ErrorIs(t, repo.SetAuthorization(ctx, lapsed.ID, holderIdentity, "pi_x"), apperr.ErrFailedPrecondition)
	})

	t.Run("commit", func(t *testing.T) {
		// @spec components/entity/reservation/commit "Within the hold"
		res := heldReservation(t, repo, 2, start)
		outcome, err := repo.Commit(ctx, res.ID, start.Add(5*time.Minute))
		require.NoError(t, err)
		assert.Equal(t, entity.CommitOutcomeCommitted, outcome)
		got, err := repo.Get(ctx, res.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ReservationStatusCommitted, got.Status)
		assert.Equal(t, start.Add(5*time.Minute), got.CommitTime.UTC())
		sale, err := sales.Get(ctx, res.TicketSaleID, start)
		require.NoError(t, err)
		assert.Equal(t, 2, sale.SoldCount)
		assert.Equal(t, 0, sale.HeldCount)

		// @spec components/entity/reservation/commit "Repeated commit"
		outcome, err = repo.Commit(ctx, res.ID, start.Add(6*time.Minute))
		require.NoError(t, err)
		assert.Equal(t, entity.CommitOutcomeCommitted, outcome)
		sale, err = sales.Get(ctx, res.TicketSaleID, start)
		require.NoError(t, err)
		assert.Equal(t, 2, sale.SoldCount, "a repeated commit adds nothing")

		// @spec components/entity/reservation/commit "Hold lapsed"
		lapsed := heldReservation(t, repo, 2, start)
		outcome, err = repo.Commit(ctx, lapsed.ID, expiry)
		require.NoError(t, err)
		assert.Equal(t, entity.CommitOutcomeNotHeld, outcome)
		got, err = repo.Get(ctx, lapsed.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ReservationStatusHeld, got.Status)
	})

	t.Run("release", func(t *testing.T) {
		// @spec components/entity/reservation/release "Still holding"
		res := heldReservation(t, repo, 2, start)
		changed, err := repo.Release(ctx, res.ID, expiry.Add(-time.Minute))
		require.NoError(t, err)
		assert.False(t, changed)

		// @spec components/entity/reservation/release "Hold lapsed"
		changed, err = repo.Release(ctx, res.ID, expiry.Add(time.Minute))
		require.NoError(t, err)
		assert.True(t, changed)
		got, err := repo.Get(ctx, res.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ReservationStatusExpired, got.Status)

		// @spec components/entity/reservation/release "Paid checkout"
		paid := completedReservation(t, repo, issuance, start)
		changed, err = repo.Release(ctx, paid.ID, expiry.Add(time.Hour))
		require.NoError(t, err)
		assert.False(t, changed)
	})

	t.Run("committed just before the sweep", func(t *testing.T) {
		// @spec components/entity/reservation/release "Committed just before the sweep"
		// A commit just before the expiry and the sweep just after it race;
		// whatever the interleaving, at most one of them changes the row, and
		// a committed row stays Committed.
		for range 10 {
			res := heldReservation(t, repo, 1, start)
			var wg sync.WaitGroup
			var outcome entity.CommitOutcome
			var released bool
			var commitErr, releaseErr error
			wg.Go(func() { outcome, commitErr = repo.Commit(ctx, res.ID, expiry.Add(-time.Second)) })
			wg.Go(func() { released, releaseErr = repo.Release(ctx, res.ID, expiry.Add(time.Second)) })
			wg.Wait()
			require.NoError(t, commitErr)
			require.NoError(t, releaseErr)

			got, err := repo.Get(ctx, res.ID)
			require.NoError(t, err)
			if outcome == entity.CommitOutcomeCommitted {
				assert.False(t, released)
				assert.Equal(t, entity.ReservationStatusCommitted, got.Status)
			} else {
				assert.True(t, released)
				assert.Equal(t, entity.ReservationStatusExpired, got.Status)
			}
		}
	})

	t.Run("revert commit", func(t *testing.T) {
		// @spec components/entity/reservation/revert-commit "Card closed after the commit"
		res := heldReservation(t, repo, 2, start)
		committedAt := start.Add(10 * time.Minute)
		_, err := repo.Commit(ctx, res.ID, committedAt)
		require.NoError(t, err)
		require.NoError(t, repo.RevertCommit(ctx, res.ID))
		got, err := repo.Get(ctx, res.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ReservationStatusReleased, got.Status)
		assert.Equal(t, committedAt, got.CommitTime.UTC())
		sale, err := sales.Get(ctx, res.TicketSaleID, start)
		require.NoError(t, err)
		assert.Equal(t, 0, sale.SoldCount)

		// @spec components/entity/reservation/revert-commit "Already charged"
		charged := heldReservation(t, repo, 2, start)
		_, err = repo.Commit(ctx, charged.ID, committedAt)
		require.NoError(t, err)
		require.NoError(t, repo.RecordCapture(ctx, charged.ID, committedAt, capturedPay))
		assert.ErrorIs(t, repo.RevertCommit(ctx, charged.ID), apperr.ErrFailedPrecondition)
		got, err = repo.Get(ctx, charged.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ReservationStatusCommitted, got.Status)
	})

	t.Run("record capture", func(t *testing.T) {
		res := heldReservation(t, repo, 2, start)
		_, err := repo.Commit(ctx, res.ID, start.Add(time.Minute))
		require.NoError(t, err)
		chargedAt := start.Add(14 * time.Minute)

		// @spec components/entity/reservation/record-capture "First charge"
		require.NoError(t, repo.RecordCapture(ctx, res.ID, chargedAt, capturedPay))
		got, err := repo.Get(ctx, res.ID)
		require.NoError(t, err)
		assert.Equal(t, chargedAt, got.CaptureTime.UTC())
		assert.Equal(t, "pi_test", got.PaymentRef)
		assert.Equal(t, "4242", got.CardLast4)

		// @spec components/entity/reservation/record-capture "Recorded twice"
		require.NoError(t, repo.RecordCapture(ctx, res.ID, chargedAt.Add(time.Hour), &entity.CapturedPayment{PaymentIntentRef: "pi_other"}))
		got, err = repo.Get(ctx, res.ID)
		require.NoError(t, err)
		assert.Equal(t, chargedAt, got.CaptureTime.UTC())
		assert.Equal(t, "pi_test", got.PaymentRef)

		held := heldReservation(t, repo, 1, start)
		assert.ErrorIs(t, repo.RecordCapture(ctx, held.ID, chargedAt, capturedPay), apperr.ErrFailedPrecondition)
	})

	t.Run("record authorization release", func(t *testing.T) {
		// @spec components/entity/reservation/record-authorization-release "Hold given back"
		res := heldReservation(t, repo, 1, start)
		require.NoError(t, repo.SetAuthorization(ctx, res.ID, holderIdentity, "pi_rel"))
		_, err := repo.Release(ctx, res.ID, expiry)
		require.NoError(t, err)
		releasedAt := expiry.Add(time.Minute)

		require.NoError(t, repo.RecordAuthorizationRelease(ctx, res.ID, releasedAt))
		got, err := repo.Get(ctx, res.ID)
		require.NoError(t, err)
		assert.Equal(t, releasedAt, got.AuthorizationReleaseTime.UTC())
		require.NoError(t, repo.RecordAuthorizationRelease(ctx, res.ID, releasedAt.Add(time.Hour)), "already set: no change")

		held := heldReservation(t, repo, 1, start)
		assert.ErrorIs(t, repo.RecordAuthorizationRelease(ctx, held.ID, releasedAt), apperr.ErrFailedPrecondition)
	})
}

func TestReservationRepository_ListDue(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewReservationRepository(testDB)
	ctx := context.Background()
	t0 := saleWindowStart.Add(time.Hour)
	now := t0.Add(30 * time.Minute)

	// @spec components/entity/reservation/list-due "One of each"
	lapsed := heldReservation(t, repo, 1, t0.Add(5*time.Minute)) // expired at t0+20m
	released := heldReservation(t, repo, 1, t0)
	require.NoError(t, repo.SetAuthorization(ctx, released.ID, holderIdentity, "pi_released"))
	_, err := repo.Release(ctx, released.ID, t0.Add(16*time.Minute))
	require.NoError(t, err)
	stalled := heldReservation(t, repo, 1, t0.Add(20*time.Minute))
	_, err = repo.Commit(ctx, stalled.ID, now.Add(-2*time.Minute))
	require.NoError(t, err)

	// @spec components/entity/reservation/list-due "Fresh commit"
	fresh := heldReservation(t, repo, 1, t0.Add(20*time.Minute))
	_, err = repo.Commit(ctx, fresh.ID, now.Add(-30*time.Second))
	require.NoError(t, err)

	// @spec components/entity/reservation/list-due "Card hold already given back"
	givenBack := heldReservation(t, repo, 1, t0)
	require.NoError(t, repo.SetAuthorization(ctx, givenBack.ID, holderIdentity, "pi_given_back"))
	_, err = repo.Release(ctx, givenBack.ID, t0.Add(16*time.Minute))
	require.NoError(t, err)
	require.NoError(t, repo.RecordAuthorizationRelease(ctx, givenBack.ID, t0.Add(17*time.Minute)))

	due, err := repo.ListDue(ctx, now)

	require.NoError(t, err)
	ids := map[entity.ReservationID]bool{}
	for _, r := range due {
		ids[r.ID] = true
	}
	assert.True(t, ids[lapsed.ID], "lapsed hold")
	assert.True(t, ids[released.ID], "card hold to give back")
	assert.True(t, ids[stalled.ID], "stalled commit")
	assert.False(t, ids[fresh.ID], "fresh commit")
	assert.False(t, ids[givenBack.ID], "card hold already given back")
	assert.Len(t, due, 3)
}

func TestReservationRepository_Serialize(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	repo := rdb.NewReservationRepository(testDB)
	ctx := context.Background()
	id := entity.ReservationID(entity.NewID())

	// Two calls for the same reservation never overlap.
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

// completedReservation drives a reservation through commit, capture and
// issuance, returning it Completed.
func completedReservation(t *testing.T, repo *rdb.ReservationRepository, issuance *rdb.IssuanceRepository, start time.Time) *entity.Reservation {
	t.Helper()
	ctx := context.Background()
	res := heldReservation(t, repo, 2, start)
	require.NoError(t, repo.SetAuthorization(ctx, res.ID, holderIdentity, "pi_"+string(res.ID)))
	_, err := repo.Commit(ctx, res.ID, start.Add(time.Minute))
	require.NoError(t, err)
	require.NoError(t, repo.RecordCapture(ctx, res.ID, start.Add(2*time.Minute),
		&entity.CapturedPayment{PaymentIntentRef: "pi_" + string(res.ID), CardBrand: "visa", CardLast4: "4242"}))
	order, tickets, settlement := checkoutOrder(t, res, start.Add(2*time.Minute))
	require.NoError(t, issuance.Issue(ctx, order, tickets, settlement))
	return res
}

// checkoutOrder builds the Order, Tickets and Settlement of a charged
// reservation, as IssuanceUseCase does.
func checkoutOrder(t *testing.T, res *entity.Reservation, now time.Time) (*entity.Order, []*entity.Ticket, *entity.Settlement) {
	t.Helper()
	var eventID string
	require.NoError(t, testDB.Pool.QueryRow(context.Background(),
		`SELECT event_id FROM ticket_sales WHERE id = $1`, string(res.TicketSaleID)).Scan(&eventID))
	order := &entity.Order{
		ID:            entity.OrderID(entity.NewID()),
		BuyerID:       res.UserID,
		ReservationID: res.ID,
		Payment:       entity.Payment{Provider: entity.PaymentProviderStripe, PaymentIntentRef: "pi_" + string(res.ID)},
		Status:        entity.OrderStatusPaid,
		Amount:        res.Amount,
		Currency:      "JPY",
		PaidTime:      now,
	}
	tickets := make([]*entity.Ticket, 0, res.TicketCount)
	for range res.TicketCount {
		tickets = append(tickets, &entity.Ticket{
			ID: entity.TicketID(entity.NewID()), OrderID: order.ID, HolderID: res.UserID, EventID: eventID,
			HolderIdentity: holderIdentity, ResaleWithoutConsentProhibited: true,
			Status: entity.TicketStatusIssued, IssuedTime: now,
		})
	}
	settlement := entity.NewHeldSettlement(order, seedOrganizer(t), eventID, 800, now)
	return order, tickets, settlement
}
