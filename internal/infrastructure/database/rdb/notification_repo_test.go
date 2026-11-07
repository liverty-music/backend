package rdb_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestNotification returns a new_concerts Notification about an artist and
// one concert. The subject's ids are not foreign keys, so nothing else is
// seeded.
func newTestNotification(userID string) *entity.Notification {
	return &entity.Notification{
		UserID:  userID,
		Type:    entity.NotificationTypeNewConcerts,
		Subject: entity.NotificationSubject{ArtistID: entity.NewID(), EventIDs: []string{entity.NewID()}},
	}
}

// notificationCount counts the stored Notifications of the fan.
func notificationCount(t *testing.T, userID string) int {
	t.Helper()
	var n int
	require.NoError(t, testDB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM notifications WHERE user_id = $1`, userID).Scan(&n))
	return n
}

func TestNotificationRepository_Create(t *testing.T) {
	repo := rdb.NewNotificationRepository(testDB)
	ctx := context.Background()

	// @spec components/entity/notification/create "Notification recorded"
	t.Run("Notification recorded", func(t *testing.T) {
		cleanDatabase(t)
		userID := seedUser(t, "notif-create-user", "notif-create@example.com", "ext-notif-create-01")
		n := newTestNotification(userID)

		require.NoError(t, repo.Create(ctx, n))

		assert.NotEmpty(t, n.ID)
		assert.False(t, n.CreateTime.IsZero())
		got, err := repo.Get(ctx, n.ID)
		require.NoError(t, err)
		assert.Equal(t, userID, got.UserID)
		assert.Nil(t, got.ReadTime, "a new Notification is unread")
	})

	// @spec components/entity/notification/create "Two notifications"
	t.Run("Two notifications", func(t *testing.T) {
		cleanDatabase(t)
		userID := seedUser(t, "notif-two-user", "notif-two@example.com", "ext-notif-two-01")
		first := newTestNotification(userID)
		second := &entity.Notification{UserID: userID, Type: first.Type, Subject: first.Subject}

		require.NoError(t, repo.Create(ctx, first))
		require.NoError(t, repo.Create(ctx, second))

		assert.NotEqual(t, first.ID, second.ID)
		assert.Equal(t, 2, notificationCount(t, userID))
	})

	// @spec components/entity/notification/create "Unknown fan"
	t.Run("Unknown fan", func(t *testing.T) {
		cleanDatabase(t)
		unknown := entity.NewID()

		err := repo.Create(ctx, newTestNotification(unknown))

		assertAppErrCode(t, err, codes.FailedPrecondition)
		assert.Zero(t, notificationCount(t, unknown))
	})

	// @spec components/entity/notification/create "Subject missing a record"
	t.Run("Subject missing a record", func(t *testing.T) {
		cleanDatabase(t)
		userID := seedUser(t, "notif-invalid-user", "notif-invalid@example.com", "ext-notif-invalid-01")

		err := repo.Create(ctx, &entity.Notification{UserID: userID, Type: entity.NotificationTypeLotteryLoss})

		assertAppErrCode(t, err, codes.InvalidArgument)
		assert.Zero(t, notificationCount(t, userID))
	})
}

func TestNotificationRepository_Get(t *testing.T) {
	repo := rdb.NewNotificationRepository(testDB)
	ctx := context.Background()

	// @spec components/entity/notification/get "Existing notification"
	t.Run("Existing notification", func(t *testing.T) {
		cleanDatabase(t)
		userID := seedUser(t, "notif-get-user", "notif-get@example.com", "ext-notif-get-01")
		subject := entity.NotificationSubject{
			TicketSaleID: entity.NewID(),
			Stage:        entity.ReminderStageApplyClose24H,
			EventID:      entity.NewID(),
		}
		n := &entity.Notification{UserID: userID, Type: entity.NotificationTypeSalesReminder, Subject: subject}
		require.NoError(t, repo.Create(ctx, n))

		unread, err := repo.Get(ctx, n.ID)
		require.NoError(t, err)
		assert.Equal(t, n.ID, unread.ID)
		assert.Equal(t, userID, unread.UserID)
		assert.Equal(t, entity.NotificationTypeSalesReminder, unread.Type)
		assert.Equal(t, subject, unread.Subject)
		assert.True(t, n.CreateTime.Equal(unread.CreateTime), "got create time %s, want %s", unread.CreateTime, n.CreateTime)
		assert.Nil(t, unread.ReadTime)

		require.NoError(t, repo.MarkRead(ctx, userID, n.ID))
		read, err := repo.Get(ctx, n.ID)
		require.NoError(t, err)
		assert.NotNil(t, read.ReadTime)
	})

	// @spec components/entity/notification/get "Unknown id"
	t.Run("Unknown id", func(t *testing.T) {
		cleanDatabase(t)

		_, err := repo.Get(ctx, entity.NewID())

		assertAppErrCode(t, err, codes.NotFound)
	})
}

func TestNotificationRepository_MarkRead(t *testing.T) {
	repo := rdb.NewNotificationRepository(testDB)
	ctx := context.Background()

	cleanDatabase(t)
	userID := seedUser(t, "notif-read-user", "notif-read@example.com", "ext-notif-read-01")
	otherID := seedUser(t, "notif-read-other", "notif-read-other@example.com", "ext-notif-read-other-01")
	n := newTestNotification(userID)
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
	otherNotif := newTestNotification(otherID)
	require.NoError(t, repo.Create(ctx, otherNotif))
	require.NoError(t, repo.MarkRead(ctx, userID, otherNotif.ID)) // userID != owner otherID
	got, err = repo.Get(ctx, otherNotif.ID)
	require.NoError(t, err)
	assert.Nil(t, got.ReadTime)
}

func TestNotificationRepository_ListByUser(t *testing.T) {
	repo := rdb.NewNotificationRepository(testDB)
	ctx := context.Background()

	// @spec components/entity/notification/list-by-user "More than the limit"
	t.Run("More than the limit", func(t *testing.T) {
		cleanDatabase(t)
		userID := seedUser(t, "notif-limit-user", "notif-limit@example.com", "ext-notif-limit-01")
		// Each Create runs in its own transaction, so each gets a later created_at.
		newestFirst := make([]string, 60)
		for i := range newestFirst {
			n := newTestNotification(userID)
			require.NoError(t, repo.Create(ctx, n))
			newestFirst[len(newestFirst)-1-i] = n.ID
		}

		got, err := repo.ListByUser(ctx, userID, 50)

		require.NoError(t, err)
		ids := make([]string, len(got))
		for i, n := range got {
			ids[i] = n.ID
		}
		assert.Equal(t, newestFirst[:50], ids)
	})

	// @spec components/entity/notification/list-by-user "Another fan's notifications"
	t.Run("Another fan's notifications", func(t *testing.T) {
		cleanDatabase(t)
		userID := seedUser(t, "notif-list-user", "notif-list@example.com", "ext-notif-list-01")
		otherID := seedUser(t, "notif-list-other", "notif-list-other@example.com", "ext-notif-list-other-01")
		own := newTestNotification(userID)
		require.NoError(t, repo.Create(ctx, own))
		require.NoError(t, repo.Create(ctx, newTestNotification(otherID)))

		got, err := repo.ListByUser(ctx, userID, 50)

		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, own.ID, got[0].ID)
		assert.Equal(t, own.Subject, got[0].Subject, "listed with all its attributes")
		assert.True(t, own.CreateTime.Equal(got[0].CreateTime))
	})

	// @spec components/entity/notification/list-by-user "No notification"
	t.Run("No notification", func(t *testing.T) {
		cleanDatabase(t)
		userID := seedUser(t, "notif-empty-user", "notif-empty@example.com", "ext-notif-empty-01")

		got, err := repo.ListByUser(ctx, userID, 50)

		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})

	// @spec components/entity/notification/list-by-user "Zero limit"
	t.Run("Zero limit", func(t *testing.T) {
		_, err := repo.ListByUser(ctx, entity.NewID(), 0)

		assertAppErrCode(t, err, codes.InvalidArgument)
	})
}
