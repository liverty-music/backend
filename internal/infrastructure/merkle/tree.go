package merkle

import (
	"fmt"

	"github.com/liverty-music/backend/internal/entity"
)

// MaxDepth is the maximum supported tree depth.
const MaxDepth = 20

// Builder constructs a Merkle tree from a list of leaves using Poseidon hash.
type Builder struct {
	depth int
}

// NewBuilder creates a new Merkle tree builder with the specified depth.
// The tree can hold up to 2^depth leaves.
func NewBuilder(depth int) *Builder {
	if depth > MaxDepth {
		depth = MaxDepth
	}
	return &Builder{depth: depth}
}

// Build constructs a full Merkle tree from the given leaves.
// Empty leaf positions are filled with a zero hash.
// Returns all nodes (including leaves) and the root hash.
func (b *Builder) Build(eventID string, leaves [][]byte) ([]*entity.MerkleNode, []byte, error) {
	numLeaves := 1 << b.depth

	// Pad leaves with zero hashes if necessary.
	paddedLeaves := make([][]byte, numLeaves)
	zeroLeaf := make([]byte, 32)
	for i := range paddedLeaves {
		if i < len(leaves) {
			paddedLeaves[i] = leaves[i]
		} else {
			paddedLeaves[i] = zeroLeaf
		}
	}

	var nodes []*entity.MerkleNode

	// Store leaf nodes at depth 0.
	for i, leaf := range paddedLeaves {
		nodes = append(nodes, &entity.MerkleNode{
			EventID:   eventID,
			Depth:     0,
			NodeIndex: i,
			Hash:      leaf,
		})
	}

	// Build tree bottom-up.
	currentLevel := paddedLeaves
	for depth := 1; depth <= b.depth; depth++ {
		nextLevel := make([][]byte, len(currentLevel)/2)
		for i := 0; i < len(currentLevel); i += 2 {
			hash, err := PoseidonHash(currentLevel[i], currentLevel[i+1])
			if err != nil {
				return nil, nil, fmt.Errorf("hash at depth %d, index %d: %w", depth, i/2, err)
			}
			nextLevel[i/2] = hash
			nodes = append(nodes, &entity.MerkleNode{
				EventID:   eventID,
				Depth:     depth,
				NodeIndex: i / 2,
				Hash:      hash,
			})
		}
		currentLevel = nextLevel
	}

	root := currentLevel[0]
	return nodes, root, nil
}

// Depth returns the depth of the tree.
func (b *Builder) Depth() int {
	return b.depth
}
