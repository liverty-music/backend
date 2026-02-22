package merkle

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuilder_Build(t *testing.T) {
	t.Run("single leaf", func(t *testing.T) {
		builder := NewBuilder(2) // depth 2 → 4 leaves
		leaf, err := IdentityCommitment([]byte("user-1"))
		require.NoError(t, err)

		nodes, root, err := builder.Build("event-1", [][]byte{leaf})
		require.NoError(t, err)
		assert.NotNil(t, root)
		assert.Len(t, root, 32)
		// 4 leaves + 2 internal + 1 root = 7 nodes
		assert.Len(t, nodes, 7)
	})

	t.Run("multiple leaves", func(t *testing.T) {
		builder := NewBuilder(2)
		leaves := make([][]byte, 3)
		for i := range leaves {
			var err error
			leaves[i], err = IdentityCommitment([]byte("user-" + string(rune('1'+i))))
			require.NoError(t, err)
		}

		nodes, root, err := builder.Build("event-1", leaves)
		require.NoError(t, err)
		assert.NotNil(t, root)
		assert.Len(t, nodes, 7) // 4 + 2 + 1
	})

	t.Run("full tree", func(t *testing.T) {
		builder := NewBuilder(2)
		leaves := make([][]byte, 4) // exactly 2^2
		for i := range leaves {
			var err error
			leaves[i], err = IdentityCommitment([]byte{byte(i + 1)})
			require.NoError(t, err)
		}

		nodes, root, err := builder.Build("event-1", leaves)
		require.NoError(t, err)
		assert.NotNil(t, root)
		assert.Len(t, nodes, 7)
	})

	t.Run("deterministic output", func(t *testing.T) {
		builder := NewBuilder(2)
		leaf, err := IdentityCommitment([]byte("user-1"))
		require.NoError(t, err)

		_, root1, err := builder.Build("event-1", [][]byte{leaf})
		require.NoError(t, err)

		_, root2, err := builder.Build("event-1", [][]byte{leaf})
		require.NoError(t, err)

		assert.Equal(t, root1, root2, "same leaves should produce the same root")
	})

	t.Run("different leaves produce different roots", func(t *testing.T) {
		builder := NewBuilder(2)
		leaf1, err := IdentityCommitment([]byte("user-1"))
		require.NoError(t, err)
		leaf2, err := IdentityCommitment([]byte("user-2"))
		require.NoError(t, err)

		_, root1, err := builder.Build("event-1", [][]byte{leaf1})
		require.NoError(t, err)
		_, root2, err := builder.Build("event-1", [][]byte{leaf2})
		require.NoError(t, err)

		assert.NotEqual(t, root1, root2)
	})

	t.Run("max depth capped", func(t *testing.T) {
		builder := NewBuilder(25) // exceeds MaxDepth
		assert.Equal(t, MaxDepth, builder.Depth())
	})
}

func TestPoseidonHash(t *testing.T) {
	left := make([]byte, 32)
	left[31] = 1
	right := make([]byte, 32)
	right[31] = 2

	hash, err := PoseidonHash(left, right)
	require.NoError(t, err)
	assert.Len(t, hash, 32)

	// Same inputs should produce same output.
	hash2, err := PoseidonHash(left, right)
	require.NoError(t, err)
	assert.Equal(t, hash, hash2)

	// Different inputs should produce different output.
	right[31] = 3
	hash3, err := PoseidonHash(left, right)
	require.NoError(t, err)
	assert.NotEqual(t, hash, hash3)
}

func TestIdentityCommitment(t *testing.T) {
	commitment1, err := IdentityCommitment([]byte("user-1"))
	require.NoError(t, err)
	assert.Len(t, commitment1, 32)

	// Same input, same output.
	commitment2, err := IdentityCommitment([]byte("user-1"))
	require.NoError(t, err)
	assert.Equal(t, commitment1, commitment2)

	// Different input, different output.
	commitment3, err := IdentityCommitment([]byte("user-2"))
	require.NoError(t, err)
	assert.NotEqual(t, commitment1, commitment3)
}
