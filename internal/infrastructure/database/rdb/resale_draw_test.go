package rdb_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

// resaleDrawTime is the draw the fixtures record: 12:00 the day after the
// fans applied.
var resaleDrawTime = time.Date(2026, 12, 11, 12, 0, 0, 0, resaleJST)

// seedListing lists one Ticket of the stock, issued to a new fan, and returns
// the listing's id.
func seedListing(t *testing.T, db *rdb.Database, f *stockFixture) entity.ResaleListingID {
	t.Helper()
	ctx := context.Background()
	seller := newUserID(t, db)
	orderID := seedPaidOrder(t, db, f, string(seller), 1, issuedTicketsTime)
	ticketID, listingID := entity.NewID(), entity.NewID()
	_, err := db.Pool.Exec(ctx, `INSERT INTO tickets (id, order_id, user_id, event_id, status, issue_at) VALUES ($1, $2, $3, $4, 1, $5)`,
		ticketID, string(orderID), string(seller), f.EventID, issuedTicketsTime)
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, `INSERT INTO resale_listings (id, ticket_id, user_id, ticket_stock_id, price, status, created_at)
		VALUES ($1, $2, $3, $4, 3000, 1, $5)`, listingID, ticketID, string(seller), string(f.ID), resaleApplyTime)
	require.NoError(t, err)
	return entity.ResaleListingID(listingID)
}

// listingEntry returns the entry the listing is allocated to, empty when none.
func listingEntry(t *testing.T, db *rdb.Database, id entity.ResaleListingID) entity.ResaleEntryID {
	t.Helper()
	var entryID *string
	require.NoError(t, db.Pool.QueryRow(context.Background(), `SELECT resale_entry_id::text FROM resale_listings WHERE id = $1`, string(id)).Scan(&entryID))
	if entryID == nil {
		return ""
	}
	return entity.ResaleEntryID(*entryID)
}

// enterResale stores an Entered entry of a new fan for count tickets.
func enterResale(t *testing.T, db *rdb.Database, repo *rdb.ResaleEntryRepository, f *stockFixture, count int) *entity.ResaleEntry {
	t.Helper()
	e, err := repo.Create(context.Background(), newResaleEntry(db, f, newUserID(t, db), count))
	require.NoError(t, err)
	return e
}

func TestResaleEntryRepository_PersistDrawOutcome(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()
	repo := rdb.NewResaleEntryRepository(db, testOutbox)
	// announced returns the given entries announced under subject since mark,
	// by id.
	announced := func(t *testing.T, mark int64, subject string, ids ...entity.ResaleEntryID) map[entity.ResaleEntryID]*entityv1.ResaleEntry {
		t.Helper()
		want := map[string]bool{}
		for _, id := range ids {
			want[string(id)] = true
		}
		out := map[entity.ResaleEntryID]*entityv1.ResaleEntry{}
		for _, e := range announcedSince(t, db, mark, subject, func() *entityv1.ResaleEntry { return &entityv1.ResaleEntry{} },
			func(e *entityv1.ResaleEntry) bool { return want[e.GetId().GetValue()] }) {
			out[entity.ResaleEntryID(e.GetId().GetValue())] = e
		}
		return out
	}

	t.Run("outcome recorded", func(t *testing.T) {
		// @spec components/entity/resale-entry/persist-draw-outcome "Outcome recorded"
		f := seedResaleStock(t, db, 4, 4)
		l1, l2 := seedListing(t, db, f), seedListing(t, db, f)
		winner, lapsed := enterResale(t, db, repo, f, 2), enterResale(t, db, repo, f, 1)

		err := repo.PersistDrawOutcome(ctx, f.ID, resaleDrawTime, entity.ResaleDrawOutcome{
			Winners: []entity.ResaleWin{{Entry: winner, Listings: []entity.ResaleListingID{l1, l2}}},
			Lapsed:  []*entity.ResaleEntry{lapsed},
		})

		require.NoError(t, err)
		won, err := repo.Get(ctx, winner.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ResaleEntryStateWon, won.State)
		require.NotNil(t, won.DrawTime)
		assert.True(t, won.DrawTime.Equal(resaleDrawTime))
		assert.Equal(t, entity.OrderStatusPending, won.Order.Status, "a winner's Order is Pending until charged")
		assert.Equal(t, winner.ID, listingEntry(t, db, l1))
		assert.Equal(t, winner.ID, listingEntry(t, db, l2))
		gone, err := repo.Get(ctx, lapsed.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ResaleEntryStateLapsed, gone.State)
		require.NotNil(t, gone.DrawTime)
		assert.True(t, gone.DrawTime.Equal(resaleDrawTime))
		assert.Equal(t, entity.OrderStatusVoid, gone.Order.Status)
	})

	t.Run("listing withdrawn meanwhile", func(t *testing.T) {
		// @spec components/entity/resale-entry/persist-draw-outcome "Listing withdrawn meanwhile"
		f := seedResaleStock(t, db, 4, 4)
		l1, l2 := seedListing(t, db, f), seedListing(t, db, f)
		winner := enterResale(t, db, repo, f, 2)
		require.NoError(t, rdb.NewResaleListingRepository(db, testOutbox).Withdraw(ctx, l2, resaleApplyTime))

		err := repo.PersistDrawOutcome(ctx, f.ID, resaleDrawTime, entity.ResaleDrawOutcome{
			Winners: []entity.ResaleWin{{Entry: winner, Listings: []entity.ResaleListingID{l1, l2}}},
		})

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		got, err := repo.Get(ctx, winner.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ResaleEntryStateEntered, got.State, "nothing is recorded")
		assert.Empty(t, listingEntry(t, db, l1))
	})

	t.Run("last draw", func(t *testing.T) {
		// @spec components/entity/resale-entry/persist-draw-outcome "Last draw"
		f := seedResaleStock(t, db, 4, 4)
		a, b := enterResale(t, db, repo, f, 1), enterResale(t, db, repo, f, 2)

		require.NoError(t, repo.PersistDrawOutcome(ctx, f.ID, resaleDrawTime, entity.ResaleDrawOutcome{
			Lost: []*entity.ResaleEntry{a, b}, LostReason: entity.ResaleEntryLostReasonNoTicketLeft,
		}))

		for _, e := range []*entity.ResaleEntry{a, b} {
			got, err := repo.Get(ctx, e.ID)
			require.NoError(t, err)
			assert.Equal(t, entity.ResaleEntryStateLost, got.State)
			assert.Equal(t, entity.ResaleEntryLostReasonNoTicketLeft, got.LostReason)
			assert.Equal(t, entity.OrderStatusVoid, got.Order.Status)
		}
	})

	t.Run("a listing whose winner could not be charged is drawn again", func(t *testing.T) {
		f := seedResaleStock(t, db, 4, 4)
		l := seedListing(t, db, f)
		first := enterResale(t, db, repo, f, 1)
		require.NoError(t, repo.PersistDrawOutcome(ctx, f.ID, resaleDrawTime, entity.ResaleDrawOutcome{
			Winners: []entity.ResaleWin{{Entry: first, Listings: []entity.ResaleListingID{l}}},
		}))
		second := enterResale(t, db, repo, f, 1)
		err := repo.PersistDrawOutcome(ctx, f.ID, resaleDrawTime.Add(24*time.Hour), entity.ResaleDrawOutcome{
			Winners: []entity.ResaleWin{{Entry: second, Listings: []entity.ResaleListingID{l}}},
		})
		require.ErrorIs(t, err, apperr.ErrFailedPrecondition, "allocated to a Won entry, it is not free")

		setResaleEntryState(t, db, first.ID, entity.ResaleEntryStateLost)
		require.NoError(t, repo.PersistDrawOutcome(ctx, f.ID, resaleDrawTime.Add(24*time.Hour), entity.ResaleDrawOutcome{
			Winners: []entity.ResaleWin{{Entry: second, Listings: []entity.ResaleListingID{l}}},
		}))
		assert.Equal(t, second.ID, listingEntry(t, db, l))
	})

	t.Run("three entries recorded", func(t *testing.T) {
		// @spec components/entity/resale-entry/persist-draw-outcome "Three entries recorded"
		f := seedResaleStock(t, db, 4, 4)
		l := seedListing(t, db, f)
		won, lapsed, lost := enterResale(t, db, repo, f, 1), enterResale(t, db, repo, f, 1), enterResale(t, db, repo, f, 1)
		mark := outboxMark(t, db)

		require.NoError(t, repo.PersistDrawOutcome(ctx, f.ID, resaleDrawTime, entity.ResaleDrawOutcome{
			Winners: []entity.ResaleWin{{Entry: won, Listings: []entity.ResaleListingID{l}}},
			Lapsed:  []*entity.ResaleEntry{lapsed},
			Lost:    []*entity.ResaleEntry{lost}, LostReason: entity.ResaleEntryLostReasonNoTicketLeft,
		}))

		all := []entity.ResaleEntryID{won.ID, lapsed.ID, lost.ID}
		gotWon := announced(t, mark, entity.SubjectResaleEntryWon, all...)
		require.Len(t, gotWon, 1, "one announcement as won")
		require.Contains(t, gotWon, won.ID)
		assert.Equal(t, entityv1.ResaleEntryState_RESALE_ENTRY_STATE_WON, gotWon[won.ID].GetState())
		assert.Equal(t, string(won.OrderID), gotWon[won.ID].GetOrderId().GetValue())
		gotLapsed := announced(t, mark, entity.SubjectResaleEntryLapsed, all...)
		require.Len(t, gotLapsed, 1, "one announcement as lapsed")
		require.Contains(t, gotLapsed, lapsed.ID)
		assert.Equal(t, entityv1.ResaleEntryState_RESALE_ENTRY_STATE_LAPSED, gotLapsed[lapsed.ID].GetState())
		gotLost := announced(t, mark, entity.SubjectResaleEntryLost, all...)
		require.Len(t, gotLost, 1, "one announcement as lost")
		require.Contains(t, gotLost, lost.ID)
		assert.Equal(t, entityv1.ResaleEntryState_RESALE_ENTRY_STATE_LOST, gotLost[lost.ID].GetState())
		assert.Equal(t, entityv1.ResaleEntryLostReason_RESALE_ENTRY_LOST_REASON_NO_TICKET_LEFT, gotLost[lost.ID].GetLostReason())
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/resale-entry/persist-draw-outcome "Announcement cannot be recorded"
		f := seedResaleStock(t, db, 4, 4)
		l := seedListing(t, db, f)
		won, lapsed := enterResale(t, db, repo, f, 1), enterResale(t, db, repo, f, 1)

		err := rdb.NewResaleEntryRepository(db, failingOutbox{}).PersistDrawOutcome(ctx, f.ID, resaleDrawTime, entity.ResaleDrawOutcome{
			Winners: []entity.ResaleWin{{Entry: won, Listings: []entity.ResaleListingID{l}}},
			Lapsed:  []*entity.ResaleEntry{lapsed},
		})

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		for _, e := range []*entity.ResaleEntry{won, lapsed} {
			got, err := repo.Get(ctx, e.ID)
			require.NoError(t, err)
			assert.Equal(t, entity.ResaleEntryStateEntered, got.State)
			assert.Equal(t, entity.OrderStatusPending, got.Order.Status)
		}
		assert.Empty(t, listingEntry(t, db, l), "no listing changes")
	})
}

func TestResaleListingRepository_WithdrawFreeOnly(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()
	entries := rdb.NewResaleEntryRepository(db, testOutbox)
	listings := rdb.NewResaleListingRepository(db, testOutbox)
	status := func(t *testing.T, id entity.ResaleListingID) entity.ResaleListingStatus {
		t.Helper()
		var s int16
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT status FROM resale_listings WHERE id = $1`, string(id)).Scan(&s))
		return entity.ResaleListingStatus(s)
	}

	t.Run("nobody is buying it", func(t *testing.T) {
		// @spec components/entity/resale-listing/withdraw "Nobody is buying it"
		f := seedResaleStock(t, db, 4, 4)
		l := seedListing(t, db, f)

		require.NoError(t, listings.Withdraw(ctx, l, resaleApplyTime))

		assert.Equal(t, entity.ResaleListingStatusWithdrawn, status(t, l))
	})

	t.Run("another fan won it", func(t *testing.T) {
		// @spec components/entity/resale-listing/withdraw "Another fan won it"
		f := seedResaleStock(t, db, 4, 4)
		l := seedListing(t, db, f)
		winner := enterResale(t, db, entries, f, 1)
		require.NoError(t, entries.PersistDrawOutcome(ctx, f.ID, resaleDrawTime, entity.ResaleDrawOutcome{
			Winners: []entity.ResaleWin{{Entry: winner, Listings: []entity.ResaleListingID{l}}},
		}))

		err := listings.Withdraw(ctx, l, resaleDrawTime.Add(time.Minute))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Equal(t, entity.ResaleListingStatusListed, status(t, l))
	})

	t.Run("withdraw and draw at the same moment", func(t *testing.T) {
		// @spec components/entity/resale-listing/withdraw "Withdraw and draw at the same moment"
		for range 5 {
			f := seedResaleStock(t, db, 4, 4)
			l := seedListing(t, db, f)
			winner := enterResale(t, db, entries, f, 1)

			var withdrawErr, drawErr error
			var wg sync.WaitGroup
			start := make(chan struct{})
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				withdrawErr = listings.Withdraw(ctx, l, resaleDrawTime)
			}()
			go func() {
				defer wg.Done()
				<-start
				drawErr = entries.PersistDrawOutcome(ctx, f.ID, resaleDrawTime, entity.ResaleDrawOutcome{
					Winners: []entity.ResaleWin{{Entry: winner, Listings: []entity.ResaleListingID{l}}},
				})
			}()
			close(start)
			wg.Wait()

			switch {
			case withdrawErr == nil:
				require.True(t, errors.Is(drawErr, apperr.ErrFailedPrecondition), "the draw records nothing: %v", drawErr)
				assert.Equal(t, entity.ResaleListingStatusWithdrawn, status(t, l))
				assert.Empty(t, listingEntry(t, db, l))
			case drawErr == nil:
				require.True(t, errors.Is(withdrawErr, apperr.ErrFailedPrecondition), "the withdrawal fails: %v", withdrawErr)
				assert.Equal(t, entity.ResaleListingStatusListed, status(t, l))
				assert.Equal(t, winner.ID, listingEntry(t, db, l))
			default:
				t.Fatalf("both failed: withdraw %v, draw %v", withdrawErr, drawErr)
			}
		}
	})

	t.Run("already sold", func(t *testing.T) {
		// @spec components/entity/resale-listing/withdraw "Already sold"
		f := seedResaleStock(t, db, 4, 4)
		l := seedListing(t, db, f)
		_, err := db.Pool.Exec(ctx, `UPDATE resale_listings SET status = 2, sold_at = $2 WHERE id = $1`, string(l), resaleApplyTime)
		require.NoError(t, err)

		assert.ErrorIs(t, listings.Withdraw(ctx, l, resaleApplyTime), apperr.ErrFailedPrecondition)
	})

	t.Run("withdrawn twice", func(t *testing.T) {
		// @spec components/entity/resale-listing/withdraw "Withdrawn twice"
		f := seedResaleStock(t, db, 4, 4)
		l := seedListing(t, db, f)
		require.NoError(t, listings.Withdraw(ctx, l, resaleApplyTime))

		require.NoError(t, listings.Withdraw(ctx, l, resaleApplyTime.Add(time.Hour)))

		assert.Equal(t, entity.ResaleListingStatusWithdrawn, status(t, l))
	})
}

func TestResaleEntryRepository_Withdraw(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()
	repo := rdb.NewResaleEntryRepository(db, testOutbox)
	at := time.Date(2026, 12, 11, 10, 0, 0, 0, resaleJST)

	t.Run("entry withdrawn", func(t *testing.T) {
		// @spec components/entity/resale-entry/withdraw "Entry withdrawn"
		f := seedResaleStock(t, db, 4, 4)
		e := enterResale(t, db, repo, f, 2)

		got, err := repo.Withdraw(ctx, e.ID, at)

		require.NoError(t, err)
		assert.Equal(t, entity.ResaleEntryStateWithdrawn, got.State)
		require.NotNil(t, got.WithdrawTime)
		assert.True(t, got.WithdrawTime.Equal(at))
		stored, err := repo.Get(ctx, e.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ResaleEntryStateWithdrawn, stored.State)
		assert.Equal(t, entity.OrderStatusVoid, stored.Order.Status)
	})

	t.Run("draw recorded first", func(t *testing.T) {
		// @spec components/entity/resale-entry/withdraw "Draw recorded first"
		f := seedResaleStock(t, db, 4, 4)
		l := seedListing(t, db, f)
		e := enterResale(t, db, repo, f, 1)
		require.NoError(t, repo.PersistDrawOutcome(ctx, f.ID, resaleDrawTime, entity.ResaleDrawOutcome{
			Winners: []entity.ResaleWin{{Entry: e, Listings: []entity.ResaleListingID{l}}},
		}))

		_, err := repo.Withdraw(ctx, e.ID, resaleDrawTime)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		stored, err := repo.Get(ctx, e.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ResaleEntryStateWon, stored.State)
	})

	t.Run("withdrawn first", func(t *testing.T) {
		// @spec components/entity/resale-entry/withdraw "Withdrawn first"
		f := seedResaleStock(t, db, 4, 4)
		l := seedListing(t, db, f)
		e := enterResale(t, db, repo, f, 1)
		_, err := repo.Withdraw(ctx, e.ID, at)
		require.NoError(t, err)

		candidates, err := repo.ListEnteredByTicketStock(ctx, f.ID)
		require.NoError(t, err)
		assert.Empty(t, candidates, "the draw no longer lists it")
		err = repo.PersistDrawOutcome(ctx, f.ID, resaleDrawTime, entity.ResaleDrawOutcome{
			Winners: []entity.ResaleWin{{Entry: e, Listings: []entity.ResaleListingID{l}}},
		})
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "and allocates it no listing")
		assert.Empty(t, listingEntry(t, db, l))
	})

	t.Run("unknown entry", func(t *testing.T) {
		// @spec components/entity/resale-entry/withdraw "Unknown entry"
		_, err := repo.Withdraw(ctx, entity.ResaleEntryID(entity.NewID()), at)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("withdrawal announced", func(t *testing.T) {
		// @spec components/entity/resale-entry/withdraw "Withdrawal announced"
		f := seedResaleStock(t, db, 4, 4)
		e := enterResale(t, db, repo, f, 1)
		mark := outboxMark(t, db)

		_, err := repo.Withdraw(ctx, e.ID, at)
		require.NoError(t, err)

		got := announcedSince(t, db, mark, entity.SubjectResaleEntryWithdrawn, func() *entityv1.ResaleEntry { return &entityv1.ResaleEntry{} },
			func(m *entityv1.ResaleEntry) bool { return m.GetId().GetValue() == string(e.ID) })
		require.Len(t, got, 1, "announced as withdrawn once")
		assert.Equal(t, entityv1.ResaleEntryState_RESALE_ENTRY_STATE_WITHDRAWN, got[0].GetState())
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/resale-entry/withdraw "Announcement cannot be recorded"
		f := seedResaleStock(t, db, 4, 4)
		e := enterResale(t, db, repo, f, 1)

		_, err := rdb.NewResaleEntryRepository(db, failingOutbox{}).Withdraw(ctx, e.ID, at)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		stored, err := repo.Get(ctx, e.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ResaleEntryStateEntered, stored.State)
		assert.Equal(t, entity.OrderStatusPending, stored.Order.Status)
	})

	t.Run("withdraw and draw for one entry at the same time", func(t *testing.T) {
		for range 5 {
			f := seedResaleStock(t, db, 4, 4)
			l := seedListing(t, db, f)
			e := enterResale(t, db, repo, f, 1)

			var withdrawErr, drawErr error
			var wg sync.WaitGroup
			start := make(chan struct{})
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				_, withdrawErr = repo.Withdraw(ctx, e.ID, resaleDrawTime)
			}()
			go func() {
				defer wg.Done()
				<-start
				drawErr = repo.PersistDrawOutcome(ctx, f.ID, resaleDrawTime, entity.ResaleDrawOutcome{
					Winners: []entity.ResaleWin{{Entry: e, Listings: []entity.ResaleListingID{l}}},
				})
			}()
			close(start)
			wg.Wait()

			stored, err := repo.Get(ctx, e.ID)
			require.NoError(t, err)
			switch {
			case withdrawErr == nil:
				require.ErrorIs(t, drawErr, apperr.ErrFailedPrecondition)
				assert.Equal(t, entity.ResaleEntryStateWithdrawn, stored.State)
				assert.Empty(t, listingEntry(t, db, l))
			case drawErr == nil:
				require.ErrorIs(t, withdrawErr, apperr.ErrFailedPrecondition)
				assert.Equal(t, entity.ResaleEntryStateWon, stored.State)
			default:
				t.Fatalf("both failed: withdraw %v, draw %v", withdrawErr, drawErr)
			}
		}
	})
}

// TestResaleEntryRepository_FreeListingsParity checks that the query behind a
// draw's free listings returns exactly the listings
// entity.ResaleListing.IsFreeForDrawAt accepts, for every combination of the
// listing's status and the state of the entry it is allocated to.
func TestResaleEntryRepository_FreeListingsParity(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()
	repo := rdb.NewResaleEntryRepository(db, testOutbox)
	f := seedStock(t, db, entity.TicketSaleMethodLottery, 100, 4)

	// Expired is never stored: a Listed listing past its deadline reads as
	// Expired, which a draw at its deadline or before does not meet.
	statuses := []entity.ResaleListingStatus{
		entity.ResaleListingStatusListed, entity.ResaleListingStatusSold,
		entity.ResaleListingStatusWithdrawn, entity.ResaleListingStatusCancelled,
	}
	// The zero state stands for a listing allocated to no entry.
	states := []entity.ResaleEntryState{
		0, entity.ResaleEntryStateEntered, entity.ResaleEntryStateWon, entity.ResaleEntryStateLost,
		entity.ResaleEntryStateLapsed, entity.ResaleEntryStateWithdrawn,
	}
	want := map[entity.ResaleListingID]bool{}
	var accepted, all int
	for _, status := range statuses {
		for _, state := range states {
			id := seedListing(t, db, f)
			var entryID any
			if state != 0 {
				orderID := insertOrder(t, db, f, orderSeed{user: newUserID(t, db), status: entity.OrderStatusPending})
				id2 := entity.NewID()
				var drawAt, withdrawAt, lostReason any
				switch state {
				case entity.ResaleEntryStateWon, entity.ResaleEntryStateLost, entity.ResaleEntryStateLapsed:
					drawAt = resaleApplyTime.Add(time.Hour)
				case entity.ResaleEntryStateWithdrawn:
					withdrawAt = resaleApplyTime.Add(time.Hour)
				}
				if state == entity.ResaleEntryStateLost {
					lostReason = int16(entity.ResaleEntryLostReasonNoTicketLeft)
				}
				_, err := db.Pool.Exec(ctx, `INSERT INTO resale_entries (id, order_id, state, draw_at, withdraw_at, lost_reason, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
					id2, string(orderID), int16(state), drawAt, withdrawAt, lostReason, resaleApplyTime)
				require.NoError(t, err)
				entryID = id2
			}
			var soldAt, withdrawAt any
			switch status {
			case entity.ResaleListingStatusSold:
				soldAt = resaleApplyTime.Add(time.Hour)
			case entity.ResaleListingStatusWithdrawn:
				withdrawAt = resaleApplyTime.Add(time.Hour)
			}
			_, err := db.Pool.Exec(ctx, `UPDATE resale_listings SET status = $2, resale_entry_id = $3, sold_at = $4, withdraw_at = $5 WHERE id = $1`,
				string(id), int16(status), entryID, soldAt, withdrawAt)
			require.NoError(t, err, "status %s, entry state %s", status, state)

			listing := &entity.ResaleListing{Status: status, ResaleEntryState: state}
			if state != 0 {
				listing.ResaleEntryID = entity.ResaleEntryID("allocated")
			}
			want[id] = listing.IsFreeForDrawAt(resaleDrawTime, resaleDrawTime.Add(time.Hour))
			all++
			if want[id] {
				accepted++
			}
		}
	}

	var free []entity.ResaleListingID
	_, err := repo.DrawUnderLock(ctx, f.ID, resaleDrawTime, func(_ []*entity.ResaleEntry, listings []entity.ResaleListingID) entity.ResaleDrawOutcome {
		free = listings
		return entity.ResaleDrawOutcome{}
	})
	require.NoError(t, err)

	got := map[entity.ResaleListingID]bool{}
	for _, id := range free {
		got[id] = true
	}
	for id, isFree := range want {
		assert.Equal(t, isFree, got[id], "listing %s", id)
	}
	assert.Len(t, free, accepted)
	assert.Equal(t, len(statuses)*len(states), all)
	assert.Positive(t, accepted, "some combination is free")
}
