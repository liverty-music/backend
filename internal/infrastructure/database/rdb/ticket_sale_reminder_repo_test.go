package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedDiscoveredSale stores a discovered lottery of a new series and returns
// its id.
func seedDiscoveredSale(t *testing.T) entity.TicketSaleID {
	t.Helper()
	t0 := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	id, _, err := rdb.NewTicketSaleRepository(testDB).UpsertDiscovered(context.Background(), &entity.DiscoveredTicketSale{
		SeriesID:      seedSeriesOnly(t, "Reminder Tour"),
		Name:          "オフィシャル先行",
		Method:        entity.TicketSaleMethodLottery,
		SaleStartTime: t0,
		SaleEndTime:   t0.Add(72 * time.Hour),
	})
	require.NoError(t, err)
	return id
}

func TestTicketSaleReminderRepository_AlreadySent(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewTicketSaleReminderRepository(testDB)
	cleanDatabase(t)

	saleID := seedDiscoveredSale(t)
	userID := seedUser(t, "UserAS", "useras@example.com", "ext-as")
	require.NoError(t, repo.RecordSent(ctx, userID, saleID, entity.ReminderStageApplyClose24H))

	t.Run("recorded", func(t *testing.T) {
		// @spec components/entity/ticket-sale-reminder/already-sent "Recorded"
		sent, err := repo.AlreadySent(ctx, userID, saleID, entity.ReminderStageApplyClose24H)
		require.NoError(t, err)
		assert.True(t, sent)
	})

	t.Run("not recorded", func(t *testing.T) {
		// @spec components/entity/ticket-sale-reminder/already-sent "Not recorded"
		sent, err := repo.AlreadySent(ctx, userID, saleID, entity.ReminderStageApplyOpen)
		require.NoError(t, err)
		assert.False(t, sent)
	})

	t.Run("missing ticket sale", func(t *testing.T) {
		// @spec components/entity/ticket-sale-reminder/already-sent "Missing ticket sale"
		_, err := repo.AlreadySent(ctx, userID, "", entity.ReminderStageApplyOpen)
		assertAppErrCode(t, err, codes.InvalidArgument)
	})
}

func TestTicketSaleReminderRepository_ListSentStages(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewTicketSaleReminderRepository(testDB)
	cleanDatabase(t)

	saleID := seedDiscoveredSale(t)
	userA := seedUser(t, "UserLA", "userla@example.com", "ext-la")
	userB := seedUser(t, "UserLB", "userlb@example.com", "ext-lb")
	require.NoError(t, repo.RecordSent(ctx, userA, saleID, entity.ReminderStageApplyOpen))
	require.NoError(t, repo.RecordSent(ctx, userA, saleID, entity.ReminderStageApplyClose24H))

	t.Run("some stages sent", func(t *testing.T) {
		// @spec components/entity/ticket-sale-reminder/list-sent-stages "Some stages sent"
		sent, err := repo.ListSentStages(ctx, saleID, []string{userA, userB})
		require.NoError(t, err)
		assert.Equal(t, map[string]map[entity.ReminderStage]bool{
			userA: {entity.ReminderStageApplyOpen: true, entity.ReminderStageApplyClose24H: true},
		}, sent)
	})

	t.Run("no users given", func(t *testing.T) {
		// @spec components/entity/ticket-sale-reminder/list-sent-stages "No users given"
		sent, err := repo.ListSentStages(ctx, saleID, nil)
		require.NoError(t, err)
		assert.Empty(t, sent)
	})

	t.Run("missing ticket sale", func(t *testing.T) {
		// @spec components/entity/ticket-sale-reminder/list-sent-stages "Missing ticket sale"
		_, err := repo.ListSentStages(ctx, "", []string{userA})
		assertAppErrCode(t, err, codes.InvalidArgument)
	})
}

func TestTicketSaleReminderRepository_RecordSent(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewTicketSaleReminderRepository(testDB)
	cleanDatabase(t)

	saleID := seedDiscoveredSale(t)
	userID := seedUser(t, "UserRS", "userrs@example.com", "ext-rs")
	sentAt := func(t *testing.T) []time.Time {
		t.Helper()
		rows, err := testDB.Pool.Query(ctx,
			`SELECT sent_at FROM ticket_sale_reminders WHERE user_id = $1 AND ticket_sale_id = $2 AND stage = 1`,
			userID, string(saleID))
		require.NoError(t, err)
		defer rows.Close()
		var out []time.Time
		for rows.Next() {
			var at time.Time
			require.NoError(t, rows.Scan(&at))
			out = append(out, at)
		}
		require.NoError(t, rows.Err())
		return out
	}

	t.Run("first record", func(t *testing.T) {
		// @spec components/entity/ticket-sale-reminder/record-sent "First record"
		require.NoError(t, repo.RecordSent(ctx, userID, saleID, entity.ReminderStageApplyOpen))
		assert.Len(t, sentAt(t), 1)
	})

	t.Run("repeated record", func(t *testing.T) {
		// @spec components/entity/ticket-sale-reminder/record-sent "Repeated record"
		before := sentAt(t)
		require.NoError(t, repo.RecordSent(ctx, userID, saleID, entity.ReminderStageApplyOpen))
		assert.Equal(t, before, sentAt(t), "the existing record and its sent time are unchanged")
	})

	t.Run("missing user", func(t *testing.T) {
		// @spec components/entity/ticket-sale-reminder/record-sent "Missing user"
		err := repo.RecordSent(ctx, "", saleID, entity.ReminderStageApplyOpen)
		assertAppErrCode(t, err, codes.InvalidArgument)
	})
}
