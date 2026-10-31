package rdb_test

import (
	"context"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/jackc/pgx/v5"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

func TestLotteryEntryRepository_PersistDrawOutcomeAnnounces(t *testing.T) {
	ctx := context.Background()
	repo := rdb.NewLotteryEntryRepository(testDB, testOutbox)
	drawnOf := func(t *testing.T, mark int64, saleID entity.TicketSaleID) map[string]*entityv1.LotteryEntry {
		t.Helper()
		out := map[string]*entityv1.LotteryEntry{}
		for _, e := range announcedSince(t, mark, entity.SubjectLotteryEntryDrawn, func() *entityv1.LotteryEntry { return &entityv1.LotteryEntry{} },
			func(e *entityv1.LotteryEntry) bool { return e.GetTicketSaleId().GetValue() == string(saleID) }) {
			out[e.GetId().GetValue()] = e
		}
		return out
	}
	winner := func(e *entity.LotteryEntry, position int) entity.DrawWinnerRow {
		return entity.DrawWinnerRow{LotteryEntryID: e.ID, CardAuthorizationID: e.Order.CardAuthorizationID, DrawPosition: position}
	}
	loser := func(e *entity.LotteryEntry, position int) entity.DrawLoserRow {
		return entity.DrawLoserRow{LotteryEntryID: e.ID, CardAuthorizationID: e.Order.CardAuthorizationID, DrawPosition: position, LostReason: entity.LotteryEntryLostReasonNotDrawn}
	}

	t.Run("two entries, one place", func(t *testing.T) {
		// @spec components/entity/lottery-entry/persist-draw-outcome "Two entries, one place"
		f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
		won := insertLotteryEntry(t, f, f.SaleID, newUserID(t), 2, entity.LotteryEntryStateEntered)
		lost := insertLotteryEntry(t, f, f.SaleID, newUserID(t), 2, entity.LotteryEntryStateEntered)
		mark := outboxMark(t)

		require.NoError(t, repo.PersistDrawOutcome(ctx, f.SaleID, []entity.DrawWinnerRow{winner(won, 0)}, []entity.DrawLoserRow{loser(lost, 1)}))

		got := drawnOf(t, mark, f.SaleID)
		require.Len(t, got, 2)
		assert.Equal(t, entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_WON, got[string(won.ID)].GetState())
		assert.Equal(t, string(won.OrderID), got[string(won.ID)].GetOrderId().GetValue())
		assert.Equal(t, entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_LOST, got[string(lost.ID)].GetState())
		assert.Equal(t, entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_NOT_DRAWN, got[string(lost.ID)].GetLostReason())
		assert.Equal(t, int64(1), got[string(lost.ID)].GetDrawPosition())
	})

	t.Run("recorded again announces nothing", func(t *testing.T) {
		// @spec components/entity/lottery-entry/persist-draw-outcome "Recorded again announces nothing"
		f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
		won := insertLotteryEntry(t, f, f.SaleID, newUserID(t), 2, entity.LotteryEntryStateEntered)
		lost := insertLotteryEntry(t, f, f.SaleID, newUserID(t), 2, entity.LotteryEntryStateEntered)
		winners, losers := []entity.DrawWinnerRow{winner(won, 0)}, []entity.DrawLoserRow{loser(lost, 1)}
		require.NoError(t, repo.PersistDrawOutcome(ctx, f.SaleID, winners, losers))
		mark := outboxMark(t)

		require.NoError(t, repo.PersistDrawOutcome(ctx, f.SaleID, winners, losers))

		assert.Empty(t, drawnOf(t, mark, f.SaleID))
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/lottery-entry/persist-draw-outcome "Announcement cannot be recorded"
		f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
		won := insertLotteryEntry(t, f, f.SaleID, newUserID(t), 2, entity.LotteryEntryStateEntered)
		lost := insertLotteryEntry(t, f, f.SaleID, newUserID(t), 2, entity.LotteryEntryStateEntered)

		err := rdb.NewLotteryEntryRepository(testDB, failingOutbox{}).PersistDrawOutcome(ctx, f.SaleID,
			[]entity.DrawWinnerRow{winner(won, 0)}, []entity.DrawLoserRow{loser(lost, 1)})

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		for _, e := range []*entity.LotteryEntry{won, lost} {
			got, err := repo.Get(ctx, e.ID)
			require.NoError(t, err)
			assert.Equal(t, entity.LotteryEntryStateEntered, got.State)
			assert.Equal(t, entity.LotteryEntryLostReasonUnspecified, got.LostReason)
			assert.Equal(t, entity.OrderStatusPending, orderStatus(t, e.OrderID))
		}
	})
}

// TestLotteryEntryRepository_PersistDrawOutcomeLargeDraw draws 100,000
// entries in one call and checks the process heap stays below 200 MB: the
// announcements are built and inserted in chunks, never all at once.
func TestLotteryEntryRepository_PersistDrawOutcomeLargeDraw(t *testing.T) {
	// It seeds and draws 100,000 rows, so it runs only when asked:
	// TEST_LARGE_DRAW=1 go test ./internal/infrastructure/database/rdb -run LargeDraw
	if os.Getenv("TEST_LARGE_DRAW") == "" {
		t.Skip("set TEST_LARGE_DRAW=1 to draw 100,000 entries")
	}
	const entries = 100_000
	ctx := context.Background()
	repo := rdb.NewLotteryEntryRepository(testDB, testOutbox)
	f := seedStock(t, entity.TicketSaleMethodLottery, entries*2, 4)
	userID := newUserID(t)

	rows, err := testDB.Pool.Query(ctx, `
		WITH o AS (
			INSERT INTO orders (id, user_id, ticket_stock_id, ticket_count, card_authorization_id, provider, status, amount, currency)
			SELECT uuidv7(), $1, $2, 1, 'pi_' || gen_random_uuid(), 1, 4, 1000, 'JPY' FROM generate_series(1, $3)
			RETURNING id, card_authorization_id
		), e AS (
			INSERT INTO lottery_entries (id, order_id, ticket_sale_id, state)
			SELECT uuidv7(), o.id, $4, 1 FROM o
			RETURNING id, order_id
		)
		SELECT e.id::text, o.card_authorization_id FROM e JOIN o ON o.id = e.order_id`,
		string(userID), string(f.ID), entries, string(f.SaleID))
	require.NoError(t, err)
	type seeded struct {
		ID   string
		Card string
	}
	all, err := pgx.CollectRows(rows, pgx.RowToStructByPos[seeded])
	require.NoError(t, err)
	require.Len(t, all, entries)

	winners := make([]entity.DrawWinnerRow, 0, entries/2)
	losers := make([]entity.DrawLoserRow, 0, entries/2)
	for i, s := range all {
		if i%2 == 0 {
			winners = append(winners, entity.DrawWinnerRow{LotteryEntryID: entity.LotteryEntryID(s.ID), CardAuthorizationID: s.Card, DrawPosition: i})
		} else {
			losers = append(losers, entity.DrawLoserRow{LotteryEntryID: entity.LotteryEntryID(s.ID), CardAuthorizationID: s.Card, DrawPosition: i, LostReason: entity.LotteryEntryLostReasonNotDrawn})
		}
	}
	mark := outboxMark(t)
	runtime.GC()

	var peak atomic.Uint64
	stop := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		var m runtime.MemStats
		for {
			runtime.ReadMemStats(&m)
			if m.HeapAlloc > peak.Load() {
				peak.Store(m.HeapAlloc)
			}
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()
	err = repo.PersistDrawOutcome(ctx, f.SaleID, winners, losers)
	close(stop)
	<-sampled

	require.NoError(t, err)
	assert.Less(t, peak.Load(), uint64(200<<20), "heap peaked at %d MB", peak.Load()>>20)
	var announced int
	require.NoError(t, testDB.Pool.QueryRow(ctx,
		`SELECT count(*) FROM watermill_events_outbox WHERE "offset" > $1 AND payload->>'destination_topic' = $2`,
		mark, entity.SubjectLotteryEntryDrawn).Scan(&announced))
	assert.Equal(t, entries, announced)
}
