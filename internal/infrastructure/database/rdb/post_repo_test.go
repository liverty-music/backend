package rdb_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/pannpers/go-backend-scaffold/internal/entity"
	"github.com/pannpers/go-backend-scaffold/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-backend-scaffold/pkg/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostRepository_Create(t *testing.T) {
	type args struct {
		params *entity.NewPost
	}
	// Create a test user first
	ctx := context.Background()
	testUser := &rdb.User{
		ID:    "550e8400-e29b-41d4-a716-446655440000",
		Name:  "Test User",
		Email: "test@example.com",
	}
	_, err := testDB.NewInsert().Model(testUser).Exec(ctx)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = testDB.NewDelete().Model((*rdb.User)(nil)).Where("id = ?", testUser.ID).Exec(ctx)
	})

	tests := []struct {
		name    string
		args    args
		want    *entity.Post
		wantErr error
	}{
		{
			name: "create post successfully",
			args: args{
				params: &entity.NewPost{
					Title:  "Test Post",
					UserID: testUser.ID,
				},
			},
			want: &entity.Post{
				Title:  "Test Post",
				UserID: testUser.ID,
			},
			wantErr: nil,
		},
		{
			name: "return error when params is nil",
			args: args{
				params: nil,
			},
			want:    nil,
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "return error when user does not exist",
			args: args{
				params: &entity.NewPost{
					Title:  "Test Post",
					UserID: "99999999-9999-9999-9999-999999999999", // Non-existent user ID
				},
			},
			want:    nil,
			wantErr: apperr.ErrFailedPrecondition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := rdb.NewPostRepository(testDB).Create(ctx, tt.args.params)

			t.Cleanup(func() {
				if got != nil && got.ID != "" {
					_, _ = testDB.NewDelete().Model((*rdb.Post)(nil)).Where("id = ?", got.ID).Exec(ctx)
				}
			})

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}

			require.NoError(t, err)

			_, err = uuid.Parse(got.ID)
			require.NoError(t, err)
			assert.Equal(t, tt.want.Title, got.Title)
			assert.Equal(t, tt.want.UserID, got.UserID)
			assert.NotZero(t, got.CreatedAt)
			assert.NotZero(t, got.UpdatedAt)
		})
	}
}

func TestPostRepository_Get(t *testing.T) {
	t.Parallel()
	type args struct {
		id string
	}

	tests := []struct {
		name     string
		args     args
		fixtures []any
		want     *entity.Post
		wantErr  error
	}{
		{
			name: "return post when valid ID is provided",
			args: args{
				id: "239e4567-e89b-12d3-a456-426614174000",
			},
			fixtures: []any{
				&rdb.User{
					ID:    "550e8400-e29b-41d4-a716-446655440000",
					Name:  "Test User Get",
					Email: "testget@example.com",
				},
				&rdb.Post{
					ID:     "239e4567-e89b-12d3-a456-426614174000",
					Title:  "Test Post Get",
					UserID: "550e8400-e29b-41d4-a716-446655440000",
				},
			},
			want: &entity.Post{
				ID:     "239e4567-e89b-12d3-a456-426614174000",
				Title:  "Test Post Get",
				UserID: "550e8400-e29b-41d4-a716-446655440000",
			},
			wantErr: nil,
		},
		{
			name: "return error when post ID is empty",
			args: args{
				id: "",
			},
			want:    nil,
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "return error when malformed UUID",
			args: args{
				id: "not-a-uuid",
			},
			want:    nil,
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "return error when standard UUID format post does not exist",
			args: args{
				id: "123e4567-e89b-12d3-a456-426614174000",
			},
			want:    nil,
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()

			for _, fixture := range tt.fixtures {
				_, err := testDB.NewInsert().Model(fixture).Exec(ctx)
				require.NoError(t, err)
			}

			// Clean up test data after test
			t.Cleanup(func() {
				for _, fixture := range tt.fixtures {
					switch v := fixture.(type) {
					case *rdb.Post:
						_, _ = testDB.NewDelete().Model((*rdb.Post)(nil)).Where("id = ?", v.ID).Exec(ctx)
					case *rdb.User:
						_, _ = testDB.NewDelete().Model((*rdb.User)(nil)).Where("id = ?", v.ID).Exec(ctx)
					}
				}
			})

			// Execute the method under test
			got, err := rdb.NewPostRepository(testDB).Get(ctx, tt.args.id)

			// Assert error
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}

			assert.NoError(t, err)

			// Assert post fields
			if tt.want != nil {
				assert.Equal(t, tt.want.ID, got.ID)
				assert.Equal(t, tt.want.Title, got.Title)
				assert.Equal(t, tt.want.UserID, got.UserID)
				assert.False(t, got.CreatedAt.IsZero())
				assert.False(t, got.UpdatedAt.IsZero())
			}
		})
	}
}

func TestPostRepository_Get_ContextCancellation(t *testing.T) {
	t.Parallel()

	// Create a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	postRepo := rdb.NewPostRepository(testDB)

	got, err := postRepo.Get(ctx, "some-id")

	assert.Error(t, err)
	assert.Nil(t, got)
	assert.True(t, errors.Is(err, context.Canceled) || errors.Is(err, sql.ErrNoRows))
}
