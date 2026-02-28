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

func newTestUser(externalID, email, name string) *entity.NewUser {
	return &entity.NewUser{
		ExternalID:        externalID,
		Email:             email,
		Name:              name,
		PreferredLanguage: "ja",
		Country:           "JP",
		TimeZone:          "Asia/Tokyo",
	}
}

func newTestUserWithHome(externalID, email, name string) *entity.NewUser {
	u := newTestUser(externalID, email, name)
	u.Home = &entity.Home{
		CountryCode: "JP",
		Level1:      "JP-13",
	}
	return u
}

func TestUserRepository_Create(t *testing.T) {
	repo := rdb.NewUserRepository(testDB)
	ctx := context.Background()

	type args struct {
		params *entity.NewUser
	}

	tests := []struct {
		name    string
		setup   func()
		args    args
		wantErr error
	}{
		{
			name:  "creates a user successfully",
			setup: cleanDatabase,
			args: args{
				params: newTestUser("ext-001", "alice@example.com", "Alice"),
			},
			wantErr: nil,
		},
		{
			name:  "creates a user with home",
			setup: cleanDatabase,
			args: args{
				params: newTestUserWithHome("ext-home-1", "withhome@example.com", "WithHome"),
			},
			wantErr: nil,
		},
		{
			name:  "nil params returns error",
			setup: cleanDatabase,
			args: args{
				params: nil,
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "duplicate email returns already exists",
			setup: func() {
				cleanDatabase()
				_, err := repo.Create(ctx, newTestUser("ext-dup-1", "dup@example.com", "First"))
				require.NoError(t, err)
			},
			args: args{
				params: newTestUser("ext-dup-2", "dup@example.com", "Second"),
			},
			wantErr: apperr.ErrAlreadyExists,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()

			got, err := repo.Create(ctx, tt.args.params)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.NotEmpty(t, got.ID)
			assert.Equal(t, tt.args.params.Email, got.Email)
			assert.Equal(t, tt.args.params.Name, got.Name)
			assert.Equal(t, tt.args.params.ExternalID, got.ExternalID)

			if tt.args.params.Home != nil {
				require.NotNil(t, got.Home)
				assert.NotEmpty(t, got.Home.ID)
				assert.Equal(t, tt.args.params.Home.CountryCode, got.Home.CountryCode)
				assert.Equal(t, tt.args.params.Home.Level1, got.Home.Level1)
			} else {
				assert.Nil(t, got.Home)
			}
		})
	}
}

func TestUserRepository_Get(t *testing.T) {
	repo := rdb.NewUserRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name     string
		setup    func() string // returns user ID
		wantHome bool
		wantErr  error
	}{
		{
			name: "retrieves existing user",
			setup: func() string {
				cleanDatabase()
				user, err := repo.Create(ctx, newTestUser("ext-get-1", "get@example.com", "GetUser"))
				require.NoError(t, err)
				return user.ID
			},
			wantErr: nil,
		},
		{
			name: "retrieves user with home",
			setup: func() string {
				cleanDatabase()
				user, err := repo.Create(ctx, newTestUserWithHome("ext-get-home", "gethome@example.com", "GetHome"))
				require.NoError(t, err)
				return user.ID
			},
			wantHome: true,
			wantErr:  nil,
		},
		{
			name: "empty ID returns error",
			setup: func() string {
				return ""
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "non-existent user returns not found",
			setup: func() string {
				cleanDatabase()
				return "00000000-0000-0000-0000-000000000000"
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := tt.setup()

			got, err := repo.Get(ctx, id)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, id, got.ID)

			if tt.wantHome {
				require.NotNil(t, got.Home)
				assert.Equal(t, "JP", got.Home.CountryCode)
				assert.Equal(t, "JP-13", got.Home.Level1)
			}
		})
	}
}

func TestUserRepository_GetByExternalID(t *testing.T) {
	repo := rdb.NewUserRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name       string
		setup      func() string // returns external ID
		externalID func() string
		wantErr    error
	}{
		{
			name: "retrieves user by external ID",
			setup: func() string {
				cleanDatabase()
				_, err := repo.Create(ctx, newTestUser("ext-by-ext-1", "byext@example.com", "ByExt"))
				require.NoError(t, err)
				return "ext-by-ext-1"
			},
			wantErr: nil,
		},
		{
			name: "empty external ID returns error",
			setup: func() string {
				return ""
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "non-existent external ID returns not found",
			setup: func() string {
				cleanDatabase()
				return "non-existent-ext-id"
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extID := tt.setup()

			got, err := repo.GetByExternalID(ctx, extID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, extID, got.ExternalID)
		})
	}
}

func TestUserRepository_GetByEmail(t *testing.T) {
	repo := rdb.NewUserRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func() string // returns email
		wantErr error
	}{
		{
			name: "retrieves user by email",
			setup: func() string {
				cleanDatabase()
				_, err := repo.Create(ctx, newTestUser("ext-email-1", "byemail@example.com", "ByEmail"))
				require.NoError(t, err)
				return "byemail@example.com"
			},
			wantErr: nil,
		},
		{
			name: "empty email returns error",
			setup: func() string {
				return ""
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "non-existent email returns not found",
			setup: func() string {
				cleanDatabase()
				return "no-such-email@example.com"
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			email := tt.setup()

			got, err := repo.GetByEmail(ctx, email)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, email, got.Email)
		})
	}
}

func TestUserRepository_Update(t *testing.T) {
	repo := rdb.NewUserRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func() string // returns user ID
		params  *entity.NewUser
		wantErr error
	}{
		{
			name: "updates user successfully",
			setup: func() string {
				cleanDatabase()
				user, err := repo.Create(ctx, newTestUser("ext-upd-1", "update@example.com", "BeforeUpdate"))
				require.NoError(t, err)
				return user.ID
			},
			params:  newTestUser("ext-upd-1", "updated@example.com", "AfterUpdate"),
			wantErr: nil,
		},
		{
			name: "empty ID returns error",
			setup: func() string {
				return ""
			},
			params:  newTestUser("ext-upd-2", "any@example.com", "Any"),
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "nil params returns error",
			setup: func() string {
				cleanDatabase()
				user, err := repo.Create(ctx, newTestUser("ext-upd-3", "nilparams@example.com", "NilParams"))
				require.NoError(t, err)
				return user.ID
			},
			params:  nil,
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "non-existent user returns not found",
			setup: func() string {
				cleanDatabase()
				return "00000000-0000-0000-0000-000000000000"
			},
			params:  newTestUser("ext-upd-4", "ghost@example.com", "Ghost"),
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := tt.setup()

			got, err := repo.Update(ctx, id, tt.params)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, id, got.ID)
			assert.Equal(t, tt.params.Email, got.Email)
			assert.Equal(t, tt.params.Name, got.Name)
		})
	}
}

func TestUserRepository_List(t *testing.T) {
	repo := rdb.NewUserRepository(testDB)
	ctx := context.Background()

	type args struct {
		limit  int
		offset int
	}

	tests := []struct {
		name      string
		setup     func()
		args      args
		wantCount int
		wantErr   error
	}{
		{
			name: "lists all users",
			setup: func() {
				cleanDatabase()
				_, err := repo.Create(ctx, newTestUser("ext-list-1", "list1@example.com", "List1"))
				require.NoError(t, err)
				_, err = repo.Create(ctx, newTestUser("ext-list-2", "list2@example.com", "List2"))
				require.NoError(t, err)
				_, err = repo.Create(ctx, newTestUser("ext-list-3", "list3@example.com", "List3"))
				require.NoError(t, err)
			},
			args:      args{limit: 10, offset: 0},
			wantCount: 3,
			wantErr:   nil,
		},
		{
			name: "respects limit",
			setup: func() {
				cleanDatabase()
				_, err := repo.Create(ctx, newTestUser("ext-lim-1", "lim1@example.com", "Lim1"))
				require.NoError(t, err)
				_, err = repo.Create(ctx, newTestUser("ext-lim-2", "lim2@example.com", "Lim2"))
				require.NoError(t, err)
			},
			args:      args{limit: 1, offset: 0},
			wantCount: 1,
			wantErr:   nil,
		},
		{
			name: "respects offset",
			setup: func() {
				cleanDatabase()
				_, err := repo.Create(ctx, newTestUser("ext-off-1", "off1@example.com", "Off1"))
				require.NoError(t, err)
				_, err = repo.Create(ctx, newTestUser("ext-off-2", "off2@example.com", "Off2"))
				require.NoError(t, err)
			},
			args:      args{limit: 10, offset: 1},
			wantCount: 1,
			wantErr:   nil,
		},
		{
			name:      "empty table returns empty slice",
			setup:     cleanDatabase,
			args:      args{limit: 10, offset: 0},
			wantCount: 0,
			wantErr:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()

			got, err := repo.List(ctx, tt.args.limit, tt.args.offset)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Len(t, got, tt.wantCount)
		})
	}
}

func TestUserRepository_Delete(t *testing.T) {
	repo := rdb.NewUserRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func() string // returns user ID
		wantErr error
	}{
		{
			name: "deletes existing user",
			setup: func() string {
				cleanDatabase()
				user, err := repo.Create(ctx, newTestUser("ext-del-1", "del@example.com", "Delete"))
				require.NoError(t, err)
				return user.ID
			},
			wantErr: nil,
		},
		{
			name: "empty ID returns error",
			setup: func() string {
				return ""
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "non-existent user returns not found",
			setup: func() string {
				cleanDatabase()
				return "00000000-0000-0000-0000-000000000000"
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := tt.setup()

			err := repo.Delete(ctx, id)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)

			// Verify user is actually deleted
			_, err = repo.Get(ctx, id)
			assert.ErrorIs(t, err, apperr.ErrNotFound)
		})
	}
}

func TestUserRepository_UpdateSafeAddress(t *testing.T) {
	repo := rdb.NewUserRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name        string
		setup       func() string // returns user ID
		safeAddress string
		wantErr     error
	}{
		{
			name: "updates safe address",
			setup: func() string {
				cleanDatabase()
				user, err := repo.Create(ctx, newTestUser("ext-safe-1", "safe@example.com", "SafeAddr"))
				require.NoError(t, err)
				return user.ID
			},
			safeAddress: "0x1234567890abcdef1234567890abcdef12345678",
			wantErr:     nil,
		},
		{
			name: "empty ID returns error",
			setup: func() string {
				return ""
			},
			safeAddress: "0xabc",
			wantErr:     apperr.ErrInvalidArgument,
		},
		{
			name: "non-existent user returns not found",
			setup: func() string {
				cleanDatabase()
				return "00000000-0000-0000-0000-000000000000"
			},
			safeAddress: "0xabc",
			wantErr:     apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := tt.setup()

			err := repo.UpdateSafeAddress(ctx, id, tt.safeAddress)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)

			// Verify safe address was updated
			user, err := repo.Get(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, tt.safeAddress, user.SafeAddress)
		})
	}
}

func TestUserRepository_UpdateHome(t *testing.T) {
	repo := rdb.NewUserRepository(testDB)
	ctx := context.Background()

	t.Run("creates home for user without one", func(t *testing.T) {
		cleanDatabase()
		user, err := repo.Create(ctx, newTestUser("ext-uh-1", "uh1@example.com", "UH1"))
		require.NoError(t, err)
		assert.Nil(t, user.Home)

		home := &entity.Home{
			CountryCode: "JP",
			Level1:      "JP-13",
		}

		updated, err := repo.UpdateHome(ctx, user.ID, home)

		require.NoError(t, err)
		require.NotNil(t, updated.Home)
		assert.NotEmpty(t, updated.Home.ID)
		assert.Equal(t, "JP", updated.Home.CountryCode)
		assert.Equal(t, "JP-13", updated.Home.Level1)
		assert.Nil(t, updated.Home.Level2)
	})

	t.Run("updates existing home", func(t *testing.T) {
		cleanDatabase()
		user, err := repo.Create(ctx, newTestUserWithHome("ext-uh-2", "uh2@example.com", "UH2"))
		require.NoError(t, err)
		require.NotNil(t, user.Home)
		assert.Equal(t, "JP-13", user.Home.Level1)

		home := &entity.Home{
			CountryCode: "JP",
			Level1:      "JP-40",
		}

		updated, err := repo.UpdateHome(ctx, user.ID, home)

		require.NoError(t, err)
		require.NotNil(t, updated.Home)
		assert.Equal(t, "JP", updated.Home.CountryCode)
		assert.Equal(t, "JP-40", updated.Home.Level1)
	})

	t.Run("empty ID returns error", func(t *testing.T) {
		home := &entity.Home{
			CountryCode: "JP",
			Level1:      "JP-13",
		}

		_, err := repo.UpdateHome(ctx, "", home)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}
