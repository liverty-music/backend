package usecase

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"

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

	// Parse public signals once and extract all fields.
	// Public signals order: [merkleRoot, eventId, nullifierHash]
	signals, err := parsePublicSignals(params.PublicSignalsJSON)
	if err != nil {
		return nil, apperr.Wrap(err, codes.InvalidArgument, "failed to parse public signals")
	}

	// Verify that the eventId in the proof matches the request's EventID.
	// This prevents an attacker from submitting a proof generated for a
	// different event, which would produce a different nullifier and bypass
	// double-entry protection.
	eventIDErr := signals.verifyEventID(params.EventID)
	uc.logger.Info(ctx, "entry verification step",
		slog.String("step", "eventID"),
		slog.String("eventID", params.EventID),
		slog.Bool("match", eventIDErr == nil),
	)
	if eventIDErr != nil {
		return nil, apperr.Wrap(eventIDErr, codes.InvalidArgument, "event ID mismatch in public signals")
	}

	nullifierHash := signals.nullifierHash
	merkleRoot := signals.merkleRoot

	expectedRoot, err := uc.eventRepo.GetMerkleRoot(ctx, params.EventID)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Internal, "failed to get expected merkle root")
	}

	rootMatch := bytesEqual(merkleRoot, expectedRoot)
	uc.logger.Info(ctx, "entry verification step",
		slog.String("step", "merkleRoot"),
		slog.String("eventID", params.EventID),
		slog.Bool("match", rootMatch),
	)
	if !rootMatch {
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

	uc.logger.Info(ctx, "entry verification step",
		slog.String("step", "nullifier"),
		slog.String("eventID", params.EventID),
		slog.Bool("isDuplicate", exists),
	)
	if exists {
		uc.logger.Warn(ctx, "duplicate entry attempt",
			slog.String("eventID", params.EventID),
			slog.String("nullifier", hex.EncodeToString(nullifierHash)),
		)
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

// publicSignals holds the parsed public signals from a ZK proof.
// Expected JSON format: ["<merkleRoot>", "<eventId>", "<nullifierHash>"]
type publicSignals struct {
	merkleRoot    []byte
	eventID       *big.Int
	nullifierHash []byte
}

// verifyEventID checks that the eventId in the proof matches the expected UUID.
// The frontend encodes eventId as BigInt(hex(uuid_without_hyphens)).
func (ps *publicSignals) verifyEventID(expectedUUID string) error {
	hex := strings.ReplaceAll(expectedUUID, "-", "")
	expected := new(big.Int)
	if _, ok := expected.SetString(hex, 16); !ok {
		return fmt.Errorf("invalid event UUID: %s", expectedUUID)
	}
	if ps.eventID.Cmp(expected) != 0 {
		return fmt.Errorf("proof eventId %s does not match request event %s", ps.eventID.String(), expectedUUID)
	}
	return nil
}

// parsePublicSignals parses the public signals JSON array once and extracts
// all fields: merkleRoot (index 0), eventId (index 1), nullifierHash (index 2).
func parsePublicSignals(publicSignalsJSON string) (*publicSignals, error) {
	var raw []string
	if err := json.Unmarshal([]byte(publicSignalsJSON), &raw); err != nil {
		return nil, fmt.Errorf("unmarshal public signals: %w", err)
	}

	if len(raw) < 3 {
		return nil, fmt.Errorf("expected at least 3 public signals, got %d", len(raw))
	}

	// Index 0: merkleRoot
	rootInt := new(big.Int)
	if _, ok := rootInt.SetString(raw[0], 10); !ok {
		return nil, fmt.Errorf("invalid merkle root: %s", raw[0])
	}
	merkleRoot, err := bigIntToBytes32(rootInt, "merkle root")
	if err != nil {
		return nil, err
	}

	// Index 1: eventId
	eventID := new(big.Int)
	if _, ok := eventID.SetString(raw[1], 10); !ok {
		return nil, fmt.Errorf("invalid event ID: %s", raw[1])
	}

	// Index 2: nullifierHash
	nullInt := new(big.Int)
	if _, ok := nullInt.SetString(raw[2], 10); !ok {
		return nil, fmt.Errorf("invalid nullifier hash: %s", raw[2])
	}
	nullifierHash, err := bigIntToBytes32(nullInt, "nullifier hash")
	if err != nil {
		return nil, err
	}

	return &publicSignals{
		merkleRoot:    merkleRoot,
		eventID:       eventID,
		nullifierHash: nullifierHash,
	}, nil
}

// bigIntToBytes32 converts a big.Int to a 32-byte big-endian slice.
// Returns an error if the value exceeds 32 bytes (> 2^256-1).
func bigIntToBytes32(n *big.Int, label string) ([]byte, error) {
	b := n.Bytes()
	if len(b) > 32 {
		return nil, fmt.Errorf("%s exceeds 32 bytes (BN254 field element size): got %d bytes", label, len(b))
	}
	buf := make([]byte, 32)
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
