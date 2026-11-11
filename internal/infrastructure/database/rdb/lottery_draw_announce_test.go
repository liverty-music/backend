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
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()
	repo := rdb.NewLotteryEntryRepository(db, testOutbox)
	// drawnOf returns the entries of the sale announced under subject since
	// mark, by id.
	drawnOf := func(t *testing.T, mark int64, subject string, saleID entity.TicketSaleID) map[string]*entityv1.LotteryEntry {
		t.Helper()
		out := map[string]*entityv1.LotteryEntry{}
		for _, e := range announcedSince(t, db, mark, subject, func() *entityv1.LotteryEntry { return &entityv1.LotteryEntry{} },
			func(e *entityv1.LotteryEntry) bool { return e.GetTicketSaleId().GetValue() == string(saleID) }) {
			out[e.GetId().GetValue()] = e
		}
		return out
	}

	t.Run("two entries, one place", func(t *testing.T) {
		// @spec components/entity/lottery-entry/persist-draw-outcome "Two entries, one place"
		f := seedStock(t, db, entity.TicketSaleMethodLottery, 2, 4)
		won := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)
		lost := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)
		mark := outboxMark(t, db)

		require.NoError(t, repo.PersistDrawOutcome(ctx, f.SaleID, []entity.StockDrawOrder{drawOrder(f, won, lost)}))

		gotWon := drawnOf(t, mark, entity.SubjectLotteryEntryWon, f.SaleID)
		require.Len(t, gotWon, 1, "the Won entry is announced as won")
		require.Contains(t, gotWon, string(won.ID))
		assert.Equal(t, entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_WON, gotWon[string(won.ID)].GetState())
		assert.Equal(t, string(won.OrderID), gotWon[string(won.ID)].GetOrderId().GetValue())
		gotLost := drawnOf(t, mark, entity.SubjectLotteryEntryLost, f.SaleID)
		require.Len(t, gotLost, 1, "the Lost entry is announced as lost")
		require.Contains(t, gotLost, string(lost.ID))
		assert.Equal(t, entityv1.LotteryEntryState_LOTTERY_ENTRY_STATE_LOST, gotLost[string(lost.ID)].GetState())
		assert.Equal(t, entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_NOT_DRAWN, gotLost[string(lost.ID)].GetLostReason())
		assert.Equal(t, int64(1), gotLost[string(lost.ID)].GetDrawPosition())
	})

	t.Run("recorded again announces nothing", func(t *testing.T) {
		// @spec components/entity/lottery-entry/persist-draw-outcome "Recorded again announces nothing"
		f := seedStock(t, db, entity.TicketSaleMethodLottery, 2, 4)
		won := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)
		lost := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)
		order := []entity.StockDrawOrder{drawOrder(f, won, lost)}
		require.NoError(t, repo.PersistDrawOutcome(ctx, f.SaleID, order))
		mark := outboxMark(t, db)

		require.NoError(t, repo.PersistDrawOutcome(ctx, f.SaleID, order))

		assert.Empty(t, drawnOf(t, mark, entity.SubjectLotteryEntryWon, f.SaleID))
		assert.Empty(t, drawnOf(t, mark, entity.SubjectLotteryEntryLost, f.SaleID))
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/lottery-entry/persist-draw-outcome "Announcement cannot be recorded"
		f := seedStock(t, db, entity.TicketSaleMethodLottery, 2, 4)
		won := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)
		lost := insertLotteryEntry(t, db, f, f.SaleID, newUserID(t, db), 2, entity.LotteryEntryStateEntered)

		err := rdb.NewLotteryEntryRepository(db, failingOutbox{}).PersistDrawOutcome(ctx, f.SaleID, []entity.StockDrawOrder{drawOrder(f, won, lost)})

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		for _, e := range []*entity.LotteryEntry{won, lost} {
			got, err := repo.Get(ctx, e.ID)
			require.NoError(t, err)
			assert.Equal(t, entity.LotteryEntryStateEntered, got.State)
			assert.Equal(t, entity.LotteryEntryLostReasonUnspecified, got.LostReason)
			assert.Equal(t, entity.OrderStatusPending, orderStatus(t, db, e.OrderID))
		}
	})
}

// TestLotteryEntryRepository_PersistDrawOutcomeLargeDraw draws 100,000
// entries in one call and checks the process heap stays below 200 MB: the
// announcements are built and inserted in chunks, never all at once.
func TestLotteryEntryRepository_PersistDrawOutcomeLargeDraw(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	// It seeds and draws 100,000 rows, so it runs only when asked:
	// TEST_LARGE_DRAW=1 go test ./internal/infrastructure/database/rdb -run LargeDraw
	if os.Getenv("TEST_LARGE_DRAW") == "" {
		t.Skip("set TEST_LARGE_DRAW=1 to draw 100,000 entries")
	}
	const entries = 100_000
	ctx := context.Background()
	repo := rdb.NewLotteryEntryRepository(db, testOutbox)
	f := seedStock(t, db, entity.TicketSaleMethodLottery, entries*2, 4)
	userID := newUserID(t, db)

	rows, err := db.Pool.Query(ctx, `
		WITH ca AS (
			INSERT INTO card_authorizations (id, provider_card_authorization_id)
			SELECT uuidv7(), 'pi_' || gen_random_uuid() FROM generate_series(1, $3)
			RETURNING id
		), o AS (
			INSERT INTO orders (id, user_id, ticket_stock_id, ticket_count, card_authorization_id, provider, status, amount, currency)
			SELECT uuidv7(), $1, $2, 1, ca.id, 1, 4, 1000, 'JPY' FROM ca
			RETURNING id, card_authorization_id
		), e AS (
			INSERT INTO lottery_entries (id, order_id, ticket_sale_id, state)
			SELECT uuidv7(), o.id, $4, 1 FROM o
			RETURNING id, order_id
		)
		SELECT e.id::text, o.card_authorization_id::text FROM e JOIN o ON o.id = e.order_id`,
		string(userID), string(f.ID), entries, string(f.SaleID))
	require.NoError(t, err)
	type seeded struct {
		ID   string
		Card string
	}
	all, err := pgx.CollectRows(rows, pgx.RowToStructByPos[seeded])
	require.NoError(t, err)
	require.Len(t, all, entries)

	// Half the entries fit: each asks for 1 ticket of a stock of entries/2.
	_, err = db.Pool.Exec(ctx, `UPDATE ticket_stocks SET quantity = $2 WHERE id = $1`, string(f.ID), entries/2)
	require.NoError(t, err)
	order := entity.StockDrawOrder{TicketStockID: f.ID, Entries: make([]entity.DrawEntry, len(all))}
	for i, s := range all {
		order.Entries[i] = entity.DrawEntry{ID: entity.LotteryEntryID(s.ID), TicketCount: 1}
	}
	mark := outboxMark(t, db)
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
	err = repo.PersistDrawOutcome(ctx, f.SaleID, []entity.StockDrawOrder{order})
	close(stop)
	<-sampled

	require.NoError(t, err)
	assert.Less(t, peak.Load(), uint64(200<<20), "heap peaked at %d MB", peak.Load()>>20)
	var announced int
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM watermill_events_outbox WHERE "offset" > $1 AND payload->>'destination_topic' = ANY($2)`,
		mark, []string{entity.SubjectLotteryEntryWon, entity.SubjectLotteryEntryLost}).Scan(&announced))
	assert.Equal(t, entries, announced)
}
