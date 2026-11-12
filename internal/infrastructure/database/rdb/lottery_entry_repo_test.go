package rdb_test

import (
	"context"
	"github.com/jackc/pgx/v5/pgconn"
	"slices"
	"sync"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

// stockOf returns the TicketStock of the fixture as a repository read gives
// it, with its SeatType and nothing sold or held.
func stockOf(f *stockFixture) *entity.TicketStock {
	return &entity.TicketStock{
		ID: f.ID, EventID: f.EventID, SeatTypeID: f.SeatType.ID, Quantity: f.Quantity, SeatType: f.SeatType,
	}
}

// newEntry returns a new Entered entry in the sale with its Pending Order of
// the user for count tickets of the fixture's stock, made from a fresh card
// authorization.
func newEntry(db *rdb.Database, f *stockFixture, saleID entity.TicketSaleID, userID entity.UserID, count int, code *entity.ReferralCode) *entity.LotteryEntry {
	order := entity.NewPendingOrder(userID, stockOf(f), count, code, entity.OrderPaymentProvider)
	order.CardAuthorizationID = newCardAuthorizationID(db)
	return entity.NewLotteryEntry(order, saleID)
}

// insertLotteryEntry stores, without the repository's checks, an entry in the
// sale in the given state with its Order of the user for count tickets of the
// fixture's stock: Void for a Lost entry, Pending otherwise. It returns the
// entry as a repository read gives it.
func insertLotteryEntry(t *testing.T, db *rdb.Database, f *stockFixture, saleID entity.TicketSaleID, userID entity.UserID, count int, state entity.LotteryEntryState) *entity.LotteryEntry {
	t.Helper()
	e := newEntry(db, f, saleID, userID, count, nil)
	e.State = state
	if state == entity.LotteryEntryStateLost {
		e.Order.Status = entity.OrderStatusVoid
	}
	o := e.Order
	ctx := context.Background()
	_, err := db.Pool.Exec(ctx, `
		INSERT INTO orders (id, user_id, ticket_stock_id, ticket_count, card_authorization_id, provider, status, amount, currency)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, string(o.ID), string(o.UserID), string(o.TicketStockID), o.TicketCount, o.CardAuthorizationID,
		int16(o.Payment.Provider), int16(o.Status), o.Amount, o.Currency)
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, `
		INSERT INTO lottery_entries (id, order_id, ticket_sale_id, state) VALUES ($1, $2, $3, $4)
	`, string(e.ID), string(o.ID), string(saleID), int16(state))
	require.NoError(t, err)
	return e
}

// newUserID seeds a fan user and returns its id: LotteryEntry.Create stores
// nothing for a user that does not exist.
func newUserID(t *testing.T, db *rdb.Database) entity.UserID {
	t.Helper()
	return entity.UserID(seedFanUser(t, db))
}

// setSaleEndByDatabaseClock moves the sale's window so that it opened an hour
// before the database's current time and ends after the given interval from
// it (negative: already ended). LotteryEntry.Create reads the database clock,
// so the tests that store an entry place the window around it.
func setSaleEndByDatabaseClock(t *testing.T, db *rdb.Database, saleID entity.TicketSaleID, end time.Duration) {
	t.Helper()
	_, err := db.Pool.Exec(context.Background(), `
		UPDATE ticket_sales
		SET sale_start_at = now() - interval '1 hour' + least($2::interval, interval '0'),
		    sale_end_at = now() + $2::interval
		WHERE id = $1`, string(saleID), end)
	require.NoError(t, err)
}

// openLotteryStock seeds a stock of 100 tickets of an event with a
// per-account limit of 4, and a Lottery sale naming the event that is open
// by the database clock for another day.
func openLotteryStock(t *testing.T, db *rdb.Database) *stockFixture {
	t.Helper()
	f := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
	setSaleEndByDatabaseClock(t, db, f.SaleID, 24*time.Hour)
	return f
}

// seedSiblingEvent inserts another event of the Series of eventID, at the
// same venue a day later per call, and returns its id.
func seedSiblingEvent(t *testing.T, db *rdb.Database, eventID string) string {
	t.Helper()
	id := uuid.NewV7().String()
	_, err := db.Pool.Exec(context.Background(), `
		INSERT INTO events (id, series_id, venue_id, local_event_date, publish_state, per_account_limit)
		SELECT $1, series_id, venue_id,
			local_event_date + 1 + (SELECT count(*)::int FROM events e2 WHERE e2.series_id = events.series_id),
			publish_state, per_account_limit
		FROM events WHERE id = $2
	`, id, eventID)
	require.NoError(t, err)
	return id
}

// seedLotteryOfTwoEvents seeds two events of one Series, a stock of 100
// tickets of each, and one Lottery sale naming both. It returns the sale and
// the two stocks; each stock's own SaleID is another Lottery sale that names
// only its event.
func seedLotteryOfTwoEvents(t *testing.T, db *rdb.Database) (entity.TicketSaleID, *stockFixture, *stockFixture) {
	t.Helper()
	return seedLotteryOfTwoStocks(t, db, 100, 100)
}

// seedLotteryOfTwoStocks is seedLotteryOfTwoEvents with stocks of qa and qb
// tickets.
func seedLotteryOfTwoStocks(t *testing.T, db *rdb.Database, qa, qb int) (entity.TicketSaleID, *stockFixture, *stockFixture) {
	t.Helper()
	a := seedStock(t, db, entity.TicketSaleMethodLottery, qa, 4)
	b := seedStockForEvent(t, db, seedSiblingEvent(t, db, a.EventID), entity.TicketSaleMethodLottery, qb, 4)
	_, err := db.Pool.Exec(context.Background(),
		`INSERT INTO ticket_sale_events (ticket_sale_id, event_id) VALUES ($1, $2)`, string(a.SaleID), b.EventID)
	require.NoError(t, err)
	return a.SaleID, a, b
}

// orderStatus reads the status of the Order.
func orderStatus(t *testing.T, db *rdb.Database, id entity.OrderID) entity.OrderStatus {
	t.Helper()
	var status int16
	require.NoError(t, db.Pool.QueryRow(context.Background(), `SELECT status FROM orders WHERE id = $1`, string(id)).Scan(&status))
	return entity.OrderStatus(status)
}

// countOrdersOfUser counts every Order of the user, in any status.
func countOrdersOfUser(t *testing.T, db *rdb.Database, userID entity.UserID) int {
	t.Helper()
	var n int
	require.NoError(t, db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM orders WHERE user_id = $1`, string(userID)).Scan(&n))
	return n
}

func TestLotteryEntryRepository_Create(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	repo := rdb.NewLotteryEntryRepository(db, testOutbox)
	ctx := context.Background()

	t.Run("entry stored", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Entry stored"
		f := openLotteryStock(t, db)
		code := entity.ReferralCode("member-a")
		entry := newEntry(db, f, f.SaleID, newUserID(t, db), 2, &code)

		got, err := repo.Create(ctx, entry, time.Now())

		require.NoError(t, err)
		assert.Equal(t, entity.LotteryEntryStateEntered, got.State)
		assert.Nil(t, got.DrawPosition)
		assert.Equal(t, f.SaleID, got.TicketSaleID)
		require.NotNil(t, got.Order)
		assert.Equal(t, entity.OrderStatusPending, got.Order.Status)
		assert.Equal(t, 2, got.Order.TicketCount)
		assert.Equal(t, f.ID, got.Order.TicketStockID)
		assert.Equal(t, entry.Order.CardAuthorizationID, got.Order.CardAuthorizationID)
		assert.Equal(t, &code, got.Order.ReferralCode)
		assert.Equal(t, f.Price()*2, got.Order.Amount)
		stored, err := repo.Get(ctx, got.ID)
		require.NoError(t, err)
		assert.Equal(t, got, stored)
	})

	t.Run("second entry", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Second entry"
		for _, state := range []entity.LotteryEntryState{entity.LotteryEntryStateEntered, entity.LotteryEntryStateWon, entity.LotteryEntryStateLost} {
			f := openLotteryStock(t, db)
			// Another stock of the same event, which the sale also sells.
			other := seedStockForEvent(t, db, f.EventID, entity.TicketSaleMethodLottery, 100, 4)
			userID := newUserID(t, db)
			first := insertLotteryEntry(t, db, f, f.SaleID, userID, 2, state)

			for _, stock := range []*stockFixture{f, other} {
				_, err := repo.Create(ctx, newEntry(db, stock, f.SaleID, userID, 1, nil), time.Now())

				assert.ErrorIs(t, err, apperr.ErrAlreadyExists, state.String())
			}
			all, err := repo.ListByTicketSaleAndUser(ctx, f.SaleID, userID)
			require.NoError(t, err)
			assert.Equal(t, []*entity.LotteryEntry{first}, all, "nothing is stored")
			assert.Equal(t, 1, countOrdersOfUser(t, db, userID), "no Order is stored")
		}
	})

	t.Run("double tap", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Double tap"
		f := openLotteryStock(t, db)
		userID := newUserID(t, db)

		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := range errs {
			wg.Go(func() {
				_, errs[i] = repo.Create(ctx, newEntry(db, f, f.SaleID, userID, 2, nil), time.Now())
			})
		}
		wg.Wait()

		var stored, refused int
		for _, err := range errs {
			switch {
			case err == nil:
				stored++
			case assert.ErrorIs(t, err, apperr.ErrAlreadyExists):
				refused++
			}
		}
		assert.Equal(t, 1, stored)
		assert.Equal(t, 1, refused)
		all, err := repo.ListByTicketSaleAndUser(ctx, f.SaleID, userID)
		require.NoError(t, err)
		assert.Len(t, all, 1)
		assert.Equal(t, 1, countOrdersOfUser(t, db, userID))
	})

	t.Run("another date of a tour presale", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Another date of a tour presale"
		saleID, a, b := seedLotteryOfTwoEvents(t, db)
		setSaleEndByDatabaseClock(t, db, saleID, 24*time.Hour)
		userID := newUserID(t, db)
		insertLotteryEntry(t, db, a, saleID, userID, 2, entity.LotteryEntryStateEntered)

		got, err := repo.Create(ctx, newEntry(db, b, saleID, userID, 2, nil), time.Now())

		require.NoError(t, err)
		assert.Equal(t, b.ID, got.Order.TicketStockID)
		assert.Equal(t, entity.LotteryEntryStateEntered, got.State)
		assert.Equal(t, entity.OrderStatusPending, orderStatus(t, db, got.OrderID))
		all, err := repo.ListByTicketSaleAndUser(ctx, saleID, userID)
		require.NoError(t, err)
		assert.Len(t, all, 2)
	})

	t.Run("same card authorization again", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Same card authorization again"
		f := openLotteryStock(t, db)
		userID := newUserID(t, db)
		entry := newEntry(db, f, f.SaleID, userID, 2, nil)
		first, err := repo.Create(ctx, entry, time.Now())
		require.NoError(t, err)
		stored, err := repo.Get(ctx, first.ID)
		require.NoError(t, err)

		// again returns a new entry for the same card authorization.
		again := func(f *stockFixture, cardAuthorizationID string) *entity.LotteryEntry {
			e := newEntry(db, f, f.SaleID, userID, 2, nil)
			e.Order.CardAuthorizationID = cardAuthorizationID
			return e
		}

		// Before the end time: the same entry, nothing else stored.
		got, err := repo.Create(ctx, again(f, entry.Order.CardAuthorizationID), time.Now())
		require.NoError(t, err)
		assert.Equal(t, stored, got)

		// After the end time: still the same entry.
		setSaleEndByDatabaseClock(t, db, f.SaleID, -time.Minute)
		late, err := repo.Create(ctx, again(f, entry.Order.CardAuthorizationID), time.Now())
		require.NoError(t, err)
		assert.Equal(t, stored, late)
		assert.Equal(t, 1, countOrdersOfUser(t, db, userID))

		// Twice at the same time: one entry, returned to both.
		other := openLotteryStock(t, db)
		cardAuthorizationID := newCardAuthorizationID(db)
		var wg sync.WaitGroup
		results := make([]*entity.LotteryEntry, 2)
		errs := make([]error, 2)
		for i := range results {
			wg.Go(func() {
				results[i], errs[i] = repo.Create(ctx, again(other, cardAuthorizationID), time.Now())
			})
		}
		wg.Wait()
		require.NoError(t, errs[0])
		require.NoError(t, errs[1])
		assert.Equal(t, results[0].ID, results[1].ID)
		assert.Equal(t, results[0].OrderID, results[1].OrderID)
		all, err := repo.ListByTicketSaleAndUser(ctx, other.SaleID, userID)
		require.NoError(t, err)
		assert.Len(t, all, 1)
		assert.Equal(t, 2, countOrdersOfUser(t, db, userID))
	})

	t.Run("presale winner in a general lottery", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Presale winner in a general lottery"
		presale := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
		general := seedSale(t, db, presale.EventID, entity.TicketSaleMethodLottery, saleWindowStart, saleWindowStart.Add(time.Hour))
		setSaleEndByDatabaseClock(t, db, general, 24*time.Hour)
		winner := newUserID(t, db)
		seedIssuedTicketsOf(t, db, presale, string(winner), 4)

		_, err := repo.Create(ctx, newEntry(db, presale, general, winner, 1, nil), time.Now())

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		all, err := repo.ListByTicketSaleAndUser(ctx, general, winner)
		require.NoError(t, err)
		assert.Empty(t, all, "nothing is stored")
		assert.Equal(t, 1, countOrdersOfUser(t, db, winner), "only the presale Order exists")

		// A presale winner of 2 tickets may still enter for 2.
		below := newUserID(t, db)
		seedIssuedTicketsOf(t, db, presale, string(below), 2)
		_, err = repo.Create(ctx, newEntry(db, presale, general, below, 2, nil), time.Now())
		assert.NoError(t, err)
	})

	t.Run("pending entry counts toward the limit", func(t *testing.T) {
		// An Entered entry's Pending Order in a presale counts as held.
		presale := openLotteryStock(t, db)
		general := seedSale(t, db, presale.EventID, entity.TicketSaleMethodLottery, saleWindowStart, saleWindowStart.Add(time.Hour))
		setSaleEndByDatabaseClock(t, db, general, 24*time.Hour)
		userID := newUserID(t, db)
		insertLotteryEntry(t, db, presale, presale.SaleID, userID, 3, entity.LotteryEntryStateEntered)

		_, err := repo.Create(ctx, newEntry(db, presale, general, userID, 2, nil), time.Now())

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		_, err = repo.Create(ctx, newEntry(db, presale, general, userID, 1, nil), time.Now())
		assert.NoError(t, err)
	})

	t.Run("count above the per-account limit", func(t *testing.T) {
		f := openLotteryStock(t, db)
		userID := newUserID(t, db)

		_, err := repo.Create(ctx, newEntry(db, f, f.SaleID, userID, f.Limit+1, nil), time.Now())

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Zero(t, countOrdersOfUser(t, db, userID))
	})

	t.Run("another seat type", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Another seat type"
		sSeat := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
		aSeat := seedStockForEvent(t, db, sSeat.EventID, entity.TicketSaleMethodLottery, 100, 4)
		setSaleEndByDatabaseClock(t, db, aSeat.SaleID, 24*time.Hour)
		userID := newUserID(t, db)
		seedIssuedTicketsOf(t, db, sSeat, string(userID), 2)

		_, err := repo.Create(ctx, newEntry(db, aSeat, aSeat.SaleID, userID, 1, nil), time.Now())

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		all, err := repo.ListByTicketSaleAndUser(ctx, aSeat.SaleID, userID)
		require.NoError(t, err)
		assert.Empty(t, all, "nothing is stored")

		// The same seat type in the same sale is accepted.
		_, err = repo.Create(ctx, newEntry(db, sSeat, aSeat.SaleID, userID, 2, nil), time.Now())
		assert.NoError(t, err)
	})

	t.Run("after the end time", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "After the end time"
		f := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
		setSaleEndByDatabaseClock(t, db, f.SaleID, -time.Second)
		userID := newUserID(t, db)

		_, err := repo.Create(ctx, newEntry(db, f, f.SaleID, userID, 2, nil), time.Now())

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		all, err := repo.ListByTicketSaleAndUser(ctx, f.SaleID, userID)
		require.NoError(t, err)
		assert.Empty(t, all)
		assert.Zero(t, countOrdersOfUser(t, db, userID), "no Order is stored")
	})

	t.Run("unknown ticket stock", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Unknown ticket stock"
		f := openLotteryStock(t, db)
		unknown := *f
		unknown.ID = entity.TicketStockID(entity.NewID())
		userID := newUserID(t, db)
		entry := newEntry(db, &unknown, f.SaleID, userID, 2, nil)

		_, err := repo.Create(ctx, entry, time.Now())

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		_, err = repo.GetByCardAuthorizationID(ctx, entry.Order.CardAuthorizationID)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.Zero(t, countOrdersOfUser(t, db, userID))
	})
}

func TestLotteryEntryRepository_Get(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	repo := rdb.NewLotteryEntryRepository(db, testOutbox)
	ctx := context.Background()
	f := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)

	t.Run("existing entry", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get "Existing entry"
		for _, state := range []entity.LotteryEntryState{entity.LotteryEntryStateEntered, entity.LotteryEntryStateWon, entity.LotteryEntryStateLost} {
			e := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 3, state)

			got, err := repo.Get(ctx, e.ID)

			require.NoError(t, err)
			assert.Equal(t, e, got)
		}
	})

	t.Run("unknown id", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get "Unknown id"
		_, err := repo.Get(ctx, entity.LotteryEntryID(entity.NewID()))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestLotteryEntryRepository_GetByCardAuthorizationID(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	repo := rdb.NewLotteryEntryRepository(db, testOutbox)
	ctx := context.Background()
	f := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)

	t.Run("charged win", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-by-card-authorization-id "Charged win"
		won := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateWon)
		insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateWon) // another entry

		got, err := repo.GetByCardAuthorizationID(ctx, won.Order.CardAuthorizationID)

		require.NoError(t, err)
		assert.Equal(t, won, got)
	})

	t.Run("unknown card authorization", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-by-card-authorization-id "Unknown card authorization"
		_, err := repo.GetByCardAuthorizationID(ctx, entity.NewID())

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestLotteryEntryRepository_ListByTicketSaleAndUser(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	repo := rdb.NewLotteryEntryRepository(db, testOutbox)
	ctx := context.Background()

	t.Run("entry of the user", func(t *testing.T) {
		// @spec components/entity/lottery-entry/list-by-ticket-sale-and-user "Entry of the user"
		saleID, a, b := seedLotteryOfTwoEvents(t, db)
		userID := newUserID(t, db)
		entered := insertLotteryEntry(t, db, a, saleID, userID, 2, entity.LotteryEntryStateEntered)
		insertLotteryEntry(t, db, a, saleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered) // another user
		insertLotteryEntry(t, db, b, b.SaleID, userID, 2, entity.LotteryEntryStateEntered)         // another sale

		got, err := repo.ListByTicketSaleAndUser(ctx, saleID, userID)

		require.NoError(t, err)
		assert.Equal(t, []*entity.LotteryEntry{entered}, got)
	})

	t.Run("one entry for each event", func(t *testing.T) {
		saleID, a, b := seedLotteryOfTwoEvents(t, db)
		userID := newUserID(t, db)
		first := insertLotteryEntry(t, db, a, saleID, userID, 2, entity.LotteryEntryStateWon)
		second := insertLotteryEntry(t, db, b, saleID, userID, 1, entity.LotteryEntryStateLost)

		got, err := repo.ListByTicketSaleAndUser(ctx, saleID, userID)

		require.NoError(t, err)
		assert.Equal(t, []*entity.LotteryEntry{second, first}, got, "newest first")
	})

	t.Run("never entered", func(t *testing.T) {
		// @spec components/entity/lottery-entry/list-by-ticket-sale-and-user "Never entered"
		f := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
		insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered) // another user

		got, err := repo.ListByTicketSaleAndUser(ctx, f.SaleID, newUserID(t, db))

		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})
}

func TestLotteryEntryRepository_ListByUser(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	repo := rdb.NewLotteryEntryRepository(db, testOutbox)
	ctx := context.Background()

	t.Run("entries for two ticket types", func(t *testing.T) {
		// @spec components/entity/lottery-entry/list-by-user "Entries for two ticket types"
		f := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
		other := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
		userID := newUserID(t, db)
		entered := insertLotteryEntry(t, db, f, f.SaleID, userID, 2, entity.LotteryEntryStateEntered)
		lost := insertLotteryEntry(t, db, other, other.SaleID, userID, 1, entity.LotteryEntryStateLost)
		insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered) // another user

		got, err := repo.ListByUser(ctx, userID)

		require.NoError(t, err)
		assert.Equal(t, []*entity.LotteryEntry{lost, entered}, got)
	})

	t.Run("never entered", func(t *testing.T) {
		// @spec components/entity/lottery-entry/list-by-user "Never entered"
		got, err := repo.ListByUser(ctx, newUserID(t, db))

		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})
}

func TestLotteryEntryRepository_ListEnteredByTicketSaleAndTicketStock(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	repo := rdb.NewLotteryEntryRepository(db, testOutbox)
	ctx := context.Background()

	t.Run("mixed states", func(t *testing.T) {
		// @spec components/entity/lottery-entry/list-entered-by-ticket-sale-and-ticket-stock "Mixed states"
		saleID, a, b := seedLotteryOfTwoEvents(t, db)
		x := insertLotteryEntry(t, db, a, saleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)
		y := insertLotteryEntry(t, db, a, saleID, newUserID(t, db), 1, entity.LotteryEntryStateEntered)
		insertLotteryEntry(t, db, a, saleID, newUserID(t, db), 3, entity.LotteryEntryStateWon)
		insertLotteryEntry(t, db, a, saleID, newUserID(t, db), 2, entity.LotteryEntryStateLost)
		insertLotteryEntry(t, db, b, saleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered) // another stock
		otherSale := seedSale(t, db, a.EventID, entity.TicketSaleMethodLottery, saleWindowStart, saleWindowStart.Add(time.Hour))
		insertLotteryEntry(t, db, a, otherSale, newUserID(t, db), 2, entity.LotteryEntryStateEntered) // another sale

		got, err := repo.ListEnteredByTicketSaleAndTicketStock(ctx, saleID, a.ID)

		require.NoError(t, err)
		assert.ElementsMatch(t, []*entity.LotteryEntry{x, y}, got)
	})

	t.Run("no candidates", func(t *testing.T) {
		// @spec components/entity/lottery-entry/list-entered-by-ticket-sale-and-ticket-stock "No candidates"
		f := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
		insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateWon)
		insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateLost)

		got, err := repo.ListEnteredByTicketSaleAndTicketStock(ctx, f.SaleID, f.ID)

		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})
}

func TestLotteryEntryRepository_GetStatsByTicketSaleAndTicketStock(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	repo := rdb.NewLotteryEntryRepository(db, testOutbox)
	ctx := context.Background()

	t.Run("after the draw", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-stats-by-ticket-sale-and-ticket-stock "After the draw"
		f := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
		insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateWon)
		insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateWon)
		insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 4, entity.LotteryEntryStateLost)

		got, err := repo.GetStatsByTicketSaleAndTicketStock(ctx, f.SaleID, f.ID)

		require.NoError(t, err)
		assert.Equal(t, entity.LotteryEntryStats{
			EntryCount: 3, TicketCount: 8, WonEntryCount: 2, WonTicketCount: 4, LostEntryCount: 1,
		}, got)
	})

	t.Run("before the draw", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-stats-by-ticket-sale-and-ticket-stock "Before the draw"
		f := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
		insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)
		insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 3, entity.LotteryEntryStateEntered)
		// An entry of another sale of the stock's event is not counted.
		otherSale := seedSale(t, db, f.EventID, entity.TicketSaleMethodLottery, saleWindowStart, saleWindowStart.Add(time.Hour))
		insertLotteryEntry(t, db, f, otherSale, newUserID(t, db), 4, entity.LotteryEntryStateEntered)

		got, err := repo.GetStatsByTicketSaleAndTicketStock(ctx, f.SaleID, f.ID)

		require.NoError(t, err)
		assert.Equal(t, entity.LotteryEntryStats{EntryCount: 2, TicketCount: 5}, got)
	})

	t.Run("no entries", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-stats-by-ticket-sale-and-ticket-stock "No entries"
		f := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
		other := seedStockForEvent(t, db, f.EventID, entity.TicketSaleMethodLottery, 100, 4)
		insertLotteryEntry(t, db, other, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)

		got, err := repo.GetStatsByTicketSaleAndTicketStock(ctx, f.SaleID, f.ID)

		require.NoError(t, err)
		assert.Equal(t, entity.LotteryEntryStats{}, got)
	})
}

// drawOrder is the draw order of the fixture's stock: the entries in the
// given order, each with its Order's ticket count.
func drawOrder(f *stockFixture, entries ...*entity.LotteryEntry) entity.StockDrawOrder {
	o := entity.StockDrawOrder{TicketStockID: f.ID}
	for _, e := range entries {
		o.Entries = append(o.Entries, entity.DrawEntry{ID: e.ID, TicketCount: e.Order.TicketCount})
	}
	return o
}

func TestLotteryEntryRepository_PersistDrawOutcome(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	repo := rdb.NewLotteryEntryRepository(db, testOutbox)
	sales := rdb.NewTicketSaleRepository(db, testOutbox)
	ctx := context.Background()

	// get reads an entry back.
	get := func(t *testing.T, id entity.LotteryEntryID) *entity.LotteryEntry {
		t.Helper()
		e, err := repo.Get(ctx, id)
		require.NoError(t, err)
		return e
	}
	// drawn reports whether the closed sale is drawn: no longer due for the
	// draw a month after every seeded window.
	drawn := func(t *testing.T, id entity.TicketSaleID) bool {
		t.Helper()
		due, err := sales.ListDueForDraw(ctx, saleWindowStart.Add(60*24*time.Hour))
		require.NoError(t, err)
		return !slices.ContainsFunc(due, func(s *entity.TicketSale) bool { return s.ID == id })
	}
	position := func(p int) *int { return &p }

	t.Run("outcome recorded", func(t *testing.T) {
		// @spec components/entity/lottery-entry/persist-draw-outcome "Outcome recorded"
		saleID, a, b := seedLotteryOfTwoStocks(t, db, 3, 2)
		winA := insertLotteryEntry(t, db, a, saleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)
		loseA := insertLotteryEntry(t, db, a, saleID, newUserID(t, db), 4, entity.LotteryEntryStateEntered)
		loseB := insertLotteryEntry(t, db, b, saleID, newUserID(t, db), 3, entity.LotteryEntryStateEntered)
		winB := insertLotteryEntry(t, db, b, saleID, newUserID(t, db), 1, entity.LotteryEntryStateEntered)
		require.False(t, drawn(t, saleID))

		err := repo.PersistDrawOutcome(ctx, saleID, []entity.StockDrawOrder{drawOrder(a, winA, loseA), drawOrder(b, loseB, winB)}, time.Now())

		require.NoError(t, err)
		for _, tc := range []struct {
			entry    *entity.LotteryEntry
			state    entity.LotteryEntryState
			position int
			status   entity.OrderStatus
		}{
			{winA, entity.LotteryEntryStateWon, 0, entity.OrderStatusPending},
			{loseA, entity.LotteryEntryStateLost, 1, entity.OrderStatusVoid},
			{loseB, entity.LotteryEntryStateLost, 0, entity.OrderStatusVoid},
			{winB, entity.LotteryEntryStateWon, 1, entity.OrderStatusPending},
		} {
			got := get(t, tc.entry.ID)
			assert.Equal(t, tc.state, got.State)
			assert.Equal(t, position(tc.position), got.DrawPosition)
			assert.Equal(t, tc.status, got.Order.Status)
			wantReason := entity.LotteryEntryLostReasonUnspecified
			if tc.state == entity.LotteryEntryStateLost {
				wantReason = entity.LotteryEntryLostReasonNotDrawn
			}
			assert.Equal(t, wantReason, got.LostReason)
		}
		assert.True(t, drawn(t, saleID))
	})

	t.Run("failure leaves nothing recorded", func(t *testing.T) {
		// @spec components/entity/lottery-entry/persist-draw-outcome "Failure leaves nothing recorded"
		saleID, a, b := seedLotteryOfTwoStocks(t, db, 100, 1)
		won := insertLotteryEntry(t, db, a, saleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)
		lost := insertLotteryEntry(t, db, b, saleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)
		// The last write fails: voiding the loser's Order is refused.
		failOrderUpdate(t, db, lost.OrderID)

		err := repo.PersistDrawOutcome(ctx, saleID, []entity.StockDrawOrder{drawOrder(a, won), drawOrder(b, lost)}, time.Now())

		require.Error(t, err)
		assert.Equal(t, won, get(t, won.ID))
		assert.Equal(t, lost, get(t, lost.ID))
		assert.False(t, drawn(t, saleID))
	})

	t.Run("unknown sale", func(t *testing.T) {
		f := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
		e := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)

		err := repo.PersistDrawOutcome(ctx, entity.TicketSaleID(entity.NewID()), []entity.StockDrawOrder{drawOrder(f, e)}, time.Now())

		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.Equal(t, e, get(t, e.ID))
	})

	t.Run("empty draw", func(t *testing.T) {
		// @spec components/entity/lottery-entry/persist-draw-outcome "Empty draw"
		f := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
		bystander := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)

		require.NoError(t, repo.PersistDrawOutcome(ctx, f.SaleID, nil, time.Now()))

		assert.Equal(t, bystander, get(t, bystander.ID))
	})

	t.Run("recorded again", func(t *testing.T) {
		// @spec components/entity/lottery-entry/persist-draw-outcome "Recorded again"
		f := seedStock(t, db, entity.TicketSaleMethodLottery, 2, 4)
		won := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)
		lost := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)
		order := []entity.StockDrawOrder{drawOrder(f, won, lost)}
		require.NoError(t, repo.PersistDrawOutcome(ctx, f.SaleID, order, time.Now()))
		// The winner is made Lost since, which frees its 2 tickets.
		require.NoError(t, repo.RevertWin(ctx, won.ID, entity.LotteryEntryLostReasonCardNotCharged))
		wonBefore, lostBefore := get(t, won.ID), get(t, lost.ID)

		// The same order again, and one that would put the loser first.
		require.NoError(t, repo.PersistDrawOutcome(ctx, f.SaleID, order, time.Now()))
		require.NoError(t, repo.PersistDrawOutcome(ctx, f.SaleID, []entity.StockDrawOrder{drawOrder(f, lost, won)}, time.Now()))

		assert.Equal(t, wonBefore, get(t, won.ID))
		assert.Equal(t, lostBefore, get(t, lost.ID))
		assert.Equal(t, entity.LotteryEntryStateLost, lostBefore.State, "the loser is not admitted to the freed tickets")
	})

	t.Run("tickets taken since the order was fixed", func(t *testing.T) {
		// @spec components/entity/lottery-entry/persist-draw-outcome "Tickets taken since the order was fixed"
		f := seedStock(t, db, entity.TicketSaleMethodLottery, 4, 4)
		first := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)
		second := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)
		third := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 3, entity.LotteryEntryStateEntered)
		order := []entity.StockDrawOrder{drawOrder(f, first, second, third)}
		// A first-come checkout of the event commits 2 tickets after the
		// order was fixed.
		checkouts := rdb.NewCheckoutRepository(db, testOutbox)
		firstCome := seedSale(t, db, f.EventID, entity.TicketSaleMethodFirstCome, saleWindowStart, saleWindowStart.Add(24*time.Hour))
		at := saleWindowStart.Add(time.Hour)
		held, err := checkouts.GetOrCreateHeld(ctx, f.ID, firstCome, nextFan(t, db), 2, nil, at)
		require.NoError(t, err)
		outcome, err := checkouts.Commit(ctx, held.ID, at)
		require.NoError(t, err)
		require.Equal(t, entity.CommitOutcomeCommitted, outcome)

		require.NoError(t, repo.PersistDrawOutcome(ctx, f.SaleID, order, time.Now()))

		assert.Equal(t, entity.LotteryEntryStateWon, get(t, first.ID).State)
		assert.Equal(t, entity.LotteryEntryStateLost, get(t, second.ID).State)
		assert.Equal(t, entity.LotteryEntryStateLost, get(t, third.ID).State)
		sold, _ := stockCounts(t, db, f.ID, at)
		assert.Equal(t, 4, sold, "the sold count never exceeds the quantity")
	})

	t.Run("empty sale id", func(t *testing.T) {
		err := repo.PersistDrawOutcome(ctx, "", nil, time.Now())

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestLotteryEntryRepository_RevertWin(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewLotteryEntryRepository(db, testOutbox)
	issuance := rdb.NewIssuanceRepository(db, testOutbox)
	ctx := context.Background()

	// drawnWin records a draw of the fixture's stock that the entry wins at
	// draw position 3, behind three losers that do not fit.
	drawnWin := func(t *testing.T) (*entity.LotteryEntry, *stockFixture) {
		t.Helper()
		f := seedStock(t, db, entity.TicketSaleMethodLottery, 2, 4)
		var order []*entity.LotteryEntry
		for range 3 {
			order = append(order, insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 3, entity.LotteryEntryStateEntered))
		}
		won := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)
		require.NoError(t, repo.PersistDrawOutcome(ctx, f.SaleID, []entity.StockDrawOrder{drawOrder(f, append(order, won)...)}, time.Now()))
		got, err := repo.Get(ctx, won.ID)
		require.NoError(t, err)
		require.Equal(t, entity.LotteryEntryStateWon, got.State)
		return got, f
	}
	get := func(t *testing.T, id entity.LotteryEntryID) *entity.LotteryEntry {
		t.Helper()
		e, err := repo.Get(ctx, id)
		require.NoError(t, err)
		return e
	}
	drawnOf := func(t *testing.T, mark int64, id entity.LotteryEntryID) []*entityv1.LotteryEntry {
		t.Helper()
		return announcedSince(t, db, mark, entity.SubjectLotteryEntryLost, func() *entityv1.LotteryEntry { return &entityv1.LotteryEntry{} },
			func(e *entityv1.LotteryEntry) bool { return e.GetId().GetValue() == string(id) })
	}

	t.Run("win reverted", func(t *testing.T) {
		// @spec components/entity/lottery-entry/revert-win "Win reverted"
		won, f := drawnWin(t)

		require.NoError(t, repo.RevertWin(ctx, won.ID, entity.LotteryEntryLostReasonCardNotCharged))

		got := get(t, won.ID)
		assert.Equal(t, entity.LotteryEntryStateLost, got.State)
		assert.Equal(t, entity.LotteryEntryLostReasonCardNotCharged, got.LostReason)
		require.NotNil(t, got.DrawPosition)
		assert.Equal(t, 3, *got.DrawPosition)
		assert.Equal(t, entity.OrderStatusVoid, got.Order.Status)
		sold, _ := stockCounts(t, db, f.ID, time.Now())
		assert.Zero(t, sold, "its tickets stay unallocated")
	})

	t.Run("win reverted for a cancelled concert", func(t *testing.T) {
		// @spec components/entity/lottery-entry/revert-win "Win reverted for a cancelled concert"
		won, _ := drawnWin(t)
		mark := outboxMark(t, db)

		require.NoError(t, repo.RevertWin(ctx, won.ID, entity.LotteryEntryLostReasonConcertCancelled))

		got := get(t, won.ID)
		assert.Equal(t, entity.LotteryEntryStateLost, got.State)
		assert.Equal(t, entity.LotteryEntryLostReasonConcertCancelled, got.LostReason)
		assert.Equal(t, entity.OrderStatusVoid, got.Order.Status)
		announced := drawnOf(t, mark, won.ID)
		require.Len(t, announced, 1)
		assert.Equal(t, entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_CONCERT_CANCELLED, announced[0].GetLostReason(), "the announcement carries the reason RevertWin was given")
	})

	t.Run("already reverted", func(t *testing.T) {
		// @spec components/entity/lottery-entry/revert-win "Already reverted"
		won, _ := drawnWin(t)
		require.NoError(t, repo.RevertWin(ctx, won.ID, entity.LotteryEntryLostReasonCardNotCharged))
		before := get(t, won.ID)

		require.NoError(t, repo.RevertWin(ctx, won.ID, entity.LotteryEntryLostReasonCardNotCharged))

		assert.Equal(t, before, get(t, won.ID))
	})

	t.Run("win already issued", func(t *testing.T) {
		// @spec components/entity/lottery-entry/revert-win "Win already issued"
		won, f := drawnWin(t)
		order, issued, settlement := issuanceOf(t, db, won.Order, f.EventID)
		require.NoError(t, issuance.Issue(ctx, order, issued, settlement))
		before := get(t, won.ID)

		err := repo.RevertWin(ctx, won.ID, entity.LotteryEntryLostReasonCardNotCharged)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Equal(t, before, get(t, won.ID))
	})

	t.Run("not drawn", func(t *testing.T) {
		// @spec components/entity/lottery-entry/revert-win "Not drawn"
		f := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
		entered := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)

		err := repo.RevertWin(ctx, entered.ID, entity.LotteryEntryLostReasonCardNotCharged)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Equal(t, entered, get(t, entered.ID))
	})

	t.Run("unknown entry", func(t *testing.T) {
		// @spec components/entity/lottery-entry/revert-win "Unknown entry"
		err := repo.RevertWin(ctx, entity.LotteryEntryID(entity.NewID()), entity.LotteryEntryLostReasonCardNotCharged)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("win reverted and announced", func(t *testing.T) {
		// @spec components/entity/lottery-entry/revert-win "Win reverted and announced"
		won, _ := drawnWin(t)
		mark := outboxMark(t, db)

		require.NoError(t, repo.RevertWin(ctx, won.ID, entity.LotteryEntryLostReasonCardNotCharged))

		got := drawnOf(t, mark, won.ID)
		require.Len(t, got, 1)
		assert.Equal(t, entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_LOST, got[0].GetState())
		assert.Equal(t, entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_CARD_NOT_CHARGED, got[0].GetLostReason())
		assert.Equal(t, string(won.OrderID), got[0].GetOrderId().GetValue())
	})

	t.Run("already reverted announces nothing", func(t *testing.T) {
		// @spec components/entity/lottery-entry/revert-win "Already reverted announces nothing"
		won, _ := drawnWin(t)
		require.NoError(t, repo.RevertWin(ctx, won.ID, entity.LotteryEntryLostReasonCardNotCharged))
		mark := outboxMark(t, db)

		require.NoError(t, repo.RevertWin(ctx, won.ID, entity.LotteryEntryLostReasonCardNotCharged))

		assert.Empty(t, drawnOf(t, mark, won.ID))
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/lottery-entry/revert-win "Announcement cannot be recorded"
		won, _ := drawnWin(t)

		err := rdb.NewLotteryEntryRepository(db, failingOutbox{}).RevertWin(ctx, won.ID, entity.LotteryEntryLostReasonCardNotCharged)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		got := get(t, won.ID)
		assert.Equal(t, entity.LotteryEntryStateWon, got.State)
		assert.Equal(t, entity.OrderStatusPending, got.Order.Status)
	})
}

func TestLotteryEntryRepository_EndEnteredByEvent(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewLotteryEntryRepository(db, testOutbox)
	ctx := context.Background()

	get := func(t *testing.T, id entity.LotteryEntryID) *entity.LotteryEntry {
		t.Helper()
		e, err := repo.Get(ctx, id)
		require.NoError(t, err)
		return e
	}
	// endedOf returns the LOTTERY_ENTRY.lost announcements since mark of the
	// given entries.
	endedOf := func(t *testing.T, mark int64, entries ...*entity.LotteryEntry) []*entityv1.LotteryEntry {
		t.Helper()
		ids := make(map[string]bool, len(entries))
		for _, e := range entries {
			ids[string(e.ID)] = true
		}
		return announcedSince(t, db, mark, entity.SubjectLotteryEntryLost, func() *entityv1.LotteryEntry { return &entityv1.LotteryEntry{} },
			func(e *entityv1.LotteryEntry) bool { return ids[e.GetId().GetValue()] })
	}
	// assertEnded checks that the entry is Lost with the lost reason concert
	// cancelled, no draw position and a Void Order.
	assertEnded := func(t *testing.T, e *entity.LotteryEntry) {
		t.Helper()
		got := get(t, e.ID)
		assert.Equal(t, entity.LotteryEntryStateLost, got.State)
		assert.Equal(t, entity.LotteryEntryLostReasonConcertCancelled, got.LostReason)
		assert.Nil(t, got.DrawPosition)
		assert.Equal(t, entity.OrderStatusVoid, got.Order.Status)
	}

	t.Run("entries of a cancelled event", func(t *testing.T) {
		// @spec components/entity/lottery-entry/end-entered-by-event "Entries of a cancelled event"
		a := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
		b := seedStockForEvent(t, db, a.EventID, entity.TicketSaleMethodLottery, 100, 4)
		entries := []*entity.LotteryEntry{
			insertLotteryEntry(t, db, a, a.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered),
			insertLotteryEntry(t, db, a, a.SaleID, newUserID(t, db), 1, entity.LotteryEntryStateEntered),
			insertLotteryEntry(t, db, b, b.SaleID, newUserID(t, db), 3, entity.LotteryEntryStateEntered),
		}

		require.NoError(t, repo.EndEnteredByEvent(ctx, a.EventID))

		for _, e := range entries {
			assertEnded(t, e)
		}
	})

	t.Run("won entry", func(t *testing.T) {
		// @spec components/entity/lottery-entry/end-entered-by-event "Won entry"
		f := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
		won := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateWon)

		require.NoError(t, repo.EndEnteredByEvent(ctx, f.EventID))

		assert.Equal(t, won, get(t, won.ID))
	})

	t.Run("another date of the series", func(t *testing.T) {
		// @spec components/entity/lottery-entry/end-entered-by-event "Another date of the series"
		saleID, a, b := seedLotteryOfTwoEvents(t, db)
		cancelled := insertLotteryEntry(t, db, a, saleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)
		other := insertLotteryEntry(t, db, b, saleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)

		require.NoError(t, repo.EndEnteredByEvent(ctx, a.EventID))

		assertEnded(t, cancelled)
		got := get(t, other.ID)
		assert.Equal(t, entity.LotteryEntryStateEntered, got.State)
		assert.Equal(t, entity.OrderStatusPending, got.Order.Status)
	})

	t.Run("called again", func(t *testing.T) {
		// @spec components/entity/lottery-entry/end-entered-by-event "Called again"
		f := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
		e := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)
		require.NoError(t, repo.EndEnteredByEvent(ctx, f.EventID))
		before := get(t, e.ID)

		require.NoError(t, repo.EndEnteredByEvent(ctx, f.EventID))

		assert.Equal(t, before, get(t, e.ID))
	})

	t.Run("no entries", func(t *testing.T) {
		// @spec components/entity/lottery-entry/end-entered-by-event "No entries"
		f := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
		lost := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateLost)
		mark := outboxMark(t, db)

		require.NoError(t, repo.EndEnteredByEvent(ctx, f.EventID), "an event without Entered entries")
		require.NoError(t, repo.EndEnteredByEvent(ctx, entity.NewID()), "no event has the id")

		assert.Equal(t, lost, get(t, lost.ID))
		assert.Empty(t, endedOf(t, mark, lost))
	})

	t.Run("two entries ended", func(t *testing.T) {
		// @spec components/entity/lottery-entry/end-entered-by-event "Two entries ended"
		f := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
		first := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)
		second := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 1, entity.LotteryEntryStateEntered)
		mark := outboxMark(t, db)

		require.NoError(t, repo.EndEnteredByEvent(ctx, f.EventID))

		got := endedOf(t, mark, first, second)
		require.Len(t, got, 2)
		for _, e := range got {
			assert.Equal(t, entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_LOST, e.GetState())
			assert.Equal(t, entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_CONCERT_CANCELLED, e.GetLostReason())
			assert.Equal(t, string(f.SaleID), e.GetTicketSaleId().GetValue())
			assert.Nil(t, e.DrawPosition)
		}
		assert.ElementsMatch(t, []string{string(first.OrderID), string(second.OrderID)},
			[]string{got[0].GetOrderId().GetValue(), got[1].GetOrderId().GetValue()})
	})

	t.Run("called again announces nothing", func(t *testing.T) {
		// @spec components/entity/lottery-entry/end-entered-by-event "Called again announces nothing"
		f := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
		e := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)
		require.NoError(t, repo.EndEnteredByEvent(ctx, f.EventID))
		mark := outboxMark(t, db)

		require.NoError(t, repo.EndEnteredByEvent(ctx, f.EventID))

		assert.Empty(t, endedOf(t, mark, e))
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/lottery-entry/end-entered-by-event "Announcement cannot be recorded"
		f := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
		first := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)
		second := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 1, entity.LotteryEntryStateEntered)

		err := rdb.NewLotteryEntryRepository(db, failingOutbox{}).EndEnteredByEvent(ctx, f.EventID)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Equal(t, first, get(t, first.ID))
		assert.Equal(t, second, get(t, second.ID))
	})
}

// TestLotteryEntriesLostReasonCheck checks that the stored lost reasons are
// exactly those of entity.LotteryEntryLostReason: 3 (concert cancelled) is
// accepted and 4 is refused.
func TestLotteryEntriesLostReasonCheck(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()
	f := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
	e := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)
	const setLost = `UPDATE lottery_entries SET state = 3, lost_reason = $2 WHERE id = $1`

	_, err := db.Pool.Exec(ctx, setLost, string(e.ID), 4)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "chk_lottery_entries_lost_reason", pgErr.ConstraintName)

	_, err = db.Pool.Exec(ctx, setLost, string(e.ID), int16(entity.LotteryEntryLostReasonConcertCancelled))
	require.NoError(t, err)
}

func TestLotteryEntryRepository_MarkLossEmailSent(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewLotteryEntryRepository(db, testOutbox)
	ctx := context.Background()
	f := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)
	lost := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateLost)
	first := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)

	require.NoError(t, repo.MarkLossEmailSent(ctx, lost.ID, first))
	require.NoError(t, repo.MarkLossEmailSent(ctx, lost.ID, first.Add(time.Hour)))

	got, err := repo.Get(ctx, lost.ID)
	require.NoError(t, err)
	require.NotNil(t, got.LossEmailSendTime)
	assert.True(t, first.Equal(*got.LossEmailSendTime), "the first time is kept")
	assert.ErrorIs(t, repo.MarkLossEmailSent(ctx, entity.LotteryEntryID(entity.NewID()), first), apperr.ErrNotFound)
}

// failOrderUpdate makes every update of the Order fail until the test ends,
// through a trigger that raises for its id.
func failOrderUpdate(t *testing.T, db *rdb.Database, id entity.OrderID) {
	t.Helper()
	ctx := context.Background()
	_, err := db.Pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION test_fail_order_update() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'test: update of order % refused', OLD.id;
		END $$`)
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, `
		CREATE TRIGGER test_fail_order_update BEFORE UPDATE ON orders
		FOR EACH ROW WHEN (OLD.id = '`+string(id)+`'::uuid) EXECUTE FUNCTION test_fail_order_update()`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := db.Pool.Exec(ctx, `DROP TRIGGER IF EXISTS test_fail_order_update ON orders`)
		require.NoError(t, err)
		_, err = db.Pool.Exec(ctx, `DROP FUNCTION IF EXISTS test_fail_order_update()`)
		require.NoError(t, err)
	})
}

// TestLotteryEntryRepository_StatsParity checks that the stats query counts
// exactly what entity.NewLotteryEntryStats counts for the sale's entries of
// the stock, in every state, and leaves out the entries of another stock.
func TestLotteryEntryRepository_StatsParity(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewLotteryEntryRepository(db, testOutbox)
	saleID, a, b := seedLotteryOfTwoStocks(t, db, 100, 100)

	var ofA []*entity.LotteryEntry
	for i, state := range []entity.LotteryEntryState{
		entity.LotteryEntryStateEntered, entity.LotteryEntryStateEntered, entity.LotteryEntryStateWon,
		entity.LotteryEntryStateWon, entity.LotteryEntryStateLost, entity.LotteryEntryStateLost,
	} {
		ofA = append(ofA, insertLotteryEntry(t, db, a, saleID, newUserID(t, db), i%3+1, state))
	}
	insertLotteryEntry(t, db, b, saleID, newUserID(t, db), 4, entity.LotteryEntryStateWon)

	got, err := repo.GetStatsByTicketSaleAndTicketStock(context.Background(), saleID, a.ID)

	require.NoError(t, err)
	assert.Equal(t, entity.NewLotteryEntryStats(ofA), got)
	assert.Equal(t, 6, got.EntryCount, "the entries of another stock are not counted")
}
