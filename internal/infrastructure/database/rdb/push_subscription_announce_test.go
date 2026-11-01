package rdb_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

func TestPushSubscriptionRepository_Announces(t *testing.T) {
	ctx := context.Background()
	repo := rdb.NewPushSubscriptionRepository(testDB, testOutbox)

	newFan := func(t *testing.T) string {
		t.Helper()
		tag := uuid.NewV7().String()
		return seedUser(t, "push "+tag, tag+"@example.test", "ext-"+tag)
	}
	newEndpoint := func() string { return "https://web.push.apple.com/sub/" + uuid.NewV7().String() }
	subscribed := func(t *testing.T, subject string, mark int64, id string) []*entityv1.PushSubscription {
		t.Helper()
		return announcedSince(t, mark, subject, func() *entityv1.PushSubscription { return &entityv1.PushSubscription{} },
			func(p *entityv1.PushSubscription) bool { return p.GetId().GetValue() == id })
	}
	stored := func(t *testing.T, endpoint string) (userID string, n int) {
		t.Helper()
		require.NoError(t, testDB.Pool.QueryRow(ctx,
			`SELECT COALESCE(MAX(user_id::text), ''), count(*) FROM push_subscriptions WHERE endpoint = $1`, endpoint).Scan(&userID, &n))
		return userID, n
	}

	t.Run("new browser announced", func(t *testing.T) {
		// @spec components/entity/push-subscription/create "New browser"
		// @spec components/entity/push-subscription/create "New browser announced"
		// @spec components/infrastructure/backend/process/event-delivery "New push subscription announced"
		fan, endpoint := newFan(t), newEndpoint()
		mark := outboxMark(t)
		sub := &entity.PushSubscription{UserID: fan, Endpoint: endpoint, P256dh: "k1", Auth: "a1"}

		require.NoError(t, repo.Create(ctx, sub))

		require.NotEmpty(t, sub.ID)
		got := subscribed(t, entity.SubjectNotificationSubscribed, mark, sub.ID)
		require.Len(t, got, 1)
		assert.Equal(t, fan, got[0].GetUserId().GetValue())
		assert.Equal(t, entityv1.PushDeviceFamily_PUSH_DEVICE_FAMILY_APPLE, got[0].GetDeviceFamily())
		assert.Nil(t, got[0].GetEndpoint(), "no push address")
		assert.Nil(t, got[0].GetKeys(), "no keys")
	})

	t.Run("same browser registers again", func(t *testing.T) {
		// @spec components/entity/push-subscription/create "Same browser registers again"
		fan, endpoint := newFan(t), newEndpoint()
		first := &entity.PushSubscription{UserID: fan, Endpoint: endpoint, P256dh: "k1", Auth: "a1"}
		require.NoError(t, repo.Create(ctx, first))

		again := &entity.PushSubscription{UserID: fan, Endpoint: endpoint, P256dh: "k2", Auth: "a2"}
		require.NoError(t, repo.Create(ctx, again))

		assert.Equal(t, first.ID, again.ID)
		got, err := repo.Get(ctx, fan, endpoint)
		require.NoError(t, err)
		assert.Equal(t, "k2", got.P256dh)
	})

	t.Run("same browser, same keys", func(t *testing.T) {
		// @spec components/entity/push-subscription/create "Same browser, same keys"
		fan, endpoint := newFan(t), newEndpoint()
		first := &entity.PushSubscription{UserID: fan, Endpoint: endpoint, P256dh: "k1", Auth: "a1"}
		require.NoError(t, repo.Create(ctx, first))
		mark := outboxMark(t)

		again := &entity.PushSubscription{UserID: fan, Endpoint: endpoint, P256dh: "k1", Auth: "a1"}
		require.NoError(t, repo.Create(ctx, again))

		assert.Equal(t, first.ID, again.ID)
		assert.Empty(t, subscribed(t, entity.SubjectNotificationSubscribed, mark, first.ID))
	})

	t.Run("browser moves to another fan", func(t *testing.T) {
		// @spec components/entity/push-subscription/create "Browser moves to another fan"
		fanA, fanB, endpoint := newFan(t), newFan(t), newEndpoint()
		require.NoError(t, repo.Create(ctx, &entity.PushSubscription{UserID: fanA, Endpoint: endpoint, P256dh: "k1", Auth: "a1"}))
		mark := outboxMark(t)

		moved := &entity.PushSubscription{UserID: fanB, Endpoint: endpoint, P256dh: "k1", Auth: "a1"}
		require.NoError(t, repo.Create(ctx, moved))

		owner, n := stored(t, endpoint)
		assert.Equal(t, fanB, owner)
		assert.Equal(t, 1, n)
		got := subscribed(t, entity.SubjectNotificationSubscribed, mark, moved.ID)
		require.Len(t, got, 1)
		assert.Equal(t, fanB, got[0].GetUserId().GetValue())
	})

	t.Run("create announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/push-subscription/create "Announcement cannot be recorded"
		fan, endpoint := newFan(t), newEndpoint()

		err := rdb.NewPushSubscriptionRepository(testDB, failingOutbox{}).Create(ctx, &entity.PushSubscription{UserID: fan, Endpoint: endpoint, P256dh: "k1", Auth: "a1"})

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		_, n := stored(t, endpoint)
		assert.Zero(t, n)
	})

	t.Run("registered browser removed", func(t *testing.T) {
		// @spec components/entity/push-subscription/delete "Registered browser removed"
		fan, endpoint := newFan(t), newEndpoint()
		sub := &entity.PushSubscription{UserID: fan, Endpoint: endpoint, P256dh: "k1", Auth: "a1"}
		require.NoError(t, repo.Create(ctx, sub))
		mark := outboxMark(t)

		require.NoError(t, repo.Delete(ctx, fan, endpoint))

		got := subscribed(t, entity.SubjectNotificationUnsubscribed, mark, sub.ID)
		require.Len(t, got, 1)
		assert.Nil(t, got[0].GetEndpoint())
		assert.Nil(t, got[0].GetKeys())
		_, n := stored(t, endpoint)
		assert.Zero(t, n)
	})

	t.Run("not registered", func(t *testing.T) {
		// @spec components/entity/push-subscription/delete "Not registered"
		fan := newFan(t)
		mark := outboxMark(t)

		require.NoError(t, repo.Delete(ctx, fan, newEndpoint()))
		assert.Empty(t, announcedSince(t, mark, entity.SubjectNotificationUnsubscribed, func() *entityv1.PushSubscription { return &entityv1.PushSubscription{} },
			func(p *entityv1.PushSubscription) bool { return p.GetUserId().GetValue() == fan }))
	})

	t.Run("delete announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/push-subscription/delete "Announcement cannot be recorded"
		fan, endpoint := newFan(t), newEndpoint()
		require.NoError(t, repo.Create(ctx, &entity.PushSubscription{UserID: fan, Endpoint: endpoint, P256dh: "k1", Auth: "a1"}))

		err := rdb.NewPushSubscriptionRepository(testDB, failingOutbox{}).Delete(ctx, fan, endpoint)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		_, n := stored(t, endpoint)
		assert.Equal(t, 1, n, "the PushSubscription remains")
	})

	t.Run("gone browser removed", func(t *testing.T) {
		// @spec components/entity/push-subscription/delete-gone "Gone browser removed"
		fan, endpoint, other := newFan(t), newEndpoint(), newEndpoint()
		require.NoError(t, repo.Create(ctx, &entity.PushSubscription{UserID: fan, Endpoint: endpoint, P256dh: "k1", Auth: "a1"}))
		require.NoError(t, repo.Create(ctx, &entity.PushSubscription{UserID: fan, Endpoint: other, P256dh: "k2", Auth: "a2"}))
		mark := outboxMark(t)

		require.NoError(t, repo.DeleteGone(ctx, fan, endpoint))

		_, n := stored(t, endpoint)
		assert.Zero(t, n)
		_, n = stored(t, other)
		assert.Equal(t, 1, n, "the fan's other browser keeps its subscription")
		assert.Empty(t, announcedSince(t, mark, entity.SubjectNotificationUnsubscribed, func() *entityv1.PushSubscription { return &entityv1.PushSubscription{} },
			func(p *entityv1.PushSubscription) bool { return p.GetUserId().GetValue() == fan }))
	})

	t.Run("gone browser already removed", func(t *testing.T) {
		// @spec components/entity/push-subscription/delete-gone "Already removed"
		fan, kept := newFan(t), newEndpoint()
		require.NoError(t, repo.Create(ctx, &entity.PushSubscription{UserID: fan, Endpoint: kept, P256dh: "k1", Auth: "a1"}))
		mark := outboxMark(t)

		require.NoError(t, repo.DeleteGone(ctx, fan, newEndpoint()))

		_, n := stored(t, kept)
		assert.Equal(t, 1, n, "the fan's registered browser is unchanged")
		assert.Empty(t, announcedSince(t, mark, entity.SubjectNotificationUnsubscribed, func() *entityv1.PushSubscription { return &entityv1.PushSubscription{} },
			func(p *entityv1.PushSubscription) bool { return p.GetUserId().GetValue() == fan }))
	})
}
