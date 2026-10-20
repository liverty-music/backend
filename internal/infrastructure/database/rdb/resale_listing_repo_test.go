package rdb_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resaleEventStart is the start time of every resale test event, so its
// resale deadline is 3 hours earlier.
var resaleEventStart = saleWindowStart.Add(30 * 24 * time.Hour)

// resaleDeadline is the resale deadline of every resale test event.
var resaleDeadline = resaleEventStart.Add(-3 * time.Hour)

// soldOutType is a first-come ticket type of quantity tickets at 3000 yen
// whose every ticket a seller bought in one checkout at saleWindowStart, so
// its resale is open.
type soldOutType struct {
	tt      *entity.TicketType
	seller  entity.UserID
	tickets []*entity.Ticket
}

// seedSoldOutType sells out a fresh ticket type of quantity tickets (1 to
// 10) to one seller and gives its event a start time.
func seedSoldOutType(t *testing.T, quantity int) *soldOutType {
	t.Helper()
	ctx := context.Background()
	tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, quantity, quantity)
	_, err := testDB.Pool.Exec(ctx, `UPDATE events SET start_at = $2 WHERE id = $1`, tt.EventID, resaleEventStart)
	require.NoError(t, err)

	seller := nextFan(t)
	c := completedCheckoutOf(t, rdb.NewCheckoutRepository(testDB), rdb.NewIssuanceRepository(testDB), tt, seller, quantity, saleWindowStart)
	order, err := rdb.NewOrderRepository(testDB).GetByCheckoutID(ctx, c.ID)
	require.NoError(t, err)
	tickets := ticketsOfOrder(t, rdb.NewTicketRepository(testDB), seller, order.ID)
	require.Len(t, tickets, quantity)
	return &soldOutType{tt: tt, seller: seller, tickets: tickets}
}

// list lists the i-th ticket of the seller at at.
func (s *soldOutType) list(t *testing.T, i int, at time.Time) *entity.ResaleListing {
	t.Helper()
	l, err := rdb.NewResaleListingRepository(testDB).Create(context.Background(), entity.NewResaleListing(s.tickets[i], s.tt, at))
	require.NoError(t, err)
	return l
}

// listingStatus returns the stored status of the listing.
func listingStatus(t *testing.T, id entity.ResaleListingID) entity.ResaleListingStatus {
	t.Helper()
	l, err := rdb.NewResaleListingRepository(testDB).Get(context.Background(), id)
	require.NoError(t, err)
	return l.Status
}

func TestResaleListingRepository_Create(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewResaleListingRepository(testDB)
	ctx := context.Background()
	at := saleWindowStart.Add(24 * time.Hour)

	t.Run("first listing", func(t *testing.T) {
		// @spec components/entity/resale-listing/create "First listing"
		s := seedSoldOutType(t, 2)

		got, err := repo.Create(ctx, entity.NewResaleListing(s.tickets[0], s.tt, at))

		require.NoError(t, err)
		assert.Equal(t, entity.ResaleListingStatusListed, got.Status)
		assert.Equal(t, s.tickets[0].ID, got.TicketID)
		assert.Equal(t, s.tickets[0].OrderID, got.OrderID)
		assert.Equal(t, s.seller, got.UserID)
		assert.Equal(t, s.tt.ID, got.TicketTypeID)
		assert.Equal(t, int64(3000), got.Price)
		assert.True(t, got.CreateTime.Equal(at))
	})

	t.Run("listed twice", func(t *testing.T) {
		// @spec components/entity/resale-listing/create "Listed twice"
		s := seedSoldOutType(t, 2)

		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := range errs {
			wg.Go(func() {
				_, errs[i] = repo.Create(ctx, entity.NewResaleListing(s.tickets[0], s.tt, at))
			})
		}
		wg.Wait()

		var created, exists int
		for _, err := range errs {
			switch {
			case err == nil:
				created++
			case assert.ErrorIs(t, err, apperr.ErrAlreadyExists):
				exists++
			}
		}
		assert.Equal(t, 1, created)
		assert.Equal(t, 1, exists)
	})

	t.Run("ticket voided meanwhile", func(t *testing.T) {
		// @spec components/entity/resale-listing/create "Ticket voided meanwhile"
		s := seedSoldOutType(t, 1)
		_, err := testDB.Pool.Exec(ctx, `UPDATE tickets SET status = 2 WHERE id = $1`, string(s.tickets[0].ID))
		require.NoError(t, err)

		_, err = repo.Create(ctx, entity.NewResaleListing(s.tickets[0], s.tt, at))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		listings, err := repo.ListByUser(ctx, s.seller)
		require.NoError(t, err)
		assert.Empty(t, listings)
	})

	t.Run("admitted ticket", func(t *testing.T) {
		s := seedSoldOutType(t, 1)
		_, err := testDB.Pool.Exec(ctx, `UPDATE tickets SET admit_at = $2 WHERE id = $1`, string(s.tickets[0].ID), at)
		require.NoError(t, err)

		_, err = repo.Create(ctx, entity.NewResaleListing(s.tickets[0], s.tt, at))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("listed again after withdrawing", func(t *testing.T) {
		// @spec components/entity/resale-listing/create "Listed again after withdrawing"
		s := seedSoldOutType(t, 1)
		first := s.list(t, 0, at)
		require.NoError(t, repo.Withdraw(ctx, first.ID, at.Add(time.Minute)))

		second, err := repo.Create(ctx, entity.NewResaleListing(s.tickets[0], s.tt, at.Add(2*time.Minute)))

		require.NoError(t, err)
		assert.Equal(t, entity.ResaleListingStatusListed, second.Status)
		assert.NotEqual(t, first.ID, second.ID)
	})

	t.Run("unknown ticket", func(t *testing.T) {
		s := seedSoldOutType(t, 1)
		ghost := &entity.Ticket{ID: entity.TicketID(entity.NewID()), OrderID: s.tickets[0].OrderID, UserID: s.seller}

		_, err := repo.Create(ctx, entity.NewResaleListing(ghost, s.tt, at))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestResaleListingRepository_Get(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewResaleListingRepository(testDB)
	ctx := context.Background()
	at := saleWindowStart.Add(24 * time.Hour)

	t.Run("existing listing", func(t *testing.T) {
		// @spec components/entity/resale-listing/get "Existing listing"
		s := seedSoldOutType(t, 1)
		listed := s.list(t, 0, at)

		got, err := repo.Get(ctx, listed.ID)

		require.NoError(t, err)
		assert.Equal(t, listed.ID, got.ID)
		assert.Equal(t, entity.ResaleListingStatusListed, got.Status)
		assert.Nil(t, got.Checkout)
	})

	t.Run("listing with its checkout", func(t *testing.T) {
		s := seedSoldOutType(t, 1)
		listed := s.list(t, 0, at)
		c, err := rdb.NewCheckoutRepository(testDB).GetOrCreateHeld(ctx, s.tt.ID, nextFan(t), 1, at, &resaleDeadline)
		require.NoError(t, err)

		got, err := repo.Get(ctx, listed.ID)

		require.NoError(t, err)
		assert.Equal(t, c.ID, got.CheckoutID)
		require.NotNil(t, got.Checkout)
		assert.True(t, got.Checkout.Resale)
	})

	t.Run("unknown listing", func(t *testing.T) {
		// @spec components/entity/resale-listing/get "Unknown listing"
		_, err := repo.Get(ctx, entity.ResaleListingID(entity.NewID()))
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestResaleListingRepository_Withdraw(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewResaleListingRepository(testDB)
	checkouts := rdb.NewCheckoutRepository(testDB)
	ctx := context.Background()
	at := saleWindowStart.Add(24 * time.Hour)

	t.Run("nobody is buying it", func(t *testing.T) {
		// @spec components/entity/resale-listing/withdraw "Nobody is buying it"
		s := seedSoldOutType(t, 1)
		listed := s.list(t, 0, at)

		require.NoError(t, repo.Withdraw(ctx, listed.ID, at.Add(time.Minute)))

		got, err := repo.Get(ctx, listed.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ResaleListingStatusWithdrawn, got.Status)
		require.NotNil(t, got.WithdrawTime)
		assert.True(t, got.WithdrawTime.Equal(at.Add(time.Minute)))
	})

	t.Run("another fan is checking out", func(t *testing.T) {
		// @spec components/entity/resale-listing/withdraw "Another fan is checking out"
		s := seedSoldOutType(t, 1)
		listed := s.list(t, 0, at)
		_, err := checkouts.GetOrCreateHeld(ctx, s.tt.ID, nextFan(t), 1, at, &resaleDeadline)
		require.NoError(t, err)

		err = repo.Withdraw(ctx, listed.ID, at.Add(time.Minute))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Equal(t, entity.ResaleListingStatusListed, listingStatus(t, listed.ID))
	})

	t.Run("hold lapsed, then withdrawn", func(t *testing.T) {
		s := seedSoldOutType(t, 1)
		listed := s.list(t, 0, at)
		_, err := checkouts.GetOrCreateHeld(ctx, s.tt.ID, nextFan(t), 1, at, &resaleDeadline)
		require.NoError(t, err)

		require.NoError(t, repo.Withdraw(ctx, listed.ID, at.Add(16*time.Minute)))

		assert.Equal(t, entity.ResaleListingStatusWithdrawn, listingStatus(t, listed.ID))
	})

	t.Run("withdraw and hold at the same moment", func(t *testing.T) {
		// @spec components/entity/resale-listing/withdraw "Withdraw and hold at the same moment"
		for range 5 {
			s := seedSoldOutType(t, 1)
			listed := s.list(t, 0, at)
			buyer := nextFan(t)

			var (
				wg          sync.WaitGroup
				withdrawErr error
				holdErr     error
				held        *entity.Checkout
			)
			wg.Go(func() { withdrawErr = repo.Withdraw(ctx, listed.ID, at.Add(time.Minute)) })
			wg.Go(func() {
				held, holdErr = checkouts.GetOrCreateHeld(ctx, s.tt.ID, buyer, 1, at.Add(time.Minute), &resaleDeadline)
			})
			wg.Wait()

			got, err := repo.Get(ctx, listed.ID)
			require.NoError(t, err)
			if withdrawErr == nil {
				assert.ErrorIs(t, holdErr, apperr.ErrResourceExhausted)
				assert.Equal(t, entity.ResaleListingStatusWithdrawn, got.Status)
				assert.Empty(t, got.CheckoutID)
			} else {
				assert.ErrorIs(t, withdrawErr, apperr.ErrFailedPrecondition)
				require.NoError(t, holdErr)
				assert.Equal(t, entity.ResaleListingStatusListed, got.Status)
				assert.Equal(t, held.ID, got.CheckoutID)
			}
		}
	})

	t.Run("already sold", func(t *testing.T) {
		// @spec components/entity/resale-listing/withdraw "Already sold"
		s := seedSoldOutType(t, 1)
		listed := s.list(t, 0, at)
		_, err := testDB.Pool.Exec(ctx, `UPDATE resale_listings SET status = 2, sold_at = $2 WHERE id = $1`, string(listed.ID), at)
		require.NoError(t, err)

		err = repo.Withdraw(ctx, listed.ID, at.Add(time.Minute))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("expired listing", func(t *testing.T) {
		s := seedSoldOutType(t, 1)
		listed := s.list(t, 0, at)

		err := repo.Withdraw(ctx, listed.ID, resaleDeadline)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Equal(t, entity.ResaleListingStatusListed, listingStatus(t, listed.ID))
	})

	t.Run("withdrawn twice", func(t *testing.T) {
		// @spec components/entity/resale-listing/withdraw "Withdrawn twice"
		s := seedSoldOutType(t, 1)
		listed := s.list(t, 0, at)
		require.NoError(t, repo.Withdraw(ctx, listed.ID, at.Add(time.Minute)))

		require.NoError(t, repo.Withdraw(ctx, listed.ID, at.Add(2*time.Minute)))

		got, err := repo.Get(ctx, listed.ID)
		require.NoError(t, err)
		assert.True(t, got.WithdrawTime.Equal(at.Add(time.Minute)), "the first withdraw time is kept")
	})

	t.Run("unknown listing", func(t *testing.T) {
		err := repo.Withdraw(ctx, entity.ResaleListingID(entity.NewID()), at)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestResaleListingRepository_ListByUser(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewResaleListingRepository(testDB)
	ctx := context.Background()

	t.Run("two listings", func(t *testing.T) {
		// @spec components/entity/resale-listing/list-by-user "Two listings"
		s := seedSoldOutType(t, 2)
		nov1 := s.list(t, 0, saleWindowStart)
		require.NoError(t, repo.Withdraw(ctx, nov1.ID, saleWindowStart.Add(time.Hour)))
		nov3 := s.list(t, 1, saleWindowStart.Add(48*time.Hour))

		got, err := repo.ListByUser(ctx, s.seller)

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, nov3.ID, got[0].ID)
		assert.Equal(t, nov1.ID, got[1].ID)
		assert.Equal(t, entity.ResaleListingStatusWithdrawn, got[1].Status)
	})

	t.Run("no listings", func(t *testing.T) {
		// @spec components/entity/resale-listing/list-by-user "No listings"
		got, err := repo.ListByUser(ctx, nextFan(t))

		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestResaleListingRepository_ListSoldUnrefundedAndRecordRefund(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewResaleListingRepository(testDB)
	ctx := context.Background()
	at := saleWindowStart.Add(24 * time.Hour)

	s := seedSoldOutType(t, 4)
	sell := func(l *entity.ResaleListing, soldAt time.Time) {
		t.Helper()
		_, err := testDB.Pool.Exec(ctx, `UPDATE resale_listings SET status = 2, sold_at = $2 WHERE id = $1`, string(l.ID), soldAt)
		require.NoError(t, err)
	}
	justSold := s.list(t, 0, at)
	sell(justSold, at.Add(2*time.Hour))
	refundedNotEmailed := s.list(t, 1, at)
	sell(refundedNotEmailed, at.Add(time.Hour))
	require.NoError(t, repo.RecordRefund(ctx, refundedNotEmailed.ID, "re_1", at.Add(time.Hour)))
	finished := s.list(t, 2, at)
	sell(finished, at.Add(time.Hour))
	require.NoError(t, repo.RecordRefund(ctx, finished.ID, "re_2", at.Add(time.Hour)))
	require.NoError(t, repo.MarkSoldEmailSent(ctx, finished.ID, at.Add(time.Hour)))
	listed := s.list(t, 3, at)

	got, err := repo.ListSoldUnrefunded(ctx)
	require.NoError(t, err)
	ids := make([]entity.ResaleListingID, len(got))
	for i, l := range got {
		ids[i] = l.ID
	}

	t.Run("just sold", func(t *testing.T) {
		// @spec components/entity/resale-listing/list-sold-unrefunded "Just sold"
		assert.Contains(t, ids, justSold.ID)
	})
	t.Run("refunded, email not sent", func(t *testing.T) {
		// @spec components/entity/resale-listing/list-sold-unrefunded "Refunded, email not sent"
		assert.Contains(t, ids, refundedNotEmailed.ID)
	})
	t.Run("finished", func(t *testing.T) {
		// @spec components/entity/resale-listing/list-sold-unrefunded "Finished"
		assert.NotContains(t, ids, finished.ID)
	})
	t.Run("listed listing", func(t *testing.T) {
		// @spec components/entity/resale-listing/list-sold-unrefunded "Listed listing"
		assert.NotContains(t, ids, listed.ID)
	})
	t.Run("oldest sale first", func(t *testing.T) {
		assert.Equal(t, []entity.ResaleListingID{refundedNotEmailed.ID, justSold.ID}, ids)
	})

	t.Run("refund recorded", func(t *testing.T) {
		// @spec components/entity/resale-listing/record-refund "Refund recorded"
		require.NoError(t, repo.RecordRefund(ctx, justSold.ID, "re_3", at.Add(3*time.Hour)))

		l, err := repo.Get(ctx, justSold.ID)
		require.NoError(t, err)
		assert.Equal(t, "re_3", l.ProviderRefundID)
		require.NotNil(t, l.RefundTime)
		assert.True(t, l.RefundTime.Equal(at.Add(3*time.Hour)))
	})

	t.Run("recorded twice", func(t *testing.T) {
		// @spec components/entity/resale-listing/record-refund "Recorded twice"
		require.NoError(t, repo.RecordRefund(ctx, justSold.ID, "re_other", at.Add(4*time.Hour)))

		l, err := repo.Get(ctx, justSold.ID)
		require.NoError(t, err)
		assert.Equal(t, "re_3", l.ProviderRefundID)
		assert.True(t, l.RefundTime.Equal(at.Add(3*time.Hour)))
	})

	t.Run("listing not sold", func(t *testing.T) {
		// @spec components/entity/resale-listing/record-refund "Listing not sold"
		err := repo.RecordRefund(ctx, listed.ID, "re_4", at)
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})
}
