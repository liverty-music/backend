package usecase_test

import (
	"context"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/zkp"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// zkpTestdataDir returns the path to configs/zkp/testdata from the test file location.
func zkpTestdataDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "configs", "zkp", "testdata")
}

func zkpVKPath() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "configs", "zkp", "verification_key.json")
}

// loadTestFixtures loads the snarkjs-generated proof and public signals.
func loadTestFixtures(t *testing.T) (proofJSON, publicSignalsJSON string) {
	t.Helper()

	proof, err := os.ReadFile(filepath.Join(zkpTestdataDir(), "proof.json"))
	require.NoError(t, err)

	signals, err := os.ReadFile(filepath.Join(zkpTestdataDir(), "public_signals.json"))
	require.NoError(t, err)

	return string(proof), string(signals)
}

// TestVerifyEntry_Integration_DuplicateNullifier verifies the full flow:
// real ZKP verifier + usecase logic. Submitting the same valid proof twice
// should succeed the first time and return "already checked in" the second time.
func TestVerifyEntry_Integration_DuplicateNullifier(t *testing.T) {
	t.Parallel()

	// Use the real ZKP verifier with actual verification key.
	verifier, err := zkp.NewVerifier(zkpVKPath())
	require.NoError(t, err, "failed to load verification key")

	proofJSON, publicSignalsJSON := loadTestFixtures(t)

	// The test fixture uses event UUID "550e8400-e29b-41d4-a716-446655440000".
	// The merkle root from metadata.json (as big.Int bytes).
	fixtureEventID := "550e8400-e29b-41d4-a716-446655440000"

	// Parse merkle root from public signals to set up the event repo stub.
	// Public signals: [merkleRoot, eventId, nullifierHash]
	merkleRootBig := mustParseBigInt(t, "6331401000423026358291629782353603237933267665498208286537849807283925720420")
	merkleRootBytes := bigIntToBytes32(merkleRootBig)

	eventRepo := &stubEventRepo{merkleRoot: merkleRootBytes}
	nullifiers := &stubNullifierRepo{existsResult: false}

	uc := newTestEntryUC(verifier, nullifiers, nil, eventRepo, nil)

	params := &usecase.VerifyEntryParams{
		EventID:           fixtureEventID,
		ProofJSON:         proofJSON,
		PublicSignalsJSON: publicSignalsJSON,
	}

	// First verification should succeed.
	result, err := uc.VerifyEntry(context.Background(), params)
	require.NoError(t, err)
	assert.True(t, result.Verified, "first verification should succeed")
	assert.Contains(t, result.Message, "entry verified")
	assert.Len(t, nullifiers.inserted, 1, "nullifier should be inserted")

	// Simulate that the nullifier now exists in the store.
	nullifiers.existsResult = true

	// Second verification with the same proof should be rejected.
	result2, err := uc.VerifyEntry(context.Background(), params)
	require.NoError(t, err)
	assert.False(t, result2.Verified, "duplicate nullifier should be rejected")
	assert.Contains(t, result2.Message, "already checked in")
}

// TestVerifyEntry_Integration_ConcurrentNullifierRace tests the race condition
// where two concurrent verifications pass the Exists check but one fails on Insert.
func TestVerifyEntry_Integration_ConcurrentNullifierRace(t *testing.T) {
	t.Parallel()

	verifier, err := zkp.NewVerifier(zkpVKPath())
	require.NoError(t, err)

	proofJSON, publicSignalsJSON := loadTestFixtures(t)
	fixtureEventID := "550e8400-e29b-41d4-a716-446655440000"

	merkleRootBig := mustParseBigInt(t, "6331401000423026358291629782353603237933267665498208286537849807283925720420")
	merkleRootBytes := bigIntToBytes32(merkleRootBig)

	eventRepo := &stubEventRepo{merkleRoot: merkleRootBytes}
	// Nullifier doesn't exist (race: both checks pass), but insert fails
	// with AlreadyExists (concurrent insert succeeded first).
	nullifiers := &stubNullifierRepo{
		existsResult: false,
		insertErr:    apperr.ErrAlreadyExists,
	}

	uc := newTestEntryUC(verifier, nullifiers, nil, eventRepo, nil)

	result, err := uc.VerifyEntry(context.Background(), &usecase.VerifyEntryParams{
		EventID:           fixtureEventID,
		ProofJSON:         proofJSON,
		PublicSignalsJSON: publicSignalsJSON,
	})

	require.NoError(t, err)
	assert.False(t, result.Verified)
	assert.Contains(t, result.Message, "already checked in")
}

func mustParseBigInt(t *testing.T, s string) *big.Int {
	t.Helper()
	n := new(big.Int)
	_, ok := n.SetString(s, 10)
	require.True(t, ok, "failed to parse big.Int: %s", s)
	return n
}
