package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

// seedFanUser seeds a user with unique email and provider user ID.
func seedFanUser(t *testing.T, db *rdb.Database) string {
	t.Helper()
	return seedUser(t, db, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
}

func TestTicketRepository_ListByUser(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()
	repo := rdb.NewTicketRepository(db, testOutbox)

	t.Run("user with tickets", func(t *testing.T) {
		// @spec components/entity/ticket/list-by-user "User with tickets"
		cleanTables(t, db)
		fan := seedFanUser(t, db)
		may1 := seedIssuedTickets(t, db, seedAdmissionEvent(t, db), fan, 1)[0]
		may3 := seedIssuedTickets(t, db, seedAdmissionEvent(t, db), fan, 1)[0]
		setIssuedAt(t, db, may1, time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC))
		setIssuedAt(t, db, may3, time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC))
		seedIssuedTickets(t, db, seedAdmissionEvent(t, db), seedFanUser(t, db), 1)

		got, err := repo.ListByUser(ctx, entity.UserID(fan))

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, may3, got[0].ID, "most recently issued first")
		assert.Equal(t, may1, got[1].ID)
		assert.Equal(t, entity.UserID(fan), got[0].UserID)
	})

	t.Run("voided tickets included", func(t *testing.T) {
		// @spec components/entity/ticket/list-by-user "Voided tickets included"
		cleanTables(t, db)
		fan := seedFanUser(t, db)
		ids := seedIssuedTickets(t, db, seedAdmissionEvent(t, db), fan, 2)
		_, err := db.Pool.Exec(ctx, `UPDATE tickets SET status = 2 WHERE id = $1`, string(ids[0]))
		require.NoError(t, err)

		got, err := repo.ListByUser(ctx, entity.UserID(fan))

		require.NoError(t, err)
		require.Len(t, got, 2)
		statuses := map[entity.TicketID]entity.TicketStatus{}
		for _, tk := range got {
			statuses[tk.ID] = tk.Status
		}
		assert.Equal(t, entity.TicketStatusVoided, statuses[ids[0]])
		assert.Equal(t, entity.TicketStatusIssued, statuses[ids[1]])
	})

	t.Run("no tickets", func(t *testing.T) {
		// @spec components/entity/ticket/list-by-user "No tickets"
		cleanTables(t, db)

		got, err := repo.ListByUser(ctx, entity.UserID(seedFanUser(t, db)))

		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})
}

func TestTicketRepository_ListByUserAndEvent(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()
	repo := rdb.NewTicketRepository(db, testOutbox)

	t.Run("group of tickets", func(t *testing.T) {
		// @spec components/entity/ticket/list-by-user-and-event "Group of tickets"
		cleanTables(t, db)
		eventID := seedAdmissionEvent(t, db)
		fan := seedFanUser(t, db)
		group := seedIssuedTickets(t, db, eventID, fan, 3)
		seedIssuedTickets(t, db, seedAdmissionEvent(t, db), fan, 1)
		_, err := db.Pool.Exec(ctx, `UPDATE tickets SET status = 2 WHERE id = $1`, string(group[2]))
		require.NoError(t, err)

		got, err := repo.ListByUserAndEvent(ctx, entity.UserID(fan), eventID)

		require.NoError(t, err)
		require.Len(t, got, 3)
		for i, tk := range got {
			assert.Equal(t, group[i], tk.ID, "issue order")
			assert.Equal(t, eventID, tk.EventID)
		}
		assert.Equal(t, entity.TicketStatusVoided, got[2].Status, "Voided tickets are listed too")
	})

	t.Run("no tickets for the event", func(t *testing.T) {
		// @spec components/entity/ticket/list-by-user-and-event "No tickets for the event"
		cleanTables(t, db)
		eventID := seedAdmissionEvent(t, db)
		fan := seedFanUser(t, db)
		seedIssuedTickets(t, db, seedAdmissionEvent(t, db), fan, 1)

		got, err := repo.ListByUserAndEvent(ctx, entity.UserID(fan), eventID)

		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})
}

// setIssuedAt overwrites a ticket's issue time.
func setIssuedAt(t *testing.T, db *rdb.Database, id entity.TicketID, at time.Time) {
	t.Helper()
	_, err := db.Pool.Exec(context.Background(), `UPDATE tickets SET issue_at = $2 WHERE id = $1`, string(id), at)
	require.NoError(t, err)
}
