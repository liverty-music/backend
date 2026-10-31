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

// soldOutType is a first-come TicketStock of quantity tickets at 3000 yen
// whose every ticket one seller holds through a Paid Order, so it is sold out
// and its resale is open.
type soldOutType struct {
	f       *stockFixture
	seller  entity.UserID
	tickets []*entity.Ticket
}

// seedSoldOutType sells out a fresh stock of quantity tickets (1 to 10) to
// one seller and gives its event a start time.
func seedSoldOutType(t *testing.T, quantity int) *soldOutType {
	t.Helper()
	ctx := context.Background()
	f := seedStock(t, entity.TicketSaleMethodFirstCome, quantity, quantity)
	_, err := testDB.Pool.Exec(ctx, `UPDATE events SET start_at = $2 WHERE id = $1`, f.EventID, resaleEventStart)
	require.NoError(t, err)

	seller := entity.UserID(saleFan(t))
	seedIssuedTicketsOf(t, f, string(seller), quantity)
	tickets, err := rdb.NewTicketRepository(testDB, testOutbox).ListByUserAndEvent(ctx, seller, f.EventID)
	require.NoError(t, err)
	require.Len(t, tickets, quantity)
	return &soldOutType{f: f, seller: seller, tickets: tickets}
}

// newListing returns an unsaved listing of the i-th ticket of the seller at at.
func (s *soldOutType) newListing(i int, at time.Time) *entity.ResaleListing {
	return entity.NewResaleListing(s.tickets[i], s.f.ID, s.f.SeatType, at)
}

// list lists the i-th ticket of the seller at at.
func (s *soldOutType) list(t *testing.T, i int, at time.Time) *entity.ResaleListing {
	t.Helper()
	l, err := rdb.NewResaleListingRepository(testDB).Create(context.Background(), s.newListing(i, at))
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

		got, err := repo.Create(ctx, s.newListing(0, at))

		require.NoError(t, err)
		assert.Equal(t, entity.ResaleListingStatusListed, got.Status)
		assert.Equal(t, s.tickets[0].ID, got.TicketID)
		assert.Equal(t, s.tickets[0].OrderID, got.OrderID)
		assert.Equal(t, s.seller, got.UserID)
		assert.Equal(t, s.f.ID, got.TicketStockID)
		assert.Equal(t, s.f.Price(), got.Price)
		assert.True(t, got.CreateTime.Equal(at))
	})

	t.Run("listed twice", func(t *testing.T) {
		// @spec components/entity/resale-listing/create "Listed twice"
		s := seedSoldOutType(t, 2)

		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := range errs {
			wg.Go(func() {
				_, errs[i] = repo.Create(ctx, s.newListing(0, at))
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

		_, err = repo.Create(ctx, s.newListing(0, at))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		listings, err := repo.ListByUser(ctx, s.seller)
		require.NoError(t, err)
		assert.Empty(t, listings)
	})

	t.Run("admitted ticket", func(t *testing.T) {
		s := seedSoldOutType(t, 1)
		_, err := testDB.Pool.Exec(ctx, `UPDATE tickets SET admit_at = $2 WHERE id = $1`, string(s.tickets[0].ID), at)
		require.NoError(t, err)

		_, err = repo.Create(ctx, s.newListing(0, at))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("listed again after withdrawing", func(t *testing.T) {
		// @spec components/entity/resale-listing/create "Listed again after withdrawing"
		s := seedSoldOutType(t, 1)
		first := s.list(t, 0, at)
		require.NoError(t, repo.Withdraw(ctx, first.ID, at.Add(time.Minute)))

		second, err := repo.Create(ctx, s.newListing(0, at.Add(2*time.Minute)))

		require.NoError(t, err)
		assert.Equal(t, entity.ResaleListingStatusListed, second.Status)
		assert.NotEqual(t, first.ID, second.ID)
	})

	t.Run("unknown ticket", func(t *testing.T) {
		s := seedSoldOutType(t, 1)
		ghost := &entity.Ticket{ID: entity.TicketID(entity.NewID()), OrderID: s.tickets[0].OrderID, UserID: s.seller}

		_, err := repo.Create(ctx, entity.NewResaleListing(ghost, s.f.ID, s.f.SeatType, at))

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
		assert.Equal(t, listed.TicketStockID, got.TicketStockID)
		assert.Equal(t, listed.Price, got.Price)
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
	ctx := context.Background()
	at := saleWindowStart.Add(24 * time.Hour)

	t.Run("fan withdraws an open listing", func(t *testing.T) {
		// @spec components/entity/resale-listing/withdraw "Fan withdraws an open listing"
		s := seedSoldOutType(t, 1)
		listed := s.list(t, 0, at)

		require.NoError(t, repo.Withdraw(ctx, listed.ID, at.Add(time.Minute)))

		got, err := repo.Get(ctx, listed.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ResaleListingStatusWithdrawn, got.Status)
		require.NotNil(t, got.WithdrawTime)
		assert.True(t, got.WithdrawTime.Equal(at.Add(time.Minute)))
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
		got, err := repo.ListByUser(ctx, entity.UserID(saleFan(t)))

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
