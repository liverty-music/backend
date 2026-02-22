package usecase_test

import (
	"context"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Inline mocks for entry-specific interfaces ---

type stubZKPVerifier struct {
	verified bool
	err      error
}

func (s *stubZKPVerifier) Verify(_, _ string) (bool, error) {
	return s.verified, s.err
}

type stubNullifierRepo struct {
	existsResult bool
	existsErr    error
	insertErr    error
	inserted     [][]byte
}

func (s *stubNullifierRepo) Exists(_ context.Context, _ string, _ []byte) (bool, error) {
	return s.existsResult, s.existsErr
}

func (s *stubNullifierRepo) Insert(_ context.Context, _ string, hash []byte) error {
	s.inserted = append(s.inserted, hash)
	return s.insertErr
}

type stubMerkleTreeRepo struct {
	storeBatchErr         error
	storeBatchWithRootErr error
	pathElements          [][]byte
	pathIndices           []uint32
	pathErr               error
	root                  []byte
	rootErr               error
	leaf                  []byte
	leafErr               error
}

func (s *stubMerkleTreeRepo) StoreBatch(_ context.Context, _ string, _ []*entity.MerkleNode) error {
	return s.storeBatchErr
}

func (s *stubMerkleTreeRepo) StoreBatchWithRoot(_ context.Context, _ string, _ []*entity.MerkleNode, _ []byte) error {
	return s.storeBatchWithRootErr
}

func (s *stubMerkleTreeRepo) GetPath(_ context.Context, _ string, _ int, _ int) ([][]byte, []uint32, error) {
	return s.pathElements, s.pathIndices, s.pathErr
}

func (s *stubMerkleTreeRepo) GetRoot(_ context.Context, _ string) ([]byte, error) {
	return s.root, s.rootErr
}

func (s *stubMerkleTreeRepo) GetLeaf(_ context.Context, _ string, _ int) ([]byte, error) {
	return s.leaf, s.leafErr
}

type stubEventRepo struct {
	merkleRoot     []byte
	merkleRootErr  error
	updateRootErr  error
	leafIndex      int
	leafIndexErr   error
	updatedRootVal []byte
}

func (s *stubEventRepo) GetMerkleRoot(_ context.Context, _ string) ([]byte, error) {
	return s.merkleRoot, s.merkleRootErr
}

func (s *stubEventRepo) UpdateMerkleRoot(_ context.Context, _ string, root []byte) error {
	s.updatedRootVal = root
	return s.updateRootErr
}

func (s *stubEventRepo) GetTicketLeafIndex(_ context.Context, _, _ string) (int, error) {
	return s.leafIndex, s.leafIndexErr
}

// --- Helper to build public signals JSON ---

func makePublicSignals(merkleRoot, nullifierHash *big.Int) string {
	signals := []string{merkleRoot.String(), nullifierHash.String()}
	b, _ := json.Marshal(signals)
	return string(b)
}

func bigIntToBytes32(n *big.Int) []byte {
	buf := make([]byte, 32)
	b := n.Bytes()
	copy(buf[32-len(b):], b)
	return buf
}

func newTestEntryUC(
	verifier entity.ZKPVerifier,
	nullifiers entity.NullifierRepository,
	merkleTree entity.MerkleTreeRepository,
	eventRepo entity.EventRepository,
	ticketRepo entity.TicketRepository,
) usecase.EntryUseCase {
	logger, _ := logging.New()
	return usecase.NewEntryUseCase(verifier, nullifiers, merkleTree, eventRepo, ticketRepo, logger)
}

// --- VerifyEntry tests ---

func TestVerifyEntry_Validation(t *testing.T) {
	t.Parallel()

	uc := newTestEntryUC(nil, nil, nil, nil, nil)

	tests := []struct {
		name   string
		params *usecase.VerifyEntryParams
	}{
		{"nil params", nil},
		{"empty event_id", &usecase.VerifyEntryParams{ProofJSON: "p", PublicSignalsJSON: "s"}},
		{"empty proof_json", &usecase.VerifyEntryParams{EventID: "e1", PublicSignalsJSON: "s"}},
		{"empty public_signals_json", &usecase.VerifyEntryParams{EventID: "e1", ProofJSON: "p"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := uc.VerifyEntry(context.Background(), tc.params)
			assert.Nil(t, result)
			assert.Error(t, err)
			assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		})
	}
}

func TestVerifyEntry_InvalidPublicSignals(t *testing.T) {
	t.Parallel()

	uc := newTestEntryUC(&stubZKPVerifier{}, &stubNullifierRepo{}, nil, &stubEventRepo{}, nil)

	tests := []struct {
		name          string
		publicSignals string
	}{
		{"not valid json", "not-json"},
		{"empty array", "[]"},
		{"single element", `["123"]`},
		{"invalid nullifier", `["123", "not-a-number"]`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := uc.VerifyEntry(context.Background(), &usecase.VerifyEntryParams{
				EventID:           "event-1",
				ProofJSON:         `{"proof": true}`,
				PublicSignalsJSON: tc.publicSignals,
			})
			assert.Nil(t, result)
			assert.Error(t, err)
		})
	}
}

func TestVerifyEntry_MerkleRootMismatch(t *testing.T) {
	t.Parallel()

	proofRoot := big.NewInt(12345)
	expectedRoot := big.NewInt(99999)

	eventRepo := &stubEventRepo{
		merkleRoot: bigIntToBytes32(expectedRoot),
	}
	uc := newTestEntryUC(
		&stubZKPVerifier{verified: true},
		&stubNullifierRepo{},
		nil,
		eventRepo,
		nil,
	)

	signals := makePublicSignals(proofRoot, big.NewInt(1))
	result, err := uc.VerifyEntry(context.Background(), &usecase.VerifyEntryParams{
		EventID:           "event-1",
		ProofJSON:         `{}`,
		PublicSignalsJSON: signals,
	})

	require.NoError(t, err)
	assert.False(t, result.Verified)
	assert.Contains(t, result.Message, "merkle root mismatch")
}

func TestVerifyEntry_DuplicateNullifier(t *testing.T) {
	t.Parallel()

	root := big.NewInt(42)

	eventRepo := &stubEventRepo{
		merkleRoot: bigIntToBytes32(root),
	}
	nullifiers := &stubNullifierRepo{existsResult: true}

	uc := newTestEntryUC(
		&stubZKPVerifier{verified: true},
		nullifiers,
		nil,
		eventRepo,
		nil,
	)

	signals := makePublicSignals(root, big.NewInt(100))
	result, err := uc.VerifyEntry(context.Background(), &usecase.VerifyEntryParams{
		EventID:           "event-1",
		ProofJSON:         `{}`,
		PublicSignalsJSON: signals,
	})

	require.NoError(t, err)
	assert.False(t, result.Verified)
	assert.Contains(t, result.Message, "already checked in")
}

func TestVerifyEntry_ProofFails(t *testing.T) {
	t.Parallel()

	root := big.NewInt(42)

	eventRepo := &stubEventRepo{merkleRoot: bigIntToBytes32(root)}
	nullifiers := &stubNullifierRepo{existsResult: false}
	verifier := &stubZKPVerifier{verified: false}

	uc := newTestEntryUC(verifier, nullifiers, nil, eventRepo, nil)

	signals := makePublicSignals(root, big.NewInt(100))
	result, err := uc.VerifyEntry(context.Background(), &usecase.VerifyEntryParams{
		EventID:           "event-1",
		ProofJSON:         `{}`,
		PublicSignalsJSON: signals,
	})

	require.NoError(t, err)
	assert.False(t, result.Verified)
	assert.Contains(t, result.Message, "proof verification failed")
}

func TestVerifyEntry_Success(t *testing.T) {
	t.Parallel()

	root := big.NewInt(42)

	eventRepo := &stubEventRepo{merkleRoot: bigIntToBytes32(root)}
	nullifiers := &stubNullifierRepo{existsResult: false}
	verifier := &stubZKPVerifier{verified: true}

	uc := newTestEntryUC(verifier, nullifiers, nil, eventRepo, nil)

	signals := makePublicSignals(root, big.NewInt(100))
	result, err := uc.VerifyEntry(context.Background(), &usecase.VerifyEntryParams{
		EventID:           "event-1",
		ProofJSON:         `{}`,
		PublicSignalsJSON: signals,
	})

	require.NoError(t, err)
	assert.True(t, result.Verified)
	assert.Contains(t, result.Message, "entry verified")
	assert.Len(t, nullifiers.inserted, 1, "nullifier should be recorded")
}

func TestVerifyEntry_ConcurrentNullifierInsert(t *testing.T) {
	t.Parallel()

	root := big.NewInt(42)

	eventRepo := &stubEventRepo{merkleRoot: bigIntToBytes32(root)}
	// Nullifier doesn't exist during check, but insert fails with AlreadyExists
	// (concurrent verification succeeded first).
	nullifiers := &stubNullifierRepo{
		existsResult: false,
		insertErr:    apperr.ErrAlreadyExists,
	}
	verifier := &stubZKPVerifier{verified: true}

	uc := newTestEntryUC(verifier, nullifiers, nil, eventRepo, nil)

	signals := makePublicSignals(root, big.NewInt(100))
	result, err := uc.VerifyEntry(context.Background(), &usecase.VerifyEntryParams{
		EventID:           "event-1",
		ProofJSON:         `{}`,
		PublicSignalsJSON: signals,
	})

	require.NoError(t, err)
	assert.False(t, result.Verified)
	assert.Contains(t, result.Message, "already checked in")
}

// --- GetMerklePath tests ---

func TestGetMerklePath_Validation(t *testing.T) {
	t.Parallel()

	uc := newTestEntryUC(nil, nil, nil, nil, nil)

	tests := []struct {
		name    string
		eventID string
		userID  string
	}{
		{"empty event_id", "", "user-1"},
		{"empty user_id", "event-1", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := uc.GetMerklePath(context.Background(), tc.eventID, tc.userID)
			assert.Nil(t, result)
			assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		})
	}
}

func TestGetMerklePath_NoTicket(t *testing.T) {
	t.Parallel()

	eventRepo := &stubEventRepo{leafIndex: -1}
	uc := newTestEntryUC(nil, nil, nil, eventRepo, nil)

	result, err := uc.GetMerklePath(context.Background(), "event-1", "user-1")
	assert.Nil(t, result)
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}

func TestGetMerklePath_Success(t *testing.T) {
	t.Parallel()

	root := []byte{1, 2, 3}
	pathElements := [][]byte{{4, 5, 6}, {7, 8, 9}}
	pathIndices := []uint32{0, 1}
	leaf := []byte{10, 11, 12}

	eventRepo := &stubEventRepo{
		leafIndex:  0,
		merkleRoot: root,
	}
	merkleTreeRepo := &stubMerkleTreeRepo{
		pathElements: pathElements,
		pathIndices:  pathIndices,
		leaf:         leaf,
	}

	uc := newTestEntryUC(nil, nil, merkleTreeRepo, eventRepo, nil)

	result, err := uc.GetMerklePath(context.Background(), "event-1", "user-1")
	require.NoError(t, err)
	assert.Equal(t, root, result.MerkleRoot)
	assert.Equal(t, pathElements, result.PathElements)
	assert.Equal(t, pathIndices, result.PathIndices)
	assert.Equal(t, leaf, result.Leaf)
}

// --- BuildMerkleTree tests ---

func TestBuildMerkleTree_Validation(t *testing.T) {
	t.Parallel()

	uc := newTestEntryUC(nil, nil, nil, nil, nil)

	err := uc.BuildMerkleTree(context.Background(), "")
	assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
}

func TestBuildMerkleTree_Success(t *testing.T) {
	t.Parallel()

	ticketRepo := &mocks.MockTicketRepository{}
	ticketRepo.On("ListByEvent", context.Background(), "event-1").Return([]*entity.Ticket{
		{UserID: "user-1"},
		{UserID: "user-2"},
	}, nil)

	merkleTreeRepo := &stubMerkleTreeRepo{}
	eventRepo := &stubEventRepo{}

	uc := newTestEntryUC(nil, nil, merkleTreeRepo, eventRepo, ticketRepo)

	err := uc.BuildMerkleTree(context.Background(), "event-1")
	require.NoError(t, err)
	ticketRepo.AssertExpectations(t)
}

func TestBuildMerkleTree_StoreBatchWithRootError(t *testing.T) {
	t.Parallel()

	ticketRepo := &mocks.MockTicketRepository{}
	ticketRepo.On("ListByEvent", context.Background(), "event-1").Return([]*entity.Ticket{
		{UserID: "user-1"},
	}, nil)

	merkleTreeRepo := &stubMerkleTreeRepo{
		storeBatchWithRootErr: assert.AnError,
	}
	eventRepo := &stubEventRepo{}

	uc := newTestEntryUC(nil, nil, merkleTreeRepo, eventRepo, ticketRepo)

	err := uc.BuildMerkleTree(context.Background(), "event-1")
	assert.Error(t, err)
}

// --- Error propagation tests ---

func TestVerifyEntry_GetMerkleRootError(t *testing.T) {
	t.Parallel()

	root := big.NewInt(42)
	eventRepo := &stubEventRepo{merkleRootErr: assert.AnError}

	uc := newTestEntryUC(&stubZKPVerifier{}, &stubNullifierRepo{}, nil, eventRepo, nil)

	signals := makePublicSignals(root, big.NewInt(100))
	result, err := uc.VerifyEntry(context.Background(), &usecase.VerifyEntryParams{
		EventID:           "event-1",
		ProofJSON:         `{}`,
		PublicSignalsJSON: signals,
	})

	assert.Nil(t, result)
	assert.Error(t, err)
}

func TestVerifyEntry_NullifierExistsError(t *testing.T) {
	t.Parallel()

	root := big.NewInt(42)
	eventRepo := &stubEventRepo{merkleRoot: bigIntToBytes32(root)}
	nullifiers := &stubNullifierRepo{existsErr: assert.AnError}

	uc := newTestEntryUC(&stubZKPVerifier{}, nullifiers, nil, eventRepo, nil)

	signals := makePublicSignals(root, big.NewInt(100))
	result, err := uc.VerifyEntry(context.Background(), &usecase.VerifyEntryParams{
		EventID:           "event-1",
		ProofJSON:         `{}`,
		PublicSignalsJSON: signals,
	})

	assert.Nil(t, result)
	assert.Error(t, err)
}

func TestVerifyEntry_VerifierError(t *testing.T) {
	t.Parallel()

	root := big.NewInt(42)
	eventRepo := &stubEventRepo{merkleRoot: bigIntToBytes32(root)}
	nullifiers := &stubNullifierRepo{}
	verifier := &stubZKPVerifier{err: assert.AnError}

	uc := newTestEntryUC(verifier, nullifiers, nil, eventRepo, nil)

	signals := makePublicSignals(root, big.NewInt(100))
	result, err := uc.VerifyEntry(context.Background(), &usecase.VerifyEntryParams{
		EventID:           "event-1",
		ProofJSON:         `{}`,
		PublicSignalsJSON: signals,
	})

	assert.Nil(t, result)
	assert.Error(t, err)
}

func TestVerifyEntry_InsertNullifierError(t *testing.T) {
	t.Parallel()

	root := big.NewInt(42)
	eventRepo := &stubEventRepo{merkleRoot: bigIntToBytes32(root)}
	nullifiers := &stubNullifierRepo{insertErr: assert.AnError}
	verifier := &stubZKPVerifier{verified: true}

	uc := newTestEntryUC(verifier, nullifiers, nil, eventRepo, nil)

	signals := makePublicSignals(root, big.NewInt(100))
	result, err := uc.VerifyEntry(context.Background(), &usecase.VerifyEntryParams{
		EventID:           "event-1",
		ProofJSON:         `{}`,
		PublicSignalsJSON: signals,
	})

	assert.Nil(t, result)
	assert.Error(t, err)
}

func TestVerifyEntry_OversizedPublicSignal(t *testing.T) {
	t.Parallel()

	// A number exceeding 32 bytes should return an error, not panic.
	huge := new(big.Int).Lsh(big.NewInt(1), 264) // 2^264 > 32 bytes
	signals := makePublicSignals(huge, big.NewInt(1))

	uc := newTestEntryUC(&stubZKPVerifier{}, &stubNullifierRepo{}, nil, &stubEventRepo{}, nil)

	result, err := uc.VerifyEntry(context.Background(), &usecase.VerifyEntryParams{
		EventID:           "event-1",
		ProofJSON:         `{}`,
		PublicSignalsJSON: signals,
	})

	assert.Nil(t, result)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds 32 bytes")
}

// --- GetMerklePath error propagation ---

func TestGetMerklePath_LeafIndexError(t *testing.T) {
	t.Parallel()

	eventRepo := &stubEventRepo{leafIndexErr: assert.AnError}
	uc := newTestEntryUC(nil, nil, nil, eventRepo, nil)

	result, err := uc.GetMerklePath(context.Background(), "event-1", "user-1")
	assert.Nil(t, result)
	assert.Error(t, err)
}

func TestGetMerklePath_GetRootError(t *testing.T) {
	t.Parallel()

	eventRepo := &stubEventRepo{leafIndex: 0, merkleRootErr: assert.AnError}
	uc := newTestEntryUC(nil, nil, nil, eventRepo, nil)

	result, err := uc.GetMerklePath(context.Background(), "event-1", "user-1")
	assert.Nil(t, result)
	assert.Error(t, err)
}

func TestGetMerklePath_GetPathError(t *testing.T) {
	t.Parallel()

	eventRepo := &stubEventRepo{leafIndex: 0, merkleRoot: []byte{1}}
	merkleTreeRepo := &stubMerkleTreeRepo{pathErr: assert.AnError}
	uc := newTestEntryUC(nil, nil, merkleTreeRepo, eventRepo, nil)

	result, err := uc.GetMerklePath(context.Background(), "event-1", "user-1")
	assert.Nil(t, result)
	assert.Error(t, err)
}

func TestGetMerklePath_GetLeafError(t *testing.T) {
	t.Parallel()

	eventRepo := &stubEventRepo{leafIndex: 0, merkleRoot: []byte{1}}
	merkleTreeRepo := &stubMerkleTreeRepo{
		pathElements: [][]byte{{1}},
		pathIndices:  []uint32{0},
		leafErr:      assert.AnError,
	}
	uc := newTestEntryUC(nil, nil, merkleTreeRepo, eventRepo, nil)

	result, err := uc.GetMerklePath(context.Background(), "event-1", "user-1")
	assert.Nil(t, result)
	assert.Error(t, err)
}

// --- BuildMerkleTree error propagation ---

func TestBuildMerkleTree_ListByEventError(t *testing.T) {
	t.Parallel()

	ticketRepo := &mocks.MockTicketRepository{}
	ticketRepo.On("ListByEvent", context.Background(), "event-1").Return(nil, assert.AnError)

	uc := newTestEntryUC(nil, nil, nil, nil, ticketRepo)

	err := uc.BuildMerkleTree(context.Background(), "event-1")
	assert.Error(t, err)
}

func TestBuildMerkleTree_EmptyTickets(t *testing.T) {
	t.Parallel()

	ticketRepo := &mocks.MockTicketRepository{}
	ticketRepo.On("ListByEvent", context.Background(), "event-1").Return([]*entity.Ticket{}, nil)

	merkleTreeRepo := &stubMerkleTreeRepo{}
	eventRepo := &stubEventRepo{}

	uc := newTestEntryUC(nil, nil, merkleTreeRepo, eventRepo, ticketRepo)

	err := uc.BuildMerkleTree(context.Background(), "event-1")
	require.NoError(t, err)
}
