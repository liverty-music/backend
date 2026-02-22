package usecase

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/merkle"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// DefaultTreeDepth is the default Merkle tree depth used for event entry.
const DefaultTreeDepth = 10

// EntryUseCase defines the interface for entry verification business logic.
type EntryUseCase interface {
	// VerifyEntry verifies a ZKP for event entry.
	// On success, atomically records the nullifier to prevent double-entry.
	VerifyEntry(ctx context.Context, params *VerifyEntryParams) (*VerifyEntryResult, error)

	// GetMerklePath returns the Merkle path for a user at an event.
	GetMerklePath(ctx context.Context, eventID, userID string) (*MerklePathResult, error)

	// BuildMerkleTree builds (or rebuilds) the Merkle tree for an event
	// from all ticket holders' identity commitments.
	BuildMerkleTree(ctx context.Context, eventID string) error
}

// VerifyEntryParams holds the inputs for entry verification.
type VerifyEntryParams struct {
	EventID           string
	ProofJSON         string
	PublicSignalsJSON string
}

// VerifyEntryResult holds the result of entry verification.
type VerifyEntryResult struct {
	Verified bool
	Message  string
}

// MerklePathResult holds the Merkle path data for proof generation.
type MerklePathResult struct {
	MerkleRoot   []byte
	PathElements [][]byte
	PathIndices  []uint32
	Leaf         []byte
}

// entryUseCase implements the EntryUseCase interface.
type entryUseCase struct {
	verifier   entity.ZKPVerifier
	nullifiers entity.NullifierRepository
	merkleTree entity.MerkleTreeRepository
	eventRepo  entity.EventRepository
	ticketRepo entity.TicketRepository
	logger     *logging.Logger
}

// Compile-time interface compliance check.
var _ EntryUseCase = (*entryUseCase)(nil)

// NewEntryUseCase creates a new entry use case.
func NewEntryUseCase(
	verifier entity.ZKPVerifier,
	nullifiers entity.NullifierRepository,
	merkleTree entity.MerkleTreeRepository,
	eventRepo entity.EventRepository,
	ticketRepo entity.TicketRepository,
	logger *logging.Logger,
) EntryUseCase {
	return &entryUseCase{
		verifier:   verifier,
		nullifiers: nullifiers,
		merkleTree: merkleTree,
		eventRepo:  eventRepo,
		ticketRepo: ticketRepo,
		logger:     logger,
	}
}

// VerifyEntry verifies a ZKP and records the nullifier on success.
func (uc *entryUseCase) VerifyEntry(ctx context.Context, params *VerifyEntryParams) (*VerifyEntryResult, error) {
	if params == nil {
		return nil, apperr.New(codes.InvalidArgument, "params cannot be nil")
	}

	if params.EventID == "" {
		return nil, apperr.New(codes.InvalidArgument, "event_id is required")
	}

	if params.ProofJSON == "" {
		return nil, apperr.New(codes.InvalidArgument, "proof_json is required")
	}

	if params.PublicSignalsJSON == "" {
		return nil, apperr.New(codes.InvalidArgument, "public_signals_json is required")
	}

	// Extract nullifier hash from public signals before verification.
	// Public signals order: [merkleRoot, nullifierHash]
	nullifierHash, err := extractNullifierHash(params.PublicSignalsJSON)
	if err != nil {
		return nil, apperr.Wrap(err, codes.InvalidArgument, "failed to extract nullifier hash from public signals")
	}

	// Extract and validate the Merkle root from public signals.
	merkleRoot, err := extractMerkleRoot(params.PublicSignalsJSON)
	if err != nil {
		return nil, apperr.Wrap(err, codes.InvalidArgument, "failed to extract merkle root from public signals")
	}

	expectedRoot, err := uc.eventRepo.GetMerkleRoot(ctx, params.EventID)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to get expected merkle root")
	}

	if !bytesEqual(merkleRoot, expectedRoot) {
		return &VerifyEntryResult{
			Verified: false,
			Message:  "merkle root mismatch: proof does not match event membership set",
		}, nil
	}

	// Check for duplicate nullifier before expensive ZKP verification.
	exists, err := uc.nullifiers.Exists(ctx, params.EventID, nullifierHash)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to check nullifier")
	}
	if exists {
		return &VerifyEntryResult{
			Verified: false,
			Message:  "already checked in for this event",
		}, nil
	}

	// Verify the ZKP.
	verified, err := uc.verifier.Verify(params.ProofJSON, params.PublicSignalsJSON)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to verify proof")
	}

	if !verified {
		return &VerifyEntryResult{
			Verified: false,
			Message:  "proof verification failed",
		}, nil
	}

	// Atomically insert nullifier to prevent double-entry.
	if err := uc.nullifiers.Insert(ctx, params.EventID, nullifierHash); err != nil {
		if errors.Is(err, apperr.ErrAlreadyExists) {
			// Concurrent verification succeeded first — treat as duplicate.
			return &VerifyEntryResult{
				Verified: false,
				Message:  "already checked in for this event",
			}, nil
		}
		return nil, apperr.Wrap(err, codes.Internal, "failed to record nullifier")
	}

	uc.logger.Info(ctx, "entry verified successfully",
		slog.String("event_id", params.EventID),
		slog.String("nullifier", hex.EncodeToString(nullifierHash)),
	)

	return &VerifyEntryResult{
		Verified: true,
		Message:  "entry verified",
	}, nil
}

// extractNullifierHash extracts the nullifier hash from the public signals JSON.
// Expected format: ["<merkleRoot>", "<nullifierHash>"]
func extractNullifierHash(publicSignalsJSON string) ([]byte, error) {
	var signals []string
	if err := json.Unmarshal([]byte(publicSignalsJSON), &signals); err != nil {
		return nil, fmt.Errorf("unmarshal public signals: %w", err)
	}

	if len(signals) < 2 {
		return nil, fmt.Errorf("expected at least 2 public signals, got %d", len(signals))
	}

	// Second signal is the nullifier hash.
	n := new(big.Int)
	if _, ok := n.SetString(signals[1], 10); !ok {
		return nil, fmt.Errorf("invalid nullifier hash: %s", signals[1])
	}

	buf := make([]byte, 32)
	b := n.Bytes()
	copy(buf[32-len(b):], b)
	return buf, nil
}

// extractMerkleRoot extracts the Merkle root from the public signals JSON.
// Expected format: ["<merkleRoot>", "<nullifierHash>"]
func extractMerkleRoot(publicSignalsJSON string) ([]byte, error) {
	var signals []string
	if err := json.Unmarshal([]byte(publicSignalsJSON), &signals); err != nil {
		return nil, fmt.Errorf("unmarshal public signals: %w", err)
	}

	if len(signals) < 2 {
		return nil, fmt.Errorf("expected at least 2 public signals, got %d", len(signals))
	}

	// First signal is the Merkle root.
	n := new(big.Int)
	if _, ok := n.SetString(signals[0], 10); !ok {
		return nil, fmt.Errorf("invalid merkle root: %s", signals[0])
	}

	buf := make([]byte, 32)
	b := n.Bytes()
	copy(buf[32-len(b):], b)
	return buf, nil
}

// bytesEqual compares two byte slices for equality.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// GetMerklePath returns the Merkle path for a user at an event.
func (uc *entryUseCase) GetMerklePath(ctx context.Context, eventID, userID string) (*MerklePathResult, error) {
	if eventID == "" {
		return nil, apperr.New(codes.InvalidArgument, "event_id is required")
	}

	if userID == "" {
		return nil, apperr.New(codes.InvalidArgument, "user_id is required")
	}

	// Get the user's leaf index from their ticket position.
	leafIndex, err := uc.eventRepo.GetTicketLeafIndex(ctx, eventID, userID)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to get ticket leaf index")
	}

	if leafIndex < 0 {
		return nil, apperr.New(codes.NotFound, "no ticket found for this user and event")
	}

	// Get the Merkle root.
	root, err := uc.eventRepo.GetMerkleRoot(ctx, eventID)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to get merkle root")
	}

	// Get the Merkle path.
	pathElements, pathIndices, err := uc.merkleTree.GetPath(ctx, eventID, leafIndex, DefaultTreeDepth)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to get merkle path")
	}

	// Get the leaf value.
	leaf, err := uc.merkleTree.GetLeaf(ctx, eventID, leafIndex)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to get merkle leaf")
	}

	return &MerklePathResult{
		MerkleRoot:   root,
		PathElements: pathElements,
		PathIndices:  pathIndices,
		Leaf:         leaf,
	}, nil
}

// BuildMerkleTree builds the Merkle tree for an event from ticket holders.
func (uc *entryUseCase) BuildMerkleTree(ctx context.Context, eventID string) error {
	if eventID == "" {
		return apperr.New(codes.InvalidArgument, "event_id is required")
	}

	// Get all tickets for the event to build identity commitments.
	tickets, err := uc.ticketRepo.ListByEvent(ctx, eventID)
	if err != nil {
		return apperr.Wrap(err, codes.Internal, "failed to list tickets for event")
	}

	// Compute identity commitments for each ticket holder.
	leaves := make([][]byte, len(tickets))
	for i, ticket := range tickets {
		commitment, err := merkle.IdentityCommitment([]byte(ticket.UserID))
		if err != nil {
			return apperr.Wrap(err, codes.Internal, "failed to compute identity commitment",
				slog.String("user_id", ticket.UserID),
			)
		}
		leaves[i] = commitment
	}

	// Build the Merkle tree.
	builder := merkle.NewBuilder(DefaultTreeDepth)
	nodes, root, err := builder.Build(eventID, leaves)
	if err != nil {
		return apperr.Wrap(err, codes.Internal, "failed to build merkle tree")
	}

	// Atomically store all nodes and update the Merkle root in a single
	// transaction to prevent race conditions between concurrent builds.
	if err := uc.merkleTree.StoreBatchWithRoot(ctx, eventID, nodes, root); err != nil {
		return apperr.Wrap(err, codes.Internal, "failed to store merkle tree and root")
	}

	uc.logger.Info(ctx, "merkle tree built",
		slog.String("event_id", eventID),
		slog.Int("num_leaves", len(leaves)),
		slog.String("root", hex.EncodeToString(root)),
	)

	return nil
}
