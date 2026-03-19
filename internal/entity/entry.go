package entity

import (
	"context"
	"time"
)

// Nullifier represents a spent nullifier hash for an event entry.
// Each nullifier can only be used once per event (double-entry guard).
type Nullifier struct {
	// EventID is the event for which this nullifier was spent.
	EventID string
	// NullifierHash is the hash value derived from the ZKP circuit.
	NullifierHash []byte
	// UseTime is the timestamp when this nullifier was recorded.
	UseTime time.Time
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
	//
	// # Possible errors
	//
	//   - InvalidArgument: eventID or nullifierHash is empty.
	//   - AlreadyExists: the nullifier has already been used for this event.
	//   - Internal: database execution failure.
	Insert(ctx context.Context, eventID string, nullifierHash []byte) error

	// Exists checks if a nullifier hash has already been used for an event.
	//
	// # Possible errors
	//
	//   - InvalidArgument: eventID or nullifierHash is empty.
	//   - Internal: database query failure.
	Exists(ctx context.Context, eventID string, nullifierHash []byte) (bool, error)
}

// MerkleTreeRepository defines the interface for Merkle tree data access.
type MerkleTreeRepository interface {
	// StoreBatch inserts or replaces all nodes for an event's Merkle tree.
	// This is called when the tree is (re)built.
	//
	// # Possible errors
	//
	//   - InvalidArgument: eventID is empty.
	//   - Internal: database execution failure.
	StoreBatch(ctx context.Context, eventID string, nodes []*MerkleNode) error

	// StoreBatchWithRoot atomically stores all Merkle tree nodes and updates
	// the event's Merkle root in a single transaction.
	//
	// # Possible errors
	//
	//   - InvalidArgument: eventID is empty.
	//   - NotFound: event does not exist.
	//   - Internal: database execution failure.
	StoreBatchWithRoot(ctx context.Context, eventID string, nodes []*MerkleNode, root []byte) error

	// GetPath retrieves the Merkle path (sibling hashes and indices) for a
	// leaf at the given index for the specified event.
	//
	// # Possible errors
	//
	//   - InvalidArgument: eventID is empty.
	//   - NotFound: no node exists at the specified leaf index.
	//   - Internal: database query failure.
	GetPath(ctx context.Context, eventID string, leafIndex int, treeDepth int) (pathElements [][]byte, pathIndices []uint32, err error)

	// GetRoot retrieves the Merkle root hash for an event.
	//
	// # Possible errors
	//
	//   - InvalidArgument: eventID is empty.
	//   - NotFound: event has no Merkle root.
	//   - Internal: database query failure.
	GetRoot(ctx context.Context, eventID string) ([]byte, error)

	// GetLeaf retrieves the leaf hash at the given index for an event.
	//
	// # Possible errors
	//
	//   - InvalidArgument: eventID is empty.
	//   - NotFound: no leaf exists at the specified index.
	//   - Internal: database query failure.
	GetLeaf(ctx context.Context, eventID string, leafIndex int) ([]byte, error)
}

// ZKPVerifier defines the interface for zero-knowledge proof verification.
type ZKPVerifier interface {
	// Verify checks a Groth16 proof against the loaded verification key.
	// proofJSON is the snarkjs-format proof JSON string.
	// publicSignalsJSON is the JSON array of public signals.
	// Returns true if the proof is valid, false otherwise.
	// Returns (false, nil) when the proof is cryptographically invalid but well-formed.
	//
	// # Possible errors
	//
	//   - Internal: proof or public signals JSON is malformed or conversion failed.
	Verify(proofJSON string, publicSignalsJSON string) (bool, error)
}

// EventRepository defines the interface for event-related data access
// needed by the entry system.
type EventRepository interface {
	// GetMerkleRoot retrieves the Merkle root for an event.
	//
	// # Possible errors
	//
	//   - InvalidArgument: eventID is empty.
	//   - NotFound: event does not exist or has no Merkle root.
	//   - Internal: database query failure.
	GetMerkleRoot(ctx context.Context, eventID string) ([]byte, error)

	// UpdateMerkleRoot sets the Merkle root for an event.
	//
	// # Possible errors
	//
	//   - InvalidArgument: eventID is empty.
	//   - NotFound: event does not exist.
	//   - Internal: database execution failure.
	UpdateMerkleRoot(ctx context.Context, eventID string, root []byte) error

	// GetTicketLeafIndex returns the leaf index in the Merkle tree for a user's
	// ticket at a given event. Returns -1 if the user has no ticket.
	//
	// # Possible errors
	//
	//   - InvalidArgument: eventID or userID is empty.
	//   - NotFound: user has no ticket for this event.
	//   - Internal: database query failure.
	GetTicketLeafIndex(ctx context.Context, eventID, userID string) (int, error)
}
