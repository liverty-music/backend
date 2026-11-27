package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserRepository_RemovedIdentity(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewUserRepository(db, testOutbox)
	ctx := context.Background()
	user, err := repo.Create(ctx, newTestUser("ext-removed", "removed@example.com", "Removed"))
	require.NoError(t, err)
	require.NoError(t, repo.Delete(ctx, user.ID))

	// @spec components/entity/user/delete "Identity recorded as removed"
	// @spec components/entity/user/create "Identity of a removed User"
	t.Run("the identity of a removed user gets no new user", func(t *testing.T) {
		announced := countRows(t, db, `SELECT count(*) FROM watermill_events_outbox`)

		_, err := repo.Create(ctx, newTestUser("ext-removed", "other@example.com", "Again"))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.False(t, rowExists(t, db, `SELECT 1 FROM users WHERE provider_user_id = $1`, "ext-removed"))
		assert.Equal(t, announced, countRows(t, db, `SELECT count(*) FROM watermill_events_outbox`), "nothing is announced")
	})

	t.Run("a user stored for a recorded identity by older code can still be removed", func(t *testing.T) {
		recorded, err := repo.Create(ctx, newTestUser("ext-recorded", "recorded@example.com", "Recorded"))
		require.NoError(t, err)
		_, err = db.Pool.Exec(ctx, `INSERT INTO deleted_user_identities (provider_user_id) VALUES ($1)`, "ext-recorded")
		require.NoError(t, err)

		require.NoError(t, repo.Delete(ctx, recorded.ID))

		assert.False(t, rowExists(t, db, `SELECT 1 FROM users WHERE id = $1`, recorded.ID))
	})

	// @spec components/entity/user/create "Same email with a new identity"
	t.Run("the email of a removed user is free for another identity", func(t *testing.T) {
		_, err := repo.Create(ctx, newTestUser("ext-removed-new", "removed@example.com", "New"))

		assert.NoError(t, err)
	})
}

// The create starts while the removal's transaction holds the deleted row and
// has not committed, so its insert waits on the unique index.
//
// @spec components/entity/user/create "Removal completing during Create"
func TestUserRepository_CreateWhileTheUserIsRemoved(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewUserRepository(db, testOutbox)
	ctx := context.Background()
	user, err := repo.Create(ctx, newTestUser("ext-racing", "racing@example.com", "Racing"))
	require.NoError(t, err)
	created := make(chan error, 1)

	err = repo.SerializeDeletion(ctx, user.ID, func(txCtx context.Context) error {
		if err := repo.Delete(txCtx, user.ID); err != nil {
			return err
		}
		go func() {
			_, err := repo.Create(ctx, newTestUser("ext-racing", "racing-2@example.com", "Again"))
			created <- err
		}()
		require.Eventually(t, func() bool {
			return countRows(t, db, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`) > 0
		}, 10*time.Second, 10*time.Millisecond, "the create waits for the removal")
		return nil
	})
	require.NoError(t, err)

	assert.ErrorIs(t, <-created, apperr.ErrFailedPrecondition)
	assert.False(t, rowExists(t, db, `SELECT 1 FROM users WHERE provider_user_id = $1`, "ext-racing"))
}

// countRows returns the number a count query answers.
func countRows(t *testing.T, db *rdb.Database, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, db.Pool.QueryRow(context.Background(), query, args...).Scan(&n))
	return n
}

func TestUserRepository_ForgetRemovedIdentity(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewUserRepository(db, testOutbox)
	ctx := context.Background()

	// @spec components/entity/user/forget-removed-identity "Recorded identity"
	t.Run("no record of a recorded identity remains", func(t *testing.T) {
		user, err := repo.Create(ctx, newTestUser("ext-forgotten", "forgotten@example.com", "Forgotten"))
		require.NoError(t, err)
		require.NoError(t, repo.Delete(ctx, user.ID))

		require.NoError(t, repo.ForgetRemovedIdentity(ctx, "ext-forgotten"))

		assert.False(t, rowExists(t, db, `SELECT 1 FROM deleted_user_identities WHERE provider_user_id = $1`, "ext-forgotten"))
		_, err = repo.Create(ctx, newTestUser("ext-forgotten", "forgotten-again@example.com", "Again"))
		assert.NoError(t, err, "Create no longer refuses the identity")
	})

	// @spec components/entity/user/forget-removed-identity "No record"
	t.Run("an identity with no record counts as forgotten", func(t *testing.T) {
		assert.NoError(t, repo.ForgetRemovedIdentity(ctx, "ext-never-recorded"))
	})

	// @spec components/entity/user/forget-removed-identity "Empty provider user ID"
	t.Run("an empty provider user ID is refused", func(t *testing.T) {
		assert.ErrorIs(t, repo.ForgetRemovedIdentity(ctx, ""), apperr.ErrInvalidArgument)
	})
}
