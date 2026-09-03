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

// TestVerifiedIdentityRepository_CreateAndGet covers the happy-path round-trip
// plus the ACTIVE-only reads: GetByUserID returns any-status rows while
// GetByPocketSignUserID only surfaces the ACTIVE person key.
func TestVerifiedIdentityRepository_CreateAndGet(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	ctx := context.Background()
	repo := rdb.NewVerifiedIdentityRepository(testDB)

	userID := seedUser(t, "vi-user", "vi-user@example.com", "ext-vi-1")
	vi := entity.NewVerifiedIdentity(userID, "ps-key-1", entity.VerificationMethodJPKI)

	created, err := repo.Create(ctx, vi)
	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Equal(t, userID, created.UserID)
	assert.Equal(t, "ps-key-1", created.PocketSignUserID)
	assert.Equal(t, entity.VerificationMethodJPKI, created.Method)
	assert.Equal(t, entity.DedupeStrengthStrong, created.DedupeStrength, "JPKI derives STRONG dedupe")
	assert.Equal(t, entity.VerificationStatusActive, created.Status)

	got, err := repo.GetByUserID(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, "ps-key-1", got.PocketSignUserID)

	byKey, err := repo.GetByPocketSignUserID(ctx, "ps-key-1")
	require.NoError(t, err)
	assert.Equal(t, created.ID, byKey.ID)
}

// TestVerifiedIdentityRepository_DedupePartialUniqueIndex is the core anti-scalp
// invariant: at most ONE ACTIVE verified identity may exist per Pocket Sign
// person key (partial unique index `WHERE status=ACTIVE`). A second ACTIVE row
// for the same key — even on a different account — must be rejected; but once the
// first is downgraded (NEEDS_REVERIFICATION), the key is free again.
func TestVerifiedIdentityRepository_DedupePartialUniqueIndex(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	ctx := context.Background()
	repo := rdb.NewVerifiedIdentityRepository(testDB)

	user1 := seedUser(t, "person-a1", "a1@example.com", "ext-a1")
	user2 := seedUser(t, "person-a2", "a2@example.com", "ext-a2")
	const sharedKey = "ps-shared-person"

	first, err := repo.Create(ctx, entity.NewVerifiedIdentity(user1, sharedKey, entity.VerificationMethodJPKI))
	require.NoError(t, err)

	// Second ACTIVE row with the SAME person key on a DIFFERENT account → rejected.
	_, err = repo.Create(ctx, entity.NewVerifiedIdentity(user2, sharedKey, entity.VerificationMethodJPKI))
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrAlreadyExists,
		"a second ACTIVE verified identity for the same pocket_sign_user_id must be rejected (1 person = 1 account)")

	// Downgrade the first to NEEDS_REVERIFICATION → the partial index no longer
	// guards the key, so a fresh ACTIVE row for the second account now succeeds.
	require.NoError(t, repo.UpdateStatus(ctx, first.ID, entity.VerificationStatusNeedsReverification))
	_, err = repo.Create(ctx, entity.NewVerifiedIdentity(user2, sharedKey, entity.VerificationMethodJPKI))
	require.NoError(t, err,
		"the partial unique index guards only ACTIVE rows; a non-active row must not block re-verification")

	// GetByPocketSignUserID (ACTIVE-only) now resolves to the second account.
	active, err := repo.GetByPocketSignUserID(ctx, sharedKey)
	require.NoError(t, err)
	assert.Equal(t, user2, active.UserID)
}

// TestVerifiedIdentityRepository_CreateMissingUser proves the FK precondition:
// inserting a verified identity for a non-existent account fails closed with
// FailedPrecondition (not a bare DB error).
func TestVerifiedIdentityRepository_CreateMissingUser(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	ctx := context.Background()
	repo := rdb.NewVerifiedIdentityRepository(testDB)

	// A syntactically valid but non-existent user id.
	orphan := entity.NewVerifiedIdentity(entity.NewID(), "ps-orphan", entity.VerificationMethodJPKI)

	_, err := repo.Create(ctx, orphan)
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition,
		"a verified identity for a non-existent user must fail with FailedPrecondition (FK violation)")
}

// TestVerifiedIdentityRepository_NotFoundPaths pins every NotFound path:
// GetByUserID / GetByPocketSignUserID on an unknown key, and UpdateStatus /
// Delete on an unknown id (RowsAffected == 0).
func TestVerifiedIdentityRepository_NotFoundPaths(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	ctx := context.Background()
	repo := rdb.NewVerifiedIdentityRepository(testDB)

	_, err := repo.GetByUserID(ctx, entity.NewID())
	assert.ErrorIs(t, err, apperr.ErrNotFound, "GetByUserID on an unknown user must return NotFound")

	_, err = repo.GetByPocketSignUserID(ctx, "no-such-key")
	assert.ErrorIs(t, err, apperr.ErrNotFound, "GetByPocketSignUserID on an unknown key must return NotFound")

	err = repo.UpdateStatus(ctx, entity.NewID(), entity.VerificationStatusNeedsReverification)
	assert.ErrorIs(t, err, apperr.ErrNotFound, "UpdateStatus on an unknown id must return NotFound (RowsAffected==0)")

	err = repo.Delete(ctx, entity.NewID())
	assert.ErrorIs(t, err, apperr.ErrNotFound, "Delete on an unknown id must return NotFound (RowsAffected==0)")
}

// TestVerifiedIdentityRepository_UpdateStatusAndDelete covers the mutating happy
// paths: a status change is observable, and Delete removes the row.
func TestVerifiedIdentityRepository_UpdateStatusAndDelete(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	ctx := context.Background()
	repo := rdb.NewVerifiedIdentityRepository(testDB)

	userID := seedUser(t, "vi-mut", "vi-mut@example.com", "ext-vi-mut")
	created, err := repo.Create(ctx, entity.NewVerifiedIdentity(userID, "ps-mut", entity.VerificationMethodJPKI))
	require.NoError(t, err)

	require.NoError(t, repo.UpdateStatus(ctx, created.ID, entity.VerificationStatusNeedsReverification))
	got, err := repo.GetByUserID(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, entity.VerificationStatusNeedsReverification, got.Status,
		"GetByUserID must reflect the updated status (any-status read)")

	require.NoError(t, repo.Delete(ctx, created.ID))
	_, err = repo.GetByUserID(ctx, userID)
	assert.ErrorIs(t, err, apperr.ErrNotFound, "after Delete the user has no verified identity")
}
