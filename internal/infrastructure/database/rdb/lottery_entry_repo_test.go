package rdb_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

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
// authorization "pi_<id>".
func newEntry(f *stockFixture, saleID entity.TicketSaleID, userID entity.UserID, count int, code *entity.ReferralCode) *entity.LotteryEntry {
	order := entity.NewPendingOrder(userID, stockOf(f), count, code, entity.OrderPaymentProvider)
	order.CardAuthorizationID = "pi_" + entity.NewID()
	return entity.NewLotteryEntry(order, saleID)
}

// insertLotteryEntry stores, without the repository's checks, an entry in the
// sale in the given state with its Order of the user for count tickets of the
// fixture's stock: Void for a Lost entry, Pending otherwise. It returns the
// entry as a repository read gives it.
func insertLotteryEntry(t *testing.T, f *stockFixture, saleID entity.TicketSaleID, userID entity.UserID, count int, state entity.LotteryEntryState) *entity.LotteryEntry {
	t.Helper()
	e := newEntry(f, saleID, userID, count, nil)
	e.State = state
	if state == entity.LotteryEntryStateLost {
		e.Order.Status = entity.OrderStatusVoid
	}
	o := e.Order
	ctx := context.Background()
	_, err := testDB.Pool.Exec(ctx, `
		INSERT INTO orders (id, user_id, ticket_stock_id, ticket_count, card_authorization_id, provider, status, amount, currency)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, string(o.ID), string(o.UserID), string(o.TicketStockID), o.TicketCount, o.CardAuthorizationID,
		int16(o.Payment.Provider), int16(o.Status), o.Amount, o.Currency)
	require.NoError(t, err)
	_, err = testDB.Pool.Exec(ctx, `
		INSERT INTO lottery_entries (id, order_id, ticket_sale_id, state) VALUES ($1, $2, $3, $4)
	`, string(e.ID), string(o.ID), string(saleID), int16(state))
	require.NoError(t, err)
	return e
}

// newUserID seeds a fan user and returns its id: LotteryEntry.Create stores
// nothing for a user that does not exist.
func newUserID(t *testing.T) entity.UserID {
	t.Helper()
	return entity.UserID(seedFanUser(t))
}

// setSaleEndByDatabaseClock moves the sale's window so that it opened an hour
// before the database's current time and ends after the given interval from
// it (negative: already ended). LotteryEntry.Create reads the database clock,
// so the tests that store an entry place the window around it.
func setSaleEndByDatabaseClock(t *testing.T, saleID entity.TicketSaleID, end time.Duration) {
	t.Helper()
	_, err := testDB.Pool.Exec(context.Background(), `
		UPDATE ticket_sales
		SET sale_start_at = now() - interval '1 hour' + least($2::interval, interval '0'),
		    sale_end_at = now() + $2::interval
		WHERE id = $1`, string(saleID), end)
	require.NoError(t, err)
}

// openLotteryStock seeds a stock of 100 tickets of an event with a
// per-account limit of 4, and a Lottery sale naming the event that is open
// by the database clock for another day.
func openLotteryStock(t *testing.T) *stockFixture {
	t.Helper()
	f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
	setSaleEndByDatabaseClock(t, f.SaleID, 24*time.Hour)
	return f
}

// seedSiblingEvent inserts another event of the Series of eventID, at the
// same venue a day later per call, and returns its id.
func seedSiblingEvent(t *testing.T, eventID string) string {
	t.Helper()
	id := uuid.NewV7().String()
	_, err := testDB.Pool.Exec(context.Background(), `
		INSERT INTO events (id, series_id, venue_id, local_event_date, publish_state, per_account_limit)
		SELECT $1, series_id, venue_id,
			local_event_date + 1 + (SELECT count(*)::int FROM events e2 WHERE e2.series_id = events.series_id),
			publish_state, per_account_limit
		FROM events WHERE id = $2
	`, id, eventID)
	require.NoError(t, err)
	return id
}

// seedLotteryOfTwoEvents seeds two events of one Series, a stock of each, and
// one Lottery sale naming both. It returns the sale and the two stocks; each
// stock's own SaleID is another Lottery sale that names only its event.
func seedLotteryOfTwoEvents(t *testing.T) (entity.TicketSaleID, *stockFixture, *stockFixture) {
	t.Helper()
	a := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
	b := seedStockForEvent(t, seedSiblingEvent(t, a.EventID), entity.TicketSaleMethodLottery, 100, 4)
	_, err := testDB.Pool.Exec(context.Background(),
		`INSERT INTO ticket_sale_events (ticket_sale_id, event_id) VALUES ($1, $2)`, string(a.SaleID), b.EventID)
	require.NoError(t, err)
	return a.SaleID, a, b
}

// orderStatus reads the status of the Order.
func orderStatus(t *testing.T, id entity.OrderID) entity.OrderStatus {
	t.Helper()
	var status int16
	require.NoError(t, testDB.Pool.QueryRow(context.Background(), `SELECT status FROM orders WHERE id = $1`, string(id)).Scan(&status))
	return entity.OrderStatus(status)
}

// countOrdersOfUser counts every Order of the user, in any status.
func countOrdersOfUser(t *testing.T, userID entity.UserID) int {
	t.Helper()
	var n int
	require.NoError(t, testDB.Pool.QueryRow(context.Background(), `SELECT count(*) FROM orders WHERE user_id = $1`, string(userID)).Scan(&n))
	return n
}

func TestLotteryEntryRepository_Create(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewLotteryEntryRepository(testDB)
	ctx := context.Background()

	t.Run("entry stored", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Entry stored"
		f := openLotteryStock(t)
		code := entity.ReferralCode("member-a")
		entry := newEntry(f, f.SaleID, newUserID(t), 2, &code)

		got, err := repo.Create(ctx, entry)

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
			f := openLotteryStock(t)
			// Another stock of the same event, which the sale also sells.
			other := seedStockForEvent(t, f.EventID, entity.TicketSaleMethodLottery, 100, 4)
			userID := newUserID(t)
			first := insertLotteryEntry(t, f, f.SaleID, userID, 2, state)

			for _, stock := range []*stockFixture{f, other} {
				_, err := repo.Create(ctx, newEntry(stock, f.SaleID, userID, 1, nil))

				assert.ErrorIs(t, err, apperr.ErrAlreadyExists, state.String())
			}
			all, err := repo.ListByTicketSaleAndUser(ctx, f.SaleID, userID)
			require.NoError(t, err)
			assert.Equal(t, []*entity.LotteryEntry{first}, all, "nothing is stored")
			assert.Equal(t, 1, countOrdersOfUser(t, userID), "no Order is stored")
		}
	})

	t.Run("double tap", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Double tap"
		f := openLotteryStock(t)
		userID := newUserID(t)

		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := range errs {
			wg.Go(func() {
				_, errs[i] = repo.Create(ctx, newEntry(f, f.SaleID, userID, 2, nil))
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
		assert.Equal(t, 1, countOrdersOfUser(t, userID))
	})

	t.Run("another date of a tour presale", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Another date of a tour presale"
		saleID, a, b := seedLotteryOfTwoEvents(t)
		setSaleEndByDatabaseClock(t, saleID, 24*time.Hour)
		userID := newUserID(t)
		insertLotteryEntry(t, a, saleID, userID, 2, entity.LotteryEntryStateEntered)

		got, err := repo.Create(ctx, newEntry(b, saleID, userID, 2, nil))

		require.NoError(t, err)
		assert.Equal(t, b.ID, got.Order.TicketStockID)
		assert.Equal(t, entity.LotteryEntryStateEntered, got.State)
		assert.Equal(t, entity.OrderStatusPending, orderStatus(t, got.OrderID))
		all, err := repo.ListByTicketSaleAndUser(ctx, saleID, userID)
		require.NoError(t, err)
		assert.Len(t, all, 2)
	})

	t.Run("same card authorization again", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Same card authorization again"
		f := openLotteryStock(t)
		userID := newUserID(t)
		entry := newEntry(f, f.SaleID, userID, 2, nil)
		first, err := repo.Create(ctx, entry)
		require.NoError(t, err)
		stored, err := repo.Get(ctx, first.ID)
		require.NoError(t, err)

		// again returns a new entry for the same card authorization.
		again := func(f *stockFixture, cardAuthorizationID string) *entity.LotteryEntry {
			e := newEntry(f, f.SaleID, userID, 2, nil)
			e.Order.CardAuthorizationID = cardAuthorizationID
			return e
		}

		// Before the end time: the same entry, nothing else stored.
		got, err := repo.Create(ctx, again(f, entry.Order.CardAuthorizationID))
		require.NoError(t, err)
		assert.Equal(t, stored, got)

		// After the end time: still the same entry.
		setSaleEndByDatabaseClock(t, f.SaleID, -time.Minute)
		late, err := repo.Create(ctx, again(f, entry.Order.CardAuthorizationID))
		require.NoError(t, err)
		assert.Equal(t, stored, late)
		assert.Equal(t, 1, countOrdersOfUser(t, userID))

		// Twice at the same time: one entry, returned to both.
		other := openLotteryStock(t)
		cardAuthorizationID := "pi_" + entity.NewID()
		var wg sync.WaitGroup
		results := make([]*entity.LotteryEntry, 2)
		errs := make([]error, 2)
		for i := range results {
			wg.Go(func() {
				results[i], errs[i] = repo.Create(ctx, again(other, cardAuthorizationID))
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
		assert.Equal(t, 2, countOrdersOfUser(t, userID))
	})

	t.Run("presale winner in a general lottery", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Presale winner in a general lottery"
		presale := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
		general := seedSale(t, presale.EventID, entity.TicketSaleMethodLottery, saleWindowStart, saleWindowStart.Add(time.Hour))
		setSaleEndByDatabaseClock(t, general, 24*time.Hour)
		winner := newUserID(t)
		seedIssuedTicketsOf(t, presale, string(winner), 4)

		_, err := repo.Create(ctx, newEntry(presale, general, winner, 1, nil))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		all, err := repo.ListByTicketSaleAndUser(ctx, general, winner)
		require.NoError(t, err)
		assert.Empty(t, all, "nothing is stored")
		assert.Equal(t, 1, countOrdersOfUser(t, winner), "only the presale Order exists")

		// A presale winner of 2 tickets may still enter for 2.
		below := newUserID(t)
		seedIssuedTicketsOf(t, presale, string(below), 2)
		_, err = repo.Create(ctx, newEntry(presale, general, below, 2, nil))
		assert.NoError(t, err)
	})

	t.Run("pending entry counts toward the limit", func(t *testing.T) {
		// An Entered entry's Pending Order in a presale counts as held.
		presale := openLotteryStock(t)
		general := seedSale(t, presale.EventID, entity.TicketSaleMethodLottery, saleWindowStart, saleWindowStart.Add(time.Hour))
		setSaleEndByDatabaseClock(t, general, 24*time.Hour)
		userID := newUserID(t)
		insertLotteryEntry(t, presale, presale.SaleID, userID, 3, entity.LotteryEntryStateEntered)

		_, err := repo.Create(ctx, newEntry(presale, general, userID, 2, nil))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		_, err = repo.Create(ctx, newEntry(presale, general, userID, 1, nil))
		assert.NoError(t, err)
	})

	t.Run("count above the per-account limit", func(t *testing.T) {
		f := openLotteryStock(t)
		userID := newUserID(t)

		_, err := repo.Create(ctx, newEntry(f, f.SaleID, userID, f.Limit+1, nil))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Zero(t, countOrdersOfUser(t, userID))
	})

	t.Run("another seat type", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Another seat type"
		sSeat := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
		aSeat := seedStockForEvent(t, sSeat.EventID, entity.TicketSaleMethodLottery, 100, 4)
		setSaleEndByDatabaseClock(t, aSeat.SaleID, 24*time.Hour)
		userID := newUserID(t)
		seedIssuedTicketsOf(t, sSeat, string(userID), 2)

		_, err := repo.Create(ctx, newEntry(aSeat, aSeat.SaleID, userID, 1, nil))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		all, err := repo.ListByTicketSaleAndUser(ctx, aSeat.SaleID, userID)
		require.NoError(t, err)
		assert.Empty(t, all, "nothing is stored")

		// The same seat type in the same sale is accepted.
		_, err = repo.Create(ctx, newEntry(sSeat, aSeat.SaleID, userID, 2, nil))
		assert.NoError(t, err)
	})

	t.Run("after the end time", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "After the end time"
		f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
		setSaleEndByDatabaseClock(t, f.SaleID, -time.Second)
		userID := newUserID(t)

		_, err := repo.Create(ctx, newEntry(f, f.SaleID, userID, 2, nil))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		all, err := repo.ListByTicketSaleAndUser(ctx, f.SaleID, userID)
		require.NoError(t, err)
		assert.Empty(t, all)
		assert.Zero(t, countOrdersOfUser(t, userID), "no Order is stored")
	})

	t.Run("unknown ticket stock", func(t *testing.T) {
		// @spec components/entity/lottery-entry/create "Unknown ticket stock"
		f := openLotteryStock(t)
		unknown := *f
		unknown.ID = entity.TicketStockID(entity.NewID())
		userID := newUserID(t)
		entry := newEntry(&unknown, f.SaleID, userID, 2, nil)

		_, err := repo.Create(ctx, entry)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		_, err = repo.GetByCardAuthorizationID(ctx, entry.Order.CardAuthorizationID)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.Zero(t, countOrdersOfUser(t, userID))
	})
}

func TestLotteryEntryRepository_Get(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewLotteryEntryRepository(testDB)
	ctx := context.Background()
	f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)

	t.Run("existing entry", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get "Existing entry"
		for _, state := range []entity.LotteryEntryState{entity.LotteryEntryStateEntered, entity.LotteryEntryStateWon, entity.LotteryEntryStateLost} {
			e := insertLotteryEntry(t, f, f.SaleID, newUserID(t), 3, state)

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
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewLotteryEntryRepository(testDB)
	ctx := context.Background()
	f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)

	t.Run("charged win", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-by-card-authorization-id "Charged win"
		won := insertLotteryEntry(t, f, f.SaleID, newUserID(t), 2, entity.LotteryEntryStateWon)
		insertLotteryEntry(t, f, f.SaleID, newUserID(t), 2, entity.LotteryEntryStateWon) // another entry

		got, err := repo.GetByCardAuthorizationID(ctx, won.Order.CardAuthorizationID)

		require.NoError(t, err)
		assert.Equal(t, won, got)
	})

	t.Run("unknown card authorization", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-by-card-authorization-id "Unknown card authorization"
		_, err := repo.GetByCardAuthorizationID(ctx, "pi_unknown_"+entity.NewID())

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestLotteryEntryRepository_ListByTicketSaleAndUser(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewLotteryEntryRepository(testDB)
	ctx := context.Background()

	t.Run("entry of the user", func(t *testing.T) {
		// @spec components/entity/lottery-entry/list-by-ticket-sale-and-user "Entry of the user"
		saleID, a, b := seedLotteryOfTwoEvents(t)
		userID := newUserID(t)
		entered := insertLotteryEntry(t, a, saleID, userID, 2, entity.LotteryEntryStateEntered)
		insertLotteryEntry(t, a, saleID, newUserID(t), 2, entity.LotteryEntryStateEntered) // another user
		insertLotteryEntry(t, b, b.SaleID, userID, 2, entity.LotteryEntryStateEntered)     // another sale

		got, err := repo.ListByTicketSaleAndUser(ctx, saleID, userID)

		require.NoError(t, err)
		assert.Equal(t, []*entity.LotteryEntry{entered}, got)
	})

	t.Run("one entry for each event", func(t *testing.T) {
		saleID, a, b := seedLotteryOfTwoEvents(t)
		userID := newUserID(t)
		first := insertLotteryEntry(t, a, saleID, userID, 2, entity.LotteryEntryStateWon)
		second := insertLotteryEntry(t, b, saleID, userID, 1, entity.LotteryEntryStateLost)

		got, err := repo.ListByTicketSaleAndUser(ctx, saleID, userID)

		require.NoError(t, err)
		assert.Equal(t, []*entity.LotteryEntry{second, first}, got, "newest first")
	})

	t.Run("never entered", func(t *testing.T) {
		// @spec components/entity/lottery-entry/list-by-ticket-sale-and-user "Never entered"
		f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
		insertLotteryEntry(t, f, f.SaleID, newUserID(t), 2, entity.LotteryEntryStateEntered) // another user

		got, err := repo.ListByTicketSaleAndUser(ctx, f.SaleID, newUserID(t))

		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})
}

func TestLotteryEntryRepository_ListByUser(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewLotteryEntryRepository(testDB)
	ctx := context.Background()

	t.Run("entries for two ticket types", func(t *testing.T) {
		// @spec components/entity/lottery-entry/list-by-user "Entries for two ticket types"
		f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
		other := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
		userID := newUserID(t)
		entered := insertLotteryEntry(t, f, f.SaleID, userID, 2, entity.LotteryEntryStateEntered)
		lost := insertLotteryEntry(t, other, other.SaleID, userID, 1, entity.LotteryEntryStateLost)
		insertLotteryEntry(t, f, f.SaleID, newUserID(t), 2, entity.LotteryEntryStateEntered) // another user

		got, err := repo.ListByUser(ctx, userID)

		require.NoError(t, err)
		assert.Equal(t, []*entity.LotteryEntry{lost, entered}, got)
	})

	t.Run("never entered", func(t *testing.T) {
		// @spec components/entity/lottery-entry/list-by-user "Never entered"
		got, err := repo.ListByUser(ctx, newUserID(t))

		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})
}

func TestLotteryEntryRepository_ListEnteredByTicketSaleAndTicketStock(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewLotteryEntryRepository(testDB)
	ctx := context.Background()

	t.Run("mixed states", func(t *testing.T) {
		// @spec components/entity/lottery-entry/list-entered-by-ticket-sale-and-ticket-stock "Mixed states"
		saleID, a, b := seedLotteryOfTwoEvents(t)
		x := insertLotteryEntry(t, a, saleID, newUserID(t), 2, entity.LotteryEntryStateEntered)
		y := insertLotteryEntry(t, a, saleID, newUserID(t), 1, entity.LotteryEntryStateEntered)
		insertLotteryEntry(t, a, saleID, newUserID(t), 3, entity.LotteryEntryStateWon)
		insertLotteryEntry(t, a, saleID, newUserID(t), 2, entity.LotteryEntryStateLost)
		insertLotteryEntry(t, b, saleID, newUserID(t), 2, entity.LotteryEntryStateEntered) // another stock
		otherSale := seedSale(t, a.EventID, entity.TicketSaleMethodLottery, saleWindowStart, saleWindowStart.Add(time.Hour))
		insertLotteryEntry(t, a, otherSale, newUserID(t), 2, entity.LotteryEntryStateEntered) // another sale

		got, err := repo.ListEnteredByTicketSaleAndTicketStock(ctx, saleID, a.ID)

		require.NoError(t, err)
		assert.ElementsMatch(t, []*entity.LotteryEntry{x, y}, got)
	})

	t.Run("no candidates", func(t *testing.T) {
		// @spec components/entity/lottery-entry/list-entered-by-ticket-sale-and-ticket-stock "No candidates"
		f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
		insertLotteryEntry(t, f, f.SaleID, newUserID(t), 2, entity.LotteryEntryStateWon)
		insertLotteryEntry(t, f, f.SaleID, newUserID(t), 2, entity.LotteryEntryStateLost)

		got, err := repo.ListEnteredByTicketSaleAndTicketStock(ctx, f.SaleID, f.ID)

		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})
}

func TestLotteryEntryRepository_GetStatsByTicketSaleAndTicketStock(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewLotteryEntryRepository(testDB)
	ctx := context.Background()

	t.Run("after the draw", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-stats-by-ticket-sale-and-ticket-stock "After the draw"
		f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
		insertLotteryEntry(t, f, f.SaleID, newUserID(t), 2, entity.LotteryEntryStateWon)
		insertLotteryEntry(t, f, f.SaleID, newUserID(t), 2, entity.LotteryEntryStateWon)
		insertLotteryEntry(t, f, f.SaleID, newUserID(t), 4, entity.LotteryEntryStateLost)

		got, err := repo.GetStatsByTicketSaleAndTicketStock(ctx, f.SaleID, f.ID)

		require.NoError(t, err)
		assert.Equal(t, entity.LotteryEntryStats{
			EntryCount: 3, TicketCount: 8, WonEntryCount: 2, WonTicketCount: 4, LostEntryCount: 1,
		}, got)
	})

	t.Run("before the draw", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-stats-by-ticket-sale-and-ticket-stock "Before the draw"
		f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
		insertLotteryEntry(t, f, f.SaleID, newUserID(t), 2, entity.LotteryEntryStateEntered)
		insertLotteryEntry(t, f, f.SaleID, newUserID(t), 3, entity.LotteryEntryStateEntered)
		// An entry of another sale of the stock's event is not counted.
		otherSale := seedSale(t, f.EventID, entity.TicketSaleMethodLottery, saleWindowStart, saleWindowStart.Add(time.Hour))
		insertLotteryEntry(t, f, otherSale, newUserID(t), 4, entity.LotteryEntryStateEntered)

		got, err := repo.GetStatsByTicketSaleAndTicketStock(ctx, f.SaleID, f.ID)

		require.NoError(t, err)
		assert.Equal(t, entity.LotteryEntryStats{EntryCount: 2, TicketCount: 5}, got)
	})

	t.Run("no entries", func(t *testing.T) {
		// @spec components/entity/lottery-entry/get-stats-by-ticket-sale-and-ticket-stock "No entries"
		f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
		other := seedStockForEvent(t, f.EventID, entity.TicketSaleMethodLottery, 100, 4)
		insertLotteryEntry(t, other, f.SaleID, newUserID(t), 2, entity.LotteryEntryStateEntered)

		got, err := repo.GetStatsByTicketSaleAndTicketStock(ctx, f.SaleID, f.ID)

		require.NoError(t, err)
		assert.Equal(t, entity.LotteryEntryStats{}, got)
	})
}

func TestLotteryEntryRepository_PersistDrawOutcome(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewLotteryEntryRepository(testDB)
	sales := rdb.NewTicketSaleRepository(testDB, testOutbox)
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
	winner := func(e *entity.LotteryEntry, position int) entity.DrawWinnerRow {
		return entity.DrawWinnerRow{LotteryEntryID: e.ID, CardAuthorizationID: e.Order.CardAuthorizationID, DrawPosition: position}
	}
	loser := func(e *entity.LotteryEntry, position int) entity.DrawLoserRow {
		return entity.DrawLoserRow{LotteryEntryID: e.ID, CardAuthorizationID: e.Order.CardAuthorizationID, DrawPosition: position, LostReason: entity.LotteryEntryLostReasonNotDrawn}
	}
	position := func(p int) *int { return &p }

	t.Run("outcome recorded", func(t *testing.T) {
		// @spec components/entity/lottery-entry/persist-draw-outcome "Outcome recorded"
		saleID, a, b := seedLotteryOfTwoEvents(t)
		winA := insertLotteryEntry(t, a, saleID, newUserID(t), 2, entity.LotteryEntryStateEntered)
		loseA := insertLotteryEntry(t, a, saleID, newUserID(t), 4, entity.LotteryEntryStateEntered)
		winB := insertLotteryEntry(t, b, saleID, newUserID(t), 1, entity.LotteryEntryStateEntered)
		loseB := insertLotteryEntry(t, b, saleID, newUserID(t), 3, entity.LotteryEntryStateEntered)
		require.False(t, drawn(t, saleID))

		err := repo.PersistDrawOutcome(ctx, saleID,
			[]entity.DrawWinnerRow{winner(winA, 1), winner(winB, 0)},
			[]entity.DrawLoserRow{loser(loseA, 0), loser(loseB, 1)},
		)

		require.NoError(t, err)
		for _, tc := range []struct {
			entry    *entity.LotteryEntry
			state    entity.LotteryEntryState
			position int
			status   entity.OrderStatus
		}{
			{winA, entity.LotteryEntryStateWon, 1, entity.OrderStatusPending},
			{winB, entity.LotteryEntryStateWon, 0, entity.OrderStatusPending},
			{loseA, entity.LotteryEntryStateLost, 0, entity.OrderStatusVoid},
			{loseB, entity.LotteryEntryStateLost, 1, entity.OrderStatusVoid},
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
		saleID, a, b := seedLotteryOfTwoEvents(t)
		won := insertLotteryEntry(t, a, saleID, newUserID(t), 2, entity.LotteryEntryStateEntered)
		lost := insertLotteryEntry(t, b, saleID, newUserID(t), 2, entity.LotteryEntryStateEntered)
		// The last write fails: voiding the loser's Order is refused.
		failOrderUpdate(t, lost.OrderID)

		err := repo.PersistDrawOutcome(ctx, saleID, []entity.DrawWinnerRow{winner(won, 0)}, []entity.DrawLoserRow{loser(lost, 1)})

		require.Error(t, err)
		assert.Equal(t, won, get(t, won.ID))
		assert.Equal(t, lost, get(t, lost.ID))
		assert.False(t, drawn(t, saleID))
	})

	t.Run("unknown sale", func(t *testing.T) {
		f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
		e := insertLotteryEntry(t, f, f.SaleID, newUserID(t), 2, entity.LotteryEntryStateEntered)

		err := repo.PersistDrawOutcome(ctx, entity.TicketSaleID(entity.NewID()), []entity.DrawWinnerRow{winner(e, 0)}, nil)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.Equal(t, e, get(t, e.ID))
	})

	t.Run("empty draw", func(t *testing.T) {
		// @spec components/entity/lottery-entry/persist-draw-outcome "Empty draw"
		f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
		bystander := insertLotteryEntry(t, f, f.SaleID, newUserID(t), 2, entity.LotteryEntryStateEntered)

		require.NoError(t, repo.PersistDrawOutcome(ctx, f.SaleID, nil, nil))

		assert.Equal(t, bystander, get(t, bystander.ID))
	})

	t.Run("recorded again", func(t *testing.T) {
		// @spec components/entity/lottery-entry/persist-draw-outcome "Recorded again"
		f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
		won := insertLotteryEntry(t, f, f.SaleID, newUserID(t), 2, entity.LotteryEntryStateEntered)
		lost := insertLotteryEntry(t, f, f.SaleID, newUserID(t), 2, entity.LotteryEntryStateEntered)
		require.NoError(t, repo.PersistDrawOutcome(ctx, f.SaleID, []entity.DrawWinnerRow{winner(won, 0)}, []entity.DrawLoserRow{loser(lost, 1)}))
		wonBefore, lostBefore := get(t, won.ID), get(t, lost.ID)

		// The same outcome again, and one that would swap the two.
		require.NoError(t, repo.PersistDrawOutcome(ctx, f.SaleID, []entity.DrawWinnerRow{winner(won, 0)}, []entity.DrawLoserRow{loser(lost, 1)}))
		require.NoError(t, repo.PersistDrawOutcome(ctx, f.SaleID, []entity.DrawWinnerRow{winner(lost, 5)}, []entity.DrawLoserRow{loser(won, 6)}))

		assert.Equal(t, wonBefore, get(t, won.ID))
		assert.Equal(t, lostBefore, get(t, lost.ID))
		assert.Equal(t, entity.OrderStatusPending, wonBefore.Order.Status, "the winner's Order is never voided")
	})

	t.Run("empty sale id", func(t *testing.T) {
		err := repo.PersistDrawOutcome(ctx, "", nil, nil)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

// failOrderUpdate makes every update of the Order fail until the test ends,
// through a trigger that raises for its id.
func failOrderUpdate(t *testing.T, id entity.OrderID) {
	t.Helper()
	ctx := context.Background()
	_, err := testDB.Pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION test_fail_order_update() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'test: update of order % refused', OLD.id;
		END $$`)
	require.NoError(t, err)
	_, err = testDB.Pool.Exec(ctx, `
		CREATE TRIGGER test_fail_order_update BEFORE UPDATE ON orders
		FOR EACH ROW WHEN (OLD.id = '`+string(id)+`'::uuid) EXECUTE FUNCTION test_fail_order_update()`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := testDB.Pool.Exec(ctx, `DROP TRIGGER IF EXISTS test_fail_order_update ON orders`)
		require.NoError(t, err)
		_, err = testDB.Pool.Exec(ctx, `DROP FUNCTION IF EXISTS test_fail_order_update()`)
		require.NoError(t, err)
	})
}
