package entity

// MerkleTreeBuilder constructs Merkle trees from identity commitments.
// Implementations handle the hash function (e.g., Poseidon) and tree layout.
type MerkleTreeBuilder interface {
	// IdentityCommitment computes the identity commitment for a user ID.
	// The commitment is a 32-byte hash used as a Merkle tree leaf.
	IdentityCommitment(userID []byte) ([]byte, error)

	// Build constructs a full Merkle tree from the given leaves.
	// Empty positions are filled with a zero hash. Returns all nodes
	// (including leaves) and the root hash.
	Build(eventID string, leaves [][]byte) ([]*MerkleNode, []byte, error)
}
