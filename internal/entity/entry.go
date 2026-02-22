package entity

import (
	"context"
	"time"
)

// Nullifier represents a spent nullifier hash for an event entry.
// Each nullifier can only be used once per event (double-entry guard).
type Nullifier struct {
	// ID is the unique identifier for the nullifier record.
	ID string
	// EventID is the event for which this nullifier was spent.
	EventID string
	// NullifierHash is the hash value derived from the ZKP circuit.
	NullifierHash []byte
	// UsedAt is the timestamp when this nullifier was recorded.
	UsedAt time.Time
}

// MerkleNode represents a single node in the Merkle tree stored in the database.
type MerkleNode struct {
	// EventID is the event this node belongs to.
	EventID string
	// Depth is the level in the tree (0 = leaf level).
	Depth int
	// NodeIndex is the index of the node at this depth.
	NodeIndex int
	// Hash is the Poseidon hash value of this node.
	Hash []byte
}

// NullifierRepository defines the interface for nullifier data access.
type NullifierRepository interface {
	// Insert atomically inserts a nullifier hash for an event.
	// Returns AlreadyExists if the nullifier has already been used for this event.
	Insert(ctx context.Context, eventID string, nullifierHash []byte) error

	// Exists checks if a nullifier hash has already been used for an event.
	Exists(ctx context.Context, eventID string, nullifierHash []byte) (bool, error)
}

// MerkleTreeRepository defines the interface for Merkle tree data access.
type MerkleTreeRepository interface {
	// StoreBatch inserts or replaces all nodes for an event's Merkle tree.
	// This is called when the tree is (re)built.
	StoreBatch(ctx context.Context, eventID string, nodes []*MerkleNode) error

	// GetPath retrieves the Merkle path (sibling hashes and indices) for a
	// leaf at the given index for the specified event.
	GetPath(ctx context.Context, eventID string, leafIndex int, treeDepth int) (pathElements [][]byte, pathIndices []uint32, err error)

	// GetRoot retrieves the Merkle root hash for an event.
	GetRoot(ctx context.Context, eventID string) ([]byte, error)

	// GetLeaf retrieves the leaf hash at the given index for an event.
	GetLeaf(ctx context.Context, eventID string, leafIndex int) ([]byte, error)
}

// ZKPVerifier defines the interface for zero-knowledge proof verification.
type ZKPVerifier interface {
	// Verify checks a Groth16 proof against the loaded verification key.
	// proofJSON is the snarkjs-format proof JSON string.
	// publicSignalsJSON is the JSON array of public signals.
	// Returns true if the proof is valid, false otherwise.
	Verify(proofJSON string, publicSignalsJSON string) (bool, error)
}

// EventRepository defines the interface for event-related data access
// needed by the entry system.
type EventRepository interface {
	// GetMerkleRoot retrieves the Merkle root for an event.
	GetMerkleRoot(ctx context.Context, eventID string) ([]byte, error)

	// UpdateMerkleRoot sets the Merkle root for an event.
	UpdateMerkleRoot(ctx context.Context, eventID string, root []byte) error

	// GetTicketLeafIndex returns the leaf index in the Merkle tree for a user's
	// ticket at a given event. Returns -1 if the user has no ticket.
	GetTicketLeafIndex(ctx context.Context, eventID, userID string) (int, error)
}
