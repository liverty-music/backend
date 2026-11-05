package rdb_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

var (
	resaleJST = time.FixedZone("JST", 9*60*60)
	// resaleShowStart is when the resale fixtures' event starts; its resale
	// deadline is 12:00 that day.
	resaleShowStart = time.Date(2026, 12, 20, 19, 0, 0, 0, resaleJST)
	// resaleApplyTime is when the fixtures' fans apply, while resale is open.
	resaleApplyTime = time.Date(2026, 12, 10, 10, 0, 0, 0, resaleJST)
	// resaleCardExpiry is when the fixtures' card authorizations expire.
	resaleCardExpiry = time.Date(2027, 1, 9, 10, 0, 0, 0, resaleJST)
)

// seedResaleStock creates a stock of quantity tickets, all sold to another
// fan, for an event starting at resaleShowStart with the per-account limit.
func seedResaleStock(t *testing.T, quantity, limit int) *stockFixture {
	t.Helper()
	f := seedStock(t, entity.TicketSaleMethodLottery, quantity, limit)
	_, err := testDB.Pool.Exec(context.Background(), `UPDATE events SET start_at = $2 WHERE id = $1`, f.EventID, resaleShowStart)
	require.NoError(t, err)
	seedPaidOrder(t, f, string(newUserID(t)), quantity, issuedTicketsTime)
	return f
}

// newResaleEntry returns a new Entered entry, applied for at resaleApplyTime,
// with its Pending Order of the user for count tickets of the fixture's stock
// and a fresh card authorization expiring at resaleCardExpiry.
func newResaleEntry(f *stockFixture, userID entity.UserID, count int) *entity.ResaleEntry {
	order := entity.NewPendingOrder(userID, stockOf(f), count, nil, entity.OrderPaymentProvider)
	order.CardAuthorizationID = newCardAuthorizationID()
	e := entity.NewResaleEntry(order, resaleApplyTime)
	expire := resaleCardExpiry
	e.CardAuthorizationExpireTime = &expire
	return e
}

// setResaleEntryState stores the entry's state, with a draw time for a drawn
// state, and voids its Order for an ended state.
func setResaleEntryState(t *testing.T, id entity.ResaleEntryID, state entity.ResaleEntryState) {
	t.Helper()
	ctx := context.Background()
	var drawAt any
	if state == entity.ResaleEntryStateWon || state == entity.ResaleEntryStateLost || state == entity.ResaleEntryStateLapsed {
		drawAt = resaleApplyTime.Add(2 * time.Hour)
	}
	var lostReason any
	if state == entity.ResaleEntryStateLost {
		lostReason = int16(entity.ResaleEntryLostReasonNoTicketLeft)
	}
	_, err := testDB.Pool.Exec(ctx, `UPDATE resale_entries SET state = $2, draw_at = $3, lost_reason = $4 WHERE id = $1`,
		string(id), int16(state), drawAt, lostReason)
	require.NoError(t, err)
	if state == entity.ResaleEntryStateLost || state == entity.ResaleEntryStateLapsed {
		_, err = testDB.Pool.Exec(ctx, `UPDATE orders SET status = 5 WHERE id = (SELECT order_id FROM resale_entries WHERE id = $1)`, string(id))
		require.NoError(t, err)
	}
}

func TestResaleEntryRepository_Create(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewResaleEntryRepository(testDB, testOutbox)

	t.Run("entry stored", func(t *testing.T) {
		// @spec components/entity/resale-entry/create "Entry stored"
		f := seedResaleStock(t, 4, 4)
		fan := newUserID(t)

		got, err := repo.Create(ctx, newResaleEntry(f, fan, 2))

		require.NoError(t, err)
		stored, err := repo.Get(ctx, got.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ResaleEntryStateEntered, stored.State)
		assert.True(t, stored.CreateTime.Equal(resaleApplyTime))
		assert.Equal(t, entity.OrderStatusPending, stored.Order.Status)
		assert.Equal(t, 2, stored.Order.TicketCount)
		assert.Equal(t, got.ID, stored.Order.ResaleEntryID, "the Order's source is the entry")
		require.NotNil(t, stored.CardAuthorizationExpireTime, "the card authorization's expiry is recorded")
		assert.True(t, stored.CardAuthorizationExpireTime.Equal(resaleCardExpiry))
	})

	t.Run("resale not open", func(t *testing.T) {
		// @spec components/entity/resale-entry/create "Resale not open"
		f := seedStock(t, entity.TicketSaleMethodLottery, 4, 4)
		_, err := testDB.Pool.Exec(ctx, `UPDATE events SET start_at = $2 WHERE id = $1`, f.EventID, resaleShowStart)
		require.NoError(t, err)
		fan := newUserID(t)

		_, err = repo.Create(ctx, newResaleEntry(f, fan, 1))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		entries, err := repo.ListByUser(ctx, fan)
		require.NoError(t, err)
		assert.Empty(t, entries, "nothing is stored")
	})

	t.Run("after the resale deadline", func(t *testing.T) {
		f := seedResaleStock(t, 2, 4)
		e := newResaleEntry(f, newUserID(t), 1)
		e.CreateTime = time.Date(2026, 12, 20, 12, 0, 0, 0, resaleJST)

		_, err := repo.Create(ctx, e)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("over the limit", func(t *testing.T) {
		// @spec components/entity/resale-entry/create "Over the limit"
		f := seedResaleStock(t, 4, 4)
		fan := newUserID(t)
		held := seedPaidOrder(t, f, string(fan), 3, issuedTicketsTime)
		for range 3 {
			_, err := testDB.Pool.Exec(ctx, `INSERT INTO tickets (id, order_id, user_id, event_id, status, issue_at) VALUES ($1, $2, $3, $4, 1, $5)`,
				entity.NewID(), string(held), string(fan), f.EventID, issuedTicketsTime)
			require.NoError(t, err)
		}

		_, err := repo.Create(ctx, newResaleEntry(f, fan, 2))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		entries, err := repo.ListByUser(ctx, fan)
		require.NoError(t, err)
		assert.Empty(t, entries, "nothing is stored")
	})

	t.Run("second entry for the event", func(t *testing.T) {
		// @spec components/entity/resale-entry/create "Second entry for the event"
		f := seedResaleStock(t, 4, 4)
		fan := newUserID(t)
		_, err := repo.Create(ctx, newResaleEntry(f, fan, 1))
		require.NoError(t, err)

		_, err = repo.Create(ctx, newResaleEntry(f, fan, 1))

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
		entries, err := repo.ListByUser(ctx, fan)
		require.NoError(t, err)
		assert.Len(t, entries, 1, "nothing more is stored")
	})

	t.Run("same card authorization again", func(t *testing.T) {
		// @spec components/entity/resale-entry/create "Same card authorization again"
		f := seedResaleStock(t, 4, 4)
		fan := newUserID(t)
		first := newResaleEntry(f, fan, 2)
		created, err := repo.Create(ctx, first)
		require.NoError(t, err)

		again := newResaleEntry(f, fan, 2)
		again.Order.CardAuthorizationID = first.Order.CardAuthorizationID
		got, err := repo.Create(ctx, again)

		require.NoError(t, err)
		assert.Equal(t, created.ID, got.ID)
		entries, err := repo.ListByUser(ctx, fan)
		require.NoError(t, err)
		assert.Len(t, entries, 1)
	})

	t.Run("two applications at once", func(t *testing.T) {
		// @spec components/entity/resale-entry/create "Two applications at once"
		f := seedResaleStock(t, 4, 4)
		fan := newUserID(t)
		entries := []*entity.ResaleEntry{newResaleEntry(f, fan, 2), newResaleEntry(f, fan, 2)}

		errs := make([]error, len(entries))
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i, e := range entries {
			wg.Go(func() {
				<-start
				_, errs[i] = repo.Create(ctx, e)
			})
		}
		close(start)
		wg.Wait()

		stored, failed := 0, 0
		for _, err := range errs {
			switch {
			case err == nil:
				stored++
			case errors.Is(err, apperr.ErrAlreadyExists):
				failed++
			default:
				t.Fatalf("unexpected error: %v", err)
			}
		}
		assert.Equal(t, 1, stored)
		assert.Equal(t, 1, failed)
		got, err := repo.ListByUser(ctx, fan)
		require.NoError(t, err)
		assert.Len(t, got, 1, "one entry is stored")
	})

	t.Run("unknown stock", func(t *testing.T) {
		f := seedResaleStock(t, 2, 4)
		e := newResaleEntry(f, newUserID(t), 1)
		e.Order.TicketStockID = entity.TicketStockID(entity.NewID())

		_, err := repo.Create(ctx, e)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestResaleEntryRepository_Get(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewResaleEntryRepository(testDB, testOutbox)

	t.Run("existing entry", func(t *testing.T) {
		// @spec components/entity/resale-entry/get "Existing entry"
		f := seedResaleStock(t, 2, 4)
		created, err := repo.Create(ctx, newResaleEntry(f, newUserID(t), 1))
		require.NoError(t, err)
		setResaleEntryState(t, created.ID, entity.ResaleEntryStateLapsed)

		got, err := repo.Get(ctx, created.ID)

		require.NoError(t, err)
		assert.Equal(t, entity.ResaleEntryStateLapsed, got.State)
		require.NotNil(t, got.Order)
		assert.Equal(t, created.OrderID, got.Order.ID)
		assert.Equal(t, entity.OrderStatusVoid, got.Order.Status)
	})

	t.Run("unknown id", func(t *testing.T) {
		// @spec components/entity/resale-entry/get "Unknown id"
		_, err := repo.Get(ctx, entity.ResaleEntryID(entity.NewID()))
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestResaleEntryRepository_ListByUser(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewResaleEntryRepository(testDB, testOutbox)

	t.Run("entered and lapsed", func(t *testing.T) {
		// @spec components/entity/resale-entry/list-by-user "Entered and Lapsed"
		fan := newUserID(t)
		older, err := repo.Create(ctx, newResaleEntry(seedResaleStock(t, 2, 4), fan, 1))
		require.NoError(t, err)
		setResaleEntryState(t, older.ID, entity.ResaleEntryStateLapsed)
		newer, err := repo.Create(ctx, newResaleEntry(seedResaleStock(t, 2, 4), fan, 1))
		require.NoError(t, err)

		got, err := repo.ListByUser(ctx, fan)

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, newer.ID, got[0].ID, "the Entered one, made later, first")
		assert.Equal(t, older.ID, got[1].ID)
		assert.Equal(t, entity.ResaleEntryStateLapsed, got[1].State)
		assert.NotNil(t, got[0].Order)
	})

	t.Run("no entries", func(t *testing.T) {
		// @spec components/entity/resale-entry/list-by-user "No entries"
		got, err := repo.ListByUser(ctx, newUserID(t))
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestResaleEntryRepository_ListEnteredByTicketStock(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewResaleEntryRepository(testDB, testOutbox)

	t.Run("mixed states", func(t *testing.T) {
		// @spec components/entity/resale-entry/list-entered-by-ticket-stock "Mixed states"
		f := seedResaleStock(t, 2, 4)
		ids := make([]entity.ResaleEntryID, 4)
		for i := range ids {
			e, err := repo.Create(ctx, newResaleEntry(f, newUserID(t), 1))
			require.NoError(t, err)
			ids[i] = e.ID
		}
		setResaleEntryState(t, ids[2], entity.ResaleEntryStateWon)
		setResaleEntryState(t, ids[3], entity.ResaleEntryStateLapsed)

		got, err := repo.ListEnteredByTicketStock(ctx, f.ID)

		require.NoError(t, err)
		gotIDs := make([]entity.ResaleEntryID, 0, len(got))
		for _, e := range got {
			gotIDs = append(gotIDs, e.ID)
			require.NotNil(t, e.CardAuthorizationExpireTime, "each with its card authorization's expiry")
		}
		assert.ElementsMatch(t, ids[:2], gotIDs)
	})

	t.Run("no candidates", func(t *testing.T) {
		// @spec components/entity/resale-entry/list-entered-by-ticket-stock "No candidates"
		got, err := repo.ListEnteredByTicketStock(ctx, seedResaleStock(t, 2, 4).ID)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}
