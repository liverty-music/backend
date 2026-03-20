package rdb_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPushSubscriptionRepository_Create(t *testing.T) {
	repo := rdb.NewPushSubscriptionRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func() *entity.PushSubscription
		check   func(t *testing.T, userID string)
		wantErr error
	}{
		{
			name: "creates new subscription",
			setup: func() *entity.PushSubscription {
				cleanDatabase(t)
				userID := seedUser(t, "push-create-user", "push-create@example.com", "ext-push-create-01")
				return &entity.PushSubscription{
					UserID:   userID,
					Endpoint: "https://push.example.com/create-new",
					P256dh:   "p256dh-initial",
					Auth:     "auth-initial",
				}
			},
			wantErr: nil,
		},
		{
			name: "upsert updates existing subscription on same endpoint",
			setup: func() *entity.PushSubscription {
				cleanDatabase(t)
				userID := seedUser(t, "push-upsert-user", "push-upsert@example.com", "ext-push-upsert-01")
				first := &entity.PushSubscription{
					UserID:   userID,
					Endpoint: "https://push.example.com/upsert-endpoint",
					P256dh:   "p256dh-original",
					Auth:     "auth-original",
				}
				err := repo.Create(ctx, first)
				require.NoError(t, err)
				return &entity.PushSubscription{
					UserID:   userID,
					Endpoint: "https://push.example.com/upsert-endpoint",
					P256dh:   "p256dh-updated",
					Auth:     "auth-updated",
				}
			},
			check: func(t *testing.T, userID string) {
				t.Helper()
				subs, err := repo.ListByUserIDs(ctx, []string{userID})
				require.NoError(t, err)
				require.Len(t, subs, 1)
				assert.Equal(t, "p256dh-updated", subs[0].P256dh)
				assert.Equal(t, "auth-updated", subs[0].Auth)
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := tt.setup()

			err := repo.Create(ctx, sub)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			if tt.check != nil {
				tt.check(t, sub.UserID)
			}
		})
	}
}

func TestPushSubscriptionRepository_DeleteByEndpoint(t *testing.T) {
	repo := rdb.NewPushSubscriptionRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func() (endpoint string, userID string)
		wantErr error
	}{
		{
			name: "deletes existing subscription",
			setup: func() (string, string) {
				cleanDatabase(t)
				userID := seedUser(t, "push-delete-ep-user", "push-delete-ep@example.com", "ext-push-delete-ep-01")
				sub := &entity.PushSubscription{
					UserID:   userID,
					Endpoint: "https://push.example.com/delete-by-endpoint",
					P256dh:   "p256dh-del",
					Auth:     "auth-del",
				}
				err := repo.Create(ctx, sub)
				require.NoError(t, err)
				return sub.Endpoint, userID
			},
			wantErr: nil,
		},
		{
			name: "deleting non-existent endpoint is idempotent",
			setup: func() (string, string) {
				cleanDatabase(t)
				return "https://push.example.com/nonexistent-endpoint", ""
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoint, userID := tt.setup()

			err := repo.DeleteByEndpoint(ctx, endpoint)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			if userID != "" {
				subs, listErr := repo.ListByUserIDs(ctx, []string{userID})
				require.NoError(t, listErr)
				assert.Empty(t, subs)
			}
		})
	}
}

func TestPushSubscriptionRepository_ListByUserIDs(t *testing.T) {
	repo := rdb.NewPushSubscriptionRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func() []string
		want    int
		wantErr error
	}{
		{
			name: "empty input returns empty slice",
			setup: func() []string {
				cleanDatabase(t)
				return nil
			},
			want:    0,
			wantErr: nil,
		},
		{
			name: "returns subscriptions for matching users",
			setup: func() []string {
				cleanDatabase(t)
				userID1 := seedUser(t, "push-list-user1", "push-list-1@example.com", "ext-push-list-01")
				userID2 := seedUser(t, "push-list-user2", "push-list-2@example.com", "ext-push-list-02")
				err := repo.Create(ctx, &entity.PushSubscription{
					UserID:   userID1,
					Endpoint: "https://push.example.com/list-user1",
					P256dh:   "p256dh-u1",
					Auth:     "auth-u1",
				})
				require.NoError(t, err)
				err = repo.Create(ctx, &entity.PushSubscription{
					UserID:   userID2,
					Endpoint: "https://push.example.com/list-user2",
					P256dh:   "p256dh-u2",
					Auth:     "auth-u2",
				})
				require.NoError(t, err)
				return []string{userID1, userID2}
			},
			want:    2,
			wantErr: nil,
		},
		{
			name: "returns empty for non-matching users",
			setup: func() []string {
				cleanDatabase(t)
				return []string{"00000000-0000-0000-0000-000000000001"}
			},
			want:    0,
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userIDs := tt.setup()

			got, err := repo.ListByUserIDs(ctx, userIDs)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Len(t, got, tt.want)
			if len(userIDs) == 0 {
				assert.Equal(t, []*entity.PushSubscription{}, got)
			}
		})
	}
}

func TestPushSubscriptionRepository_DeleteByUserID(t *testing.T) {
	repo := rdb.NewPushSubscriptionRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func() string
		wantErr error
	}{
		{
			name: "deletes all subscriptions for user",
			setup: func() string {
				cleanDatabase(t)
				userID := seedUser(t, "push-delete-uid-user", "push-delete-uid@example.com", "ext-push-delete-uid-01")
				err := repo.Create(ctx, &entity.PushSubscription{
					UserID:   userID,
					Endpoint: "https://push.example.com/delete-uid-ep1",
					P256dh:   "p256dh-d1",
					Auth:     "auth-d1",
				})
				require.NoError(t, err)
				err = repo.Create(ctx, &entity.PushSubscription{
					UserID:   userID,
					Endpoint: "https://push.example.com/delete-uid-ep2",
					P256dh:   "p256dh-d2",
					Auth:     "auth-d2",
				})
				require.NoError(t, err)
				return userID
			},
			wantErr: nil,
		},
		{
			name: "deleting for user with no subscriptions is idempotent",
			setup: func() string {
				cleanDatabase(t)
				return seedUser(t, "push-delete-empty-user", "push-delete-empty@example.com", "ext-push-delete-empty-01")
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID := tt.setup()

			err := repo.DeleteByUserID(ctx, userID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			subs, listErr := repo.ListByUserIDs(ctx, []string{userID})
			require.NoError(t, listErr)
			assert.Empty(t, subs)
		})
	}
}
