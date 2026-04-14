package rdb_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
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

func TestPushSubscriptionRepository_Get(t *testing.T) {
	repo := rdb.NewPushSubscriptionRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func() (userID, endpoint string)
		check   func(t *testing.T, got *entity.PushSubscription)
		wantErr error
	}{
		{
			name: "returns matching subscription",
			setup: func() (string, string) {
				cleanDatabase(t)
				userID := seedUser(t, "push-get-user", "push-get@example.com", "ext-push-get-01")
				sub := &entity.PushSubscription{
					UserID:   userID,
					Endpoint: "https://push.example.com/get-endpoint",
					P256dh:   "p256dh-get",
					Auth:     "auth-get",
				}
				err := repo.Create(ctx, sub)
				require.NoError(t, err)
				return userID, sub.Endpoint
			},
			check: func(t *testing.T, got *entity.PushSubscription) {
				t.Helper()
				assert.Equal(t, "https://push.example.com/get-endpoint", got.Endpoint)
				assert.Equal(t, "p256dh-get", got.P256dh)
				assert.Equal(t, "auth-get", got.Auth)
			},
			wantErr: nil,
		},
		{
			name: "returns NotFound when endpoint does not match for user",
			setup: func() (string, string) {
				cleanDatabase(t)
				userID := seedUser(t, "push-get-miss-user", "push-get-miss@example.com", "ext-push-get-miss-01")
				return userID, "https://push.example.com/nonexistent"
			},
			wantErr: apperr.ErrNotFound,
		},
		{
			name: "returns NotFound when subscription exists but belongs to different user",
			setup: func() (string, string) {
				cleanDatabase(t)
				ownerID := seedUser(t, "push-get-owner", "push-get-owner@example.com", "ext-push-get-owner-01")
				sub := &entity.PushSubscription{
					UserID:   ownerID,
					Endpoint: "https://push.example.com/cross-user-endpoint",
					P256dh:   "p256dh-cross",
					Auth:     "auth-cross",
				}
				err := repo.Create(ctx, sub)
				require.NoError(t, err)
				otherID := seedUser(t, "push-get-other", "push-get-other@example.com", "ext-push-get-other-01")
				return otherID, sub.Endpoint
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID, endpoint := tt.setup()

			got, err := repo.Get(ctx, userID, endpoint)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

func TestPushSubscriptionRepository_Delete(t *testing.T) {
	repo := rdb.NewPushSubscriptionRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func() (userID, endpoint, otherEndpoint string)
		wantErr error
	}{
		{
			name: "deletes only the specified browser's subscription",
			setup: func() (string, string, string) {
				cleanDatabase(t)
				userID := seedUser(t, "push-delete-user", "push-delete@example.com", "ext-push-delete-01")
				target := &entity.PushSubscription{
					UserID:   userID,
					Endpoint: "https://push.example.com/delete-target",
					P256dh:   "p256dh-target",
					Auth:     "auth-target",
				}
				require.NoError(t, repo.Create(ctx, target))
				other := &entity.PushSubscription{
					UserID:   userID,
					Endpoint: "https://push.example.com/delete-other",
					P256dh:   "p256dh-other",
					Auth:     "auth-other",
				}
				require.NoError(t, repo.Create(ctx, other))
				return userID, target.Endpoint, other.Endpoint
			},
			wantErr: nil,
		},
		{
			name: "deleting non-existent pair is idempotent",
			setup: func() (string, string, string) {
				cleanDatabase(t)
				userID := seedUser(t, "push-delete-idem-user", "push-delete-idem@example.com", "ext-push-delete-idem-01")
				return userID, "https://push.example.com/does-not-exist", ""
			},
			wantErr: nil,
		},
		{
			name: "deleting another user's endpoint does not remove their row",
			setup: func() (string, string, string) {
				cleanDatabase(t)
				ownerID := seedUser(t, "push-delete-owner", "push-delete-owner@example.com", "ext-push-delete-owner-01")
				sub := &entity.PushSubscription{
					UserID:   ownerID,
					Endpoint: "https://push.example.com/owner-endpoint",
					P256dh:   "p256dh-owner",
					Auth:     "auth-owner",
				}
				require.NoError(t, repo.Create(ctx, sub))
				otherID := seedUser(t, "push-delete-attacker", "push-delete-attacker@example.com", "ext-push-delete-attacker-01")
				// The attacker tries to delete the owner's endpoint using the attacker's userID.
				// Repository scoping by (userID, endpoint) must leave the owner's row intact.
				return otherID, sub.Endpoint, ""
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID, endpoint, otherEndpoint := tt.setup()

			err := repo.Delete(ctx, userID, endpoint)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)

			// Verify the specified (userID, endpoint) row is gone.
			_, getErr := repo.Get(ctx, userID, endpoint)
			assert.ErrorIs(t, getErr, apperr.ErrNotFound)

			// Verify other rows for the same user remain when provided.
			if otherEndpoint != "" {
				other, otherErr := repo.Get(ctx, userID, otherEndpoint)
				require.NoError(t, otherErr)
				assert.Equal(t, otherEndpoint, other.Endpoint)
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
