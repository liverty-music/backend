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

const googlePasswordManagerAAGUID = "ea9b8d66-4d01-1d21-3ce4-b6b48cb575d4"

func TestPasskeyRepository(t *testing.T) {
	users := rdb.NewUserRepository(testDB)
	repo := rdb.NewPasskeyRepository(testDB)
	ctx := context.Background()

	newFan := func(t *testing.T) *entity.User {
		t.Helper()
		cleanDatabase(t)
		user, err := users.Create(ctx, newTestUser("ext-passkey-1", "passkey@example.com", "Passkey Fan"))
		require.NoError(t, err)
		return user
	}
	oct1 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	oct20 := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)

	// @spec components/entity/passkey/create "New passkey"
	t.Run("Create stores a new passkey with its user, AAGUID and create time", func(t *testing.T) {
		fan := newFan(t)

		err := repo.Create(ctx, &entity.Passkey{ProviderPasskeyID: "token-1", UserID: fan.ID, AAGUID: googlePasswordManagerAAGUID, CreateTime: oct1})

		require.NoError(t, err)
		got, err := repo.ListByUser(ctx, fan.ID)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "token-1", got[0].ProviderPasskeyID)
		assert.Equal(t, fan.ID, got[0].UserID)
		assert.Equal(t, googlePasswordManagerAAGUID, got[0].AAGUID)
		assert.True(t, oct1.Equal(got[0].CreateTime))
	})

	// @spec components/entity/passkey/create "Same passkey again"
	t.Run("Create for a stored provider passkey id succeeds and leaves the passkey unchanged", func(t *testing.T) {
		fan := newFan(t)
		require.NoError(t, repo.Create(ctx, &entity.Passkey{ProviderPasskeyID: "token-1", UserID: fan.ID, AAGUID: googlePasswordManagerAAGUID, CreateTime: oct1}))

		err := repo.Create(ctx, &entity.Passkey{ProviderPasskeyID: "token-1", UserID: fan.ID, AAGUID: entity.ZeroAAGUID, CreateTime: oct20})

		require.NoError(t, err)
		got, err := repo.ListByUser(ctx, fan.ID)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, googlePasswordManagerAAGUID, got[0].AAGUID)
		assert.True(t, oct1.Equal(got[0].CreateTime))
	})

	// @spec components/entity/passkey/create "Unknown User"
	t.Run("Create for a user that does not exist fails with NotFound and stores nothing", func(t *testing.T) {
		cleanDatabase(t)
		missing := "019a0000-0000-7000-8000-000000000001"

		err := repo.Create(ctx, &entity.Passkey{ProviderPasskeyID: "token-1", UserID: missing, AAGUID: entity.ZeroAAGUID, CreateTime: oct1})

		assert.ErrorIs(t, err, apperr.ErrNotFound)
		got, err := repo.ListByUser(ctx, missing)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("Create refuses an AAGUID that is not a UUID", func(t *testing.T) {
		fan := newFan(t)

		err := repo.Create(ctx, &entity.Passkey{ProviderPasskeyID: "token-1", UserID: fan.ID, AAGUID: "", CreateTime: oct1})

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	// @spec components/entity/passkey/delete "Recorded passkey"
	t.Run("Delete removes the stored passkey", func(t *testing.T) {
		fan := newFan(t)
		require.NoError(t, repo.Create(ctx, &entity.Passkey{ProviderPasskeyID: "token-1", UserID: fan.ID, AAGUID: entity.ZeroAAGUID, CreateTime: oct1}))

		require.NoError(t, repo.Delete(ctx, "token-1"))

		got, err := repo.ListByUser(ctx, fan.ID)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	// @spec components/entity/passkey/delete "Never recorded"
	t.Run("Delete succeeds when no passkey has the provider passkey id", func(t *testing.T) {
		cleanDatabase(t)

		assert.NoError(t, repo.Delete(ctx, "token-never-recorded"))
	})

	// @spec components/entity/passkey/list-by-user "Two recorded passkeys"
	t.Run("ListByUser returns both passkeys, newest first", func(t *testing.T) {
		fan := newFan(t)
		require.NoError(t, repo.Create(ctx, &entity.Passkey{ProviderPasskeyID: "token-oct1", UserID: fan.ID, AAGUID: entity.ZeroAAGUID, CreateTime: oct1}))
		require.NoError(t, repo.Create(ctx, &entity.Passkey{ProviderPasskeyID: "token-oct20", UserID: fan.ID, AAGUID: googlePasswordManagerAAGUID, CreateTime: oct20}))

		got, err := repo.ListByUser(ctx, fan.ID)

		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "token-oct20", got[0].ProviderPasskeyID)
		assert.Equal(t, "token-oct1", got[1].ProviderPasskeyID)
	})

	// @spec components/entity/passkey/list-by-user "Nothing recorded"
	t.Run("ListByUser returns an empty list for a user with no passkey", func(t *testing.T) {
		fan := newFan(t)

		got, err := repo.ListByUser(ctx, fan.ID)

		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})

	t.Run("deleting the user removes its passkeys", func(t *testing.T) {
		fan := newFan(t)
		require.NoError(t, repo.Create(ctx, &entity.Passkey{ProviderPasskeyID: "token-1", UserID: fan.ID, AAGUID: entity.ZeroAAGUID, CreateTime: oct1}))

		require.NoError(t, users.Delete(ctx, fan.ID))

		got, err := repo.ListByUser(ctx, fan.ID)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}
