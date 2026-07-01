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

func newTestNotification(userID string, typ entity.NotificationType) *entity.Notification {
	return &entity.Notification{
		UserID:  userID,
		Type:    typ,
		Payload: entity.NewNotificationPayload("Test Artist", "1 new concert found", "/concerts?artist=abc", "concert-abc"),
	}
}

func TestNotificationRepository_CreateAndGet(t *testing.T) {
	repo := rdb.NewNotificationRepository(testDB)
	ctx := context.Background()

	cleanDatabase(t)
	userID := seedUser(t, "notif-create-user", "notif-create@example.com", "ext-notif-create-01")

	n := newTestNotification(userID, entity.NotificationTypeNewConcerts)
	require.NoError(t, repo.Create(ctx, n))

	// Create mints a UUIDv7 id, defaults to queued, and writes back created_at.
	assert.NotEmpty(t, n.ID)
	assert.Equal(t, entity.NotificationDeliveryStatusQueued, n.DeliveryStatus)
	assert.False(t, n.QueueTime.IsZero())

	got, err := repo.Get(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, n.ID, got.ID)
	assert.Equal(t, userID, got.UserID)
	assert.Equal(t, entity.NotificationTypeNewConcerts, got.Type)
	assert.Equal(t, entity.NotificationDeliveryStatusQueued, got.DeliveryStatus)
	require.NotNil(t, got.Payload)
	assert.Equal(t, "Test Artist", got.Payload.Title)
	assert.Equal(t, "concert-abc", got.Payload.Tag)
	assert.Nil(t, got.DeliverTime)
	assert.Nil(t, got.ReadTime)
	assert.Nil(t, got.DismissTime)
	assert.Empty(t, got.FailureReason)
}

func TestNotificationRepository_Get_NotFound(t *testing.T) {
	repo := rdb.NewNotificationRepository(testDB)
	ctx := context.Background()

	cleanDatabase(t)
	_, err := repo.Get(ctx, "01890000-0000-7000-8000-000000000000")
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}

func TestNotificationRepository_UpdateDelivery(t *testing.T) {
	repo := rdb.NewNotificationRepository(testDB)
	ctx := context.Background()

	t.Run("delivered sets status and delivered_at, no failure reason", func(t *testing.T) {
		cleanDatabase(t)
		userID := seedUser(t, "notif-deliver-user", "notif-deliver@example.com", "ext-notif-deliver-01")
		n := newTestNotification(userID, entity.NotificationTypeSalesReminder)
		require.NoError(t, repo.Create(ctx, n))

		now := time.Now().UTC()
		require.NoError(t, repo.UpdateDelivery(ctx, n.ID, entity.NotificationDeliveryStatusDelivered, &now, ""))

		got, err := repo.Get(ctx, n.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.NotificationDeliveryStatusDelivered, got.DeliveryStatus)
		require.NotNil(t, got.DeliverTime)
		assert.WithinDuration(t, now, *got.DeliverTime, time.Second)
		assert.Empty(t, got.FailureReason)
	})

	t.Run("failed sets status and failure reason, no delivered_at", func(t *testing.T) {
		cleanDatabase(t)
		userID := seedUser(t, "notif-fail-user", "notif-fail@example.com", "ext-notif-fail-01")
		n := newTestNotification(userID, entity.NotificationTypeNewConcerts)
		require.NoError(t, repo.Create(ctx, n))

		require.NoError(t, repo.UpdateDelivery(ctx, n.ID, entity.NotificationDeliveryStatusFailed, nil, "push service rejected"))

		got, err := repo.Get(ctx, n.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.NotificationDeliveryStatusFailed, got.DeliveryStatus)
		assert.Nil(t, got.DeliverTime)
		assert.Equal(t, "push service rejected", got.FailureReason)
	})
}

func TestNotificationRepository_MarkRead(t *testing.T) {
	repo := rdb.NewNotificationRepository(testDB)
	ctx := context.Background()

	cleanDatabase(t)
	userID := seedUser(t, "notif-read-user", "notif-read@example.com", "ext-notif-read-01")
	otherID := seedUser(t, "notif-read-other", "notif-read-other@example.com", "ext-notif-read-other-01")
	n := newTestNotification(userID, entity.NotificationTypeNewConcerts)
	require.NoError(t, repo.Create(ctx, n))

	// First mark records read_at.
	require.NoError(t, repo.MarkRead(ctx, userID, n.ID))
	got, err := repo.Get(ctx, n.ID)
	require.NoError(t, err)
	require.NotNil(t, got.ReadTime)
	firstReadAt := *got.ReadTime

	// Second mark is a no-op: read_at is unchanged.
	require.NoError(t, repo.MarkRead(ctx, userID, n.ID))
	got, err = repo.Get(ctx, n.ID)
	require.NoError(t, err)
	require.NotNil(t, got.ReadTime)
	assert.Equal(t, firstReadAt, *got.ReadTime)

	// A different user marking is scoped out: no row matches, state untouched.
	otherNotif := newTestNotification(otherID, entity.NotificationTypeNewConcerts)
	require.NoError(t, repo.Create(ctx, otherNotif))
	require.NoError(t, repo.MarkRead(ctx, userID, otherNotif.ID)) // userID != owner otherID
	got, err = repo.Get(ctx, otherNotif.ID)
	require.NoError(t, err)
	assert.Nil(t, got.ReadTime)
}

func TestNotificationRepository_MarkDismissed(t *testing.T) {
	repo := rdb.NewNotificationRepository(testDB)
	ctx := context.Background()

	cleanDatabase(t)
	userID := seedUser(t, "notif-dismiss-user", "notif-dismiss@example.com", "ext-notif-dismiss-01")
	n := newTestNotification(userID, entity.NotificationTypeSalesPhaseAnnouncement)
	require.NoError(t, repo.Create(ctx, n))

	require.NoError(t, repo.MarkDismissed(ctx, userID, n.ID))
	got, err := repo.Get(ctx, n.ID)
	require.NoError(t, err)
	require.NotNil(t, got.DismissTime)
	first := *got.DismissTime

	// Idempotent: second call is a no-op.
	require.NoError(t, repo.MarkDismissed(ctx, userID, n.ID))
	got, err = repo.Get(ctx, n.ID)
	require.NoError(t, err)
	require.NotNil(t, got.DismissTime)
	assert.Equal(t, first, *got.DismissTime)
}

func TestNotificationRepository_ListByUser(t *testing.T) {
	repo := rdb.NewNotificationRepository(testDB)
	ctx := context.Background()

	t.Run("returns user's notifications most-recent-first", func(t *testing.T) {
		cleanDatabase(t)
		userID := seedUser(t, "notif-list-user", "notif-list@example.com", "ext-notif-list-01")
		otherID := seedUser(t, "notif-list-other", "notif-list-other@example.com", "ext-notif-list-other-01")

		// Two separate Create round-trips run in distinct transactions, so each
		// gets a distinct queued_at (now() at microsecond resolution; round-trip
		// latency far exceeds 1µs), making the most-recent-first ordering
		// deterministic without an artificial delay.
		first := newTestNotification(userID, entity.NotificationTypeNewConcerts)
		require.NoError(t, repo.Create(ctx, first))
		second := newTestNotification(userID, entity.NotificationTypeSalesReminder)
		require.NoError(t, repo.Create(ctx, second))
		// A different user's notification must not appear.
		require.NoError(t, repo.Create(ctx, newTestNotification(otherID, entity.NotificationTypeNewConcerts)))

		got, err := repo.ListByUser(ctx, userID, 0)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, second.ID, got[0].ID, "newest first")
		assert.Equal(t, first.ID, got[1].ID)
	})

	t.Run("limit caps results", func(t *testing.T) {
		cleanDatabase(t)
		userID := seedUser(t, "notif-limit-user", "notif-limit@example.com", "ext-notif-limit-01")
		for range 3 {
			require.NoError(t, repo.Create(ctx, newTestNotification(userID, entity.NotificationTypeNewConcerts)))
		}
		got, err := repo.ListByUser(ctx, userID, 2)
		require.NoError(t, err)
		assert.Len(t, got, 2)
	})

	t.Run("empty for user with no notifications", func(t *testing.T) {
		cleanDatabase(t)
		userID := seedUser(t, "notif-empty-user", "notif-empty@example.com", "ext-notif-empty-01")
		got, err := repo.ListByUser(ctx, userID, 0)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}
