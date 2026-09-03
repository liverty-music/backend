package pocketsign_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	infrapocketsign "github.com/liverty-music/backend/internal/infrastructure/pocketsign"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStubVerifier_StartVerify asserts that StubVerifier.StartVerify returns
// an Unavailable error on every call — the safe pre-onboarding default.
func TestStubVerifier_StartVerify(t *testing.T) {
	t.Parallel()

	s := infrapocketsign.NewStubVerifier()
	sid, url, err := s.StartVerify(context.Background(), "user-1", entity.VerificationMethodJPKI)

	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrUnavailable,
		"StubVerifier.StartVerify must return Unavailable (Pocket Sign not yet configured)")
	assert.Empty(t, sid, "session id must be empty on error")
	assert.Empty(t, url, "redirect url must be empty on error")
}

// TestStubVerifier_CompleteVerify asserts that StubVerifier.CompleteVerify
// returns Unavailable — the API is not yet wired.
func TestStubVerifier_CompleteVerify(t *testing.T) {
	t.Parallel()

	s := infrapocketsign.NewStubVerifier()
	result, err := s.CompleteVerify(context.Background(), "user-1", "sess-xyz")

	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrUnavailable,
		"StubVerifier.CompleteVerify must return Unavailable (Pocket Sign not yet configured)")
	assert.Nil(t, result, "result must be nil on error")
}

// TestStubVerifier_Recheck asserts that StubVerifier.Recheck returns
// Unavailable — the verify API is not yet wired.
func TestStubVerifier_Recheck(t *testing.T) {
	t.Parallel()

	s := infrapocketsign.NewStubVerifier()
	result, err := s.Recheck(context.Background(), "ps-user-123")

	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrUnavailable,
		"StubVerifier.Recheck must return Unavailable (Pocket Sign not yet configured)")
	assert.Nil(t, result, "result must be nil on error")
}
