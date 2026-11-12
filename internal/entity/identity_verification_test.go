package entity_test

import (
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
)

func TestIdentityVerification_NeedsReverification(t *testing.T) {
	t.Parallel()

	v := &entity.IdentityVerification{Status: entity.VerificationStatusActive}
	for state, want := range map[entity.CertificateState]bool{
		entity.CertificateStateGood:        false,
		entity.CertificateStateRevoked:     true,
		entity.CertificateStateExpired:     true,
		entity.CertificateStateUnknown:     false,
		entity.CertificateStateUnspecified: false,
	} {
		assert.Equal(t, want, v.NeedsReverification(state), "state %d", state)
	}
	assert.True(t, entity.CertificateStateUnknown.IsIndeterminate())
	assert.True(t, entity.CertificateStateUnspecified.IsIndeterminate())
	assert.False(t, entity.CertificateStateGood.IsIndeterminate())
	assert.False(t, entity.CertificateStateRevoked.IsIndeterminate(), "a confirmed bad state is determinate")
}

func TestCurrentIdentityVerification(t *testing.T) {
	t.Parallel()

	at := func(day int) time.Time { return time.Date(2026, 11, day, 0, 0, 0, 0, time.UTC) }
	record := func(id string, status entity.VerificationStatus, day int) *entity.IdentityVerification {
		return &entity.IdentityVerification{ID: id, Status: status, VerifyTime: at(day)}
	}
	active, activeOld := entity.VerificationStatusActive, entity.VerificationStatusActive
	needs := entity.VerificationStatusNeedsReverification

	assert.Nil(t, entity.CurrentIdentityVerification(nil), "no record")
	assert.Equal(t, "a", entity.CurrentIdentityVerification([]*entity.IdentityVerification{record("a", needs, 1)}).ID, "a single record")
	assert.Equal(t, "new", entity.CurrentIdentityVerification([]*entity.IdentityVerification{
		record("old", activeOld, 1), record("new", active, 5),
	}).ID, "the most recent Active one")
	assert.Equal(t, "act", entity.CurrentIdentityVerification([]*entity.IdentityVerification{
		record("flagged-newer", needs, 9), record("act", active, 2),
	}).ID, "an Active one beats a newer one flagged for re-verification")
	assert.Equal(t, "act", entity.CurrentIdentityVerification([]*entity.IdentityVerification{
		record("act", active, 2), record("flagged-newer", needs, 9),
	}).ID, "whatever the order")
	assert.Equal(t, "flagged-new", entity.CurrentIdentityVerification([]*entity.IdentityVerification{
		record("flagged-old", needs, 1), record("flagged-new", needs, 4),
	}).ID, "the most recent of those flagged when none is Active")
}

func TestCheckDedupe(t *testing.T) {
	t.Parallel()

	t.Run("the person is not verified yet", func(t *testing.T) {
		t.Parallel()
		got, err := entity.CheckDedupe("user-1", nil)
		require.NoError(t, err)
		assert.False(t, got.Changed())
	})

	t.Run("the person is verified on another account", func(t *testing.T) {
		t.Parallel()
		_, err := entity.CheckDedupe("user-1", &entity.IdentityVerification{UserID: "user-2", Status: entity.VerificationStatusActive})
		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
	})

	t.Run("a renewal moves the account's own record aside", func(t *testing.T) {
		t.Parallel()
		got, err := entity.CheckDedupe("user-1", &entity.IdentityVerification{UserID: "user-1", Status: entity.VerificationStatusActive})
		require.NoError(t, err)
		assert.Equal(t, entity.Transition[entity.VerificationStatus]{
			From: []entity.VerificationStatus{entity.VerificationStatusActive}, To: entity.VerificationStatusNeedsReverification,
		}, got)
	})
}
