package migrationtest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// notificationSubjectPrevious is the last migration before
// 20261107120000_notification_subject.sql.
const notificationSubjectPrevious = "20261106120001"

// notificationSubjectVersion is 20261107120000_notification_subject.sql.
const notificationSubjectVersion = "20261107120000"

// TestMigration_NotificationSubject loads a notification in the old shape, a
// fan with a preferred language and a sent reminder of each of the stages 1, 2
// and 4, applies 20261107120000_notification_subject.sql and checks that only
// the notification and the result-day reminder are gone, that the fan keeps
// their language, and that the result-day stage and a user without a language
// are now rejected.
func TestMigration_NotificationSubject(t *testing.T) {
	ctx := context.Background()
	conn, apply := setupScratchDatabase(t, ctx)
	apply(notificationSubjectPrevious)

	var (
		seriesID       = id(1)
		saleID         = id(2)
		fanID          = id(3)
		notificationID = id(4)
	)
	mustExec := func(sql string, args ...any) {
		t.Helper()
		_, err := conn.Exec(ctx, sql, args...)
		require.NoError(t, err, sql)
	}
	mustExec(`INSERT INTO users (id, provider_user_id, name, email, preferred_language) VALUES ($1, 'ext-1', 'Fan', 'fan@example.com', 'ja')`, fanID)
	mustExec(`INSERT INTO notifications (id, user_id, type, payload) VALUES ($1, $2, 'new_concerts', '{}')`, notificationID, fanID)
	mustExec(`INSERT INTO series (id, title, type) VALUES ($1, 'Tour 2026', 'TOUR')`, seriesID)
	mustExec(`INSERT INTO ticket_sales (id, series_id, name, method, sale_start_at, sale_end_at, discovered_at)
		VALUES ($1, $2, 'Presale', 1, NOW(), NOW() + INTERVAL '1 day', NOW())`, saleID, seriesID)
	for _, stage := range []int{1, 2, 4} {
		mustExec(`INSERT INTO ticket_sale_reminders (id, user_id, ticket_sale_id, stage) VALUES ($1, $2, $3, $4)`, id(10+stage), fanID, saleID, stage)
	}

	apply(notificationSubjectVersion)

	t.Run("the rendered notifications are gone", func(t *testing.T) {
		var n int
		require.NoError(t, conn.QueryRow(ctx, `SELECT count(*) FROM notifications`).Scan(&n))
		assert.Zero(t, n)
	})

	t.Run("only the result-day reminder is gone", func(t *testing.T) {
		rows, err := conn.Query(ctx, `SELECT stage FROM ticket_sale_reminders ORDER BY stage`)
		require.NoError(t, err)
		defer rows.Close()
		var stages []int
		for rows.Next() {
			var stage int
			require.NoError(t, rows.Scan(&stage))
			stages = append(stages, stage)
		}
		require.NoError(t, rows.Err())
		assert.Equal(t, []int{1, 2}, stages)
	})

	t.Run("the fan keeps their language", func(t *testing.T) {
		var lang string
		require.NoError(t, conn.QueryRow(ctx, `SELECT preferred_language FROM users WHERE id = $1`, fanID).Scan(&lang))
		assert.Equal(t, "ja", lang)
	})

	t.Run("the result-day stage is rejected", func(t *testing.T) {
		_, err := conn.Exec(ctx, `INSERT INTO ticket_sale_reminders (id, user_id, ticket_sale_id, stage) VALUES ($1, $2, $3, 4)`, id(14), fanID, saleID)
		assert.Error(t, err)
	})

	t.Run("a user without a language is rejected", func(t *testing.T) {
		_, err := conn.Exec(ctx, `INSERT INTO users (id, provider_user_id, name, email) VALUES ($1, 'ext-2', 'Other', 'other@example.com')`, id(5))
		assert.Error(t, err)
	})
}
