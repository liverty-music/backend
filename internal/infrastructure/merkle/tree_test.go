package merkle

import (
	"math/big"
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

func TestIdentityCommitment_UUID(t *testing.T) {
	// A 36-byte UUID string exceeds the BN254 field prime (~254 bits).
	// This test verifies that IdentityCommitment handles UUIDs correctly
	// by reducing the input modulo the field prime.
	uuid := []byte("550e8400-e29b-41d4-a716-446655440000") // 36 bytes
	commitment, err := IdentityCommitment(uuid)
	require.NoError(t, err)
	assert.Len(t, commitment, 32)

	// Same UUID should produce the same commitment.
	commitment2, err := IdentityCommitment(uuid)
	require.NoError(t, err)
	assert.Equal(t, commitment, commitment2)

	// Different UUID should produce a different commitment.
	uuid2 := []byte("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	commitment3, err := IdentityCommitment(uuid2)
	require.NoError(t, err)
	assert.NotEqual(t, commitment, commitment3)
}

func TestToFieldElement(t *testing.T) {
	// Verify that values exceeding the BN254 prime are reduced.
	large := new(big.Int).SetBytes([]byte("550e8400-e29b-41d4-a716-446655440000"))
	reduced := toFieldElement(large)

	// The reduced value must be less than the BN254 prime.
	assert.True(t, reduced.Cmp(bn254Prime) < 0, "reduced value should be less than BN254 prime")

	// A small value should remain unchanged.
	small := big.NewInt(42)
	assert.Equal(t, small, toFieldElement(small))
}

func TestPoseidonHash_LargeInputs(t *testing.T) {
	// Inputs exceeding the BN254 field prime should be reduced and still work.
	left := []byte("550e8400-e29b-41d4-a716-446655440000")  // 36 bytes
	right := []byte("6ba7b810-9dad-11d1-80b4-00c04fd430c8") // 36 bytes

	hash, err := PoseidonHash(left, right)
	require.NoError(t, err)
	assert.Len(t, hash, 32)

	// Deterministic.
	hash2, err := PoseidonHash(left, right)
	require.NoError(t, err)
	assert.Equal(t, hash, hash2)
}
