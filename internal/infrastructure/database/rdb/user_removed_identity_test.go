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
