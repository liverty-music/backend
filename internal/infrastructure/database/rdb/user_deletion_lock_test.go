package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// waitForAdvisoryLockWaiter waits until some session waits for an advisory
// lock, so the deletion under test removes the user only once the create
// under test is blocked on it.
func waitForAdvisoryLockWaiter(db *rdb.Database, ctx context.Context, t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool {
		var waiting int
		err := db.Pool.QueryRow(ctx,
			`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`).Scan(&waiting)
		return err == nil && waiting > 0
	}, 10*time.Second, 10*time.Millisecond)
}

// TestUserRepository_SerializeDeletion checks that each create of a record
// that blocks a deletion, started while the user's deletion holds the lock,
// waits for it and then stores nothing for the deleted user.
func TestUserRepository_SerializeDeletion(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	users := rdb.NewUserRepository(db, testOutbox)
	ctx := context.Background()

	cases := []struct {
		name string
		// create seeds what the create needs for the user, then returns the
		// create to run and a count of the records it may have stored.
		create func(t *testing.T, userID entity.UserID) (run func() error, stored func() int)
	}{
		{
			name: "lottery entry",
			create: func(t *testing.T, userID entity.UserID) (func() error, func() int) {
				f := seedStock(t, db, entity.TicketSaleMethodLottery, 10, 4)
				stock, err := rdb.NewTicketStockRepository(db).Get(ctx, f.ID, saleWindowStart)
				require.NoError(t, err)
				repo := rdb.NewLotteryEntryRepository(db, testOutbox)
				return func() error {
						order := entity.NewPendingOrder(userID, stock, 1, nil, entity.OrderPaymentProvider)
						order.CardAuthorizationID = newCardAuthorizationID(db)
						_, err := repo.Create(ctx, entity.NewLotteryEntry(order, f.SaleID), time.Now())
						return err
					}, func() int {
						return countUserRows(db, ctx, t, `SELECT count(*) FROM orders WHERE user_id = $1`, string(userID))
					}
			},
		},
		{
			name: "checkout",
			create: func(t *testing.T, userID entity.UserID) (func() error, func() int) {
				f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 10, 4)
				repo := rdb.NewCheckoutRepository(db, testOutbox)
				return func() error {
						_, err := repo.GetOrCreateHeld(ctx, f.ID, f.SaleID, userID, 1, nil, saleWindowStart.Add(time.Hour))
						return err
					}, func() int {
						return countUserRows(db, ctx, t, `SELECT count(*) FROM orders WHERE user_id = $1`, string(userID))
					}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+" during a deletion", func(t *testing.T) {
			userID := entity.UserID(seedFanUser(t, db))
			run, stored := tc.create(t, userID)
			assertCreateBlockedByDeletion(db, ctx, t, users, string(userID), run)
			assert.Zero(t, stored())
		})
	}

	// A resale listing's user is the fan whose ticket of a sold-out stock is
	// listed.
	t.Run("resale listing during a deletion", func(t *testing.T) {
		f := seedStock(t, db, entity.TicketSaleMethodFirstCome, 1, 4)
		seller := seedFanUser(t, db)
		ticketID := seedIssuedTicketsOf(t, db, f, seller, 1)[0]
		var orderID string
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT order_id FROM tickets WHERE id = $1`, string(ticketID)).Scan(&orderID))
		ticket := &entity.Ticket{ID: ticketID, OrderID: entity.OrderID(orderID), UserID: entity.UserID(seller), EventID: f.EventID}
		repo := rdb.NewResaleListingRepository(db, testOutbox)
		run := func() error {
			_, err := repo.Create(ctx, entity.NewResaleListing(ticket, f.ID, f.SeatType, saleWindowStart.Add(24*time.Hour)))
			return err
		}
		assertCreateBlockedByDeletion(db, ctx, t, users, seller, run)
		assert.Zero(t, countUserRows(db, ctx, t, `SELECT count(*) FROM resale_listings WHERE user_id = $1`, seller))
	})
}

// assertCreateBlockedByDeletion deletes the user under its deletion lock,
// starting create while the lock is held and removing the user only once
// create waits for the lock, and checks that create then fails with NotFound.
func assertCreateBlockedByDeletion(db *rdb.Database, ctx context.Context, t *testing.T, users *rdb.UserRepository, userID string, create func() error) {
	t.Helper()
	createErr := make(chan error, 1)
	err := users.SerializeDeletion(ctx, userID, func(ctx context.Context) error {
		go func() { createErr <- create() }()
		waitForAdvisoryLockWaiter(db, ctx, t)
		return users.Delete(ctx, userID)
	})
	require.NoError(t, err)

	select {
	case err := <-createErr:
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	case <-time.After(10 * time.Second):
		t.Fatal("the create did not finish after the deletion released its lock")
	}
}

// countUserRows runs a count query and returns its result.
func countUserRows(db *rdb.Database, ctx context.Context, t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, db.Pool.QueryRow(ctx, query, args...).Scan(&n))
	return n
}
