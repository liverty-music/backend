package merkle_test

import (
	"math/big"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/merkle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuilder_Build(t *testing.T) {
	t.Parallel()

	type args struct {
		depth   int
		eventID string
		leaves  func(t *testing.T) [][]byte
	}
	type want struct {
		nodeCount  int
		rootNotNil bool
		rootLen    int
	}
	tests := []struct {
		name  string
		args  args
		want  want
		check func(t *testing.T, builder *merkle.Builder, args args)
	}{
		{
			name: "build tree with single leaf",
			args: args{
				depth:   2,
				eventID: "event-1",
				leaves: func(t *testing.T) [][]byte {
					t.Helper()
					leaf, err := merkle.IdentityCommitment([]byte("user-1"))
					require.NoError(t, err)
					return [][]byte{leaf}
				},
			},
			// depth 2 → 4 leaves + 2 internal + 1 root = 7 nodes
			want: want{nodeCount: 7, rootNotNil: true, rootLen: 32},
		},
		{
			name: "build tree with multiple leaves",
			args: args{
				depth:   2,
				eventID: "event-1",
				leaves: func(t *testing.T) [][]byte {
					t.Helper()
					leaves := make([][]byte, 3)
					for i := range leaves {
						var err error
						leaves[i], err = merkle.IdentityCommitment([]byte("user-" + string(rune('1'+i))))
						require.NoError(t, err)
					}
					return leaves
				},
			},
			// 4 + 2 + 1
			want: want{nodeCount: 7, rootNotNil: true, rootLen: 32},
		},
		{
			name: "build tree when all leaf slots are filled",
			args: args{
				depth:   2,
				eventID: "event-1",
				leaves: func(t *testing.T) [][]byte {
					t.Helper()
					leaves := make([][]byte, 4) // exactly 2^2
					for i := range leaves {
						var err error
						leaves[i], err = merkle.IdentityCommitment([]byte{byte(i + 1)})
						require.NoError(t, err)
					}
					return leaves
				},
			},
			want: want{nodeCount: 7, rootNotNil: true, rootLen: 32},
		},
		{
			name: "produce identical root for identical leaves on repeated builds",
			args: args{
				depth:   2,
				eventID: "event-1",
				leaves: func(t *testing.T) [][]byte {
					t.Helper()
					leaf, err := merkle.IdentityCommitment([]byte("user-1"))
					require.NoError(t, err)
					return [][]byte{leaf}
				},
			},
			want: want{nodeCount: 7, rootNotNil: true, rootLen: 32},
			check: func(t *testing.T, builder *merkle.Builder, args args) {
				t.Helper()
				leaves := args.leaves(t)
				_, root1, err := builder.Build(args.eventID, leaves)
				require.NoError(t, err)
				_, root2, err := builder.Build(args.eventID, leaves)
				require.NoError(t, err)
				assert.Equal(t, root1, root2, "same leaves should produce the same root")
			},
		},
		{
			name: "produce different roots for different leaves",
			args: args{
				depth:   2,
				eventID: "event-1",
				leaves:  func(t *testing.T) [][]byte { return nil },
			},
			check: func(t *testing.T, builder *merkle.Builder, args args) {
				t.Helper()
				leaf1, err := merkle.IdentityCommitment([]byte("user-1"))
				require.NoError(t, err)
				leaf2, err := merkle.IdentityCommitment([]byte("user-2"))
				require.NoError(t, err)

				_, root1, err := builder.Build(args.eventID, [][]byte{leaf1})
				require.NoError(t, err)
				_, root2, err := builder.Build(args.eventID, [][]byte{leaf2})
				require.NoError(t, err)

				assert.NotEqual(t, root1, root2)
			},
		},
		{
			name: "cap depth at MaxDepth when requested depth exceeds maximum",
			args: args{
				depth:  25, // exceeds MaxDepth
				leaves: func(t *testing.T) [][]byte { return nil },
			},
			check: func(t *testing.T, builder *merkle.Builder, _ args) {
				t.Helper()
				assert.Equal(t, merkle.MaxDepth, builder.Depth())
			},
		},
		{
			name: "return error when leaf count exceeds tree capacity",
			args: args{
				depth:   2, // depth 2 → 4 leaves max
				eventID: "event-1",
				leaves:  func(t *testing.T) [][]byte { return nil },
			},
			check: func(t *testing.T, builder *merkle.Builder, args args) {
				t.Helper()
				leaves := make([][]byte, 5)
				for i := range leaves {
					var err error
					leaves[i], err = merkle.IdentityCommitment([]byte{byte(i + 1)})
					require.NoError(t, err)
				}
				_, _, err := builder.Build(args.eventID, leaves)
				assert.ErrorContains(t, err, "too many leaves")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			builder := merkle.NewBuilder(tt.args.depth)

			// Cases with a custom check delegate all assertions there.
			if tt.check != nil {
				tt.check(t, builder, tt.args)
				return
			}

			leaves := tt.args.leaves(t)
			nodes, root, err := builder.Build(tt.args.eventID, leaves)

			assert.NoError(t, err)
			assert.NotNil(t, root)
			assert.Len(t, root, tt.want.rootLen)
			assert.Len(t, nodes, tt.want.nodeCount)
		})
	}
}

func TestPoseidonHash(t *testing.T) {
	t.Parallel()

	left := make([]byte, 32)
	left[31] = 1
	right := make([]byte, 32)
	right[31] = 2

	tests := []struct {
		name        string
		left, right []byte
		check       func(t *testing.T, hash []byte)
	}{
		{
			name:  "return 32-byte hash for valid inputs",
			left:  left,
			right: right,
			check: func(t *testing.T, hash []byte) {
				t.Helper()
				assert.Len(t, hash, 32)
			},
		},
		{
			name:  "return identical hash for identical inputs",
			left:  left,
			right: right,
			check: func(t *testing.T, hash []byte) {
				t.Helper()
				hash2, err := merkle.PoseidonHash(left, right)
				require.NoError(t, err)
				assert.Equal(t, hash, hash2)
			},
		},
		{
			name:  "return different hash when right input changes",
			left:  left,
			right: func() []byte { b := make([]byte, 32); b[31] = 3; return b }(),
			check: func(t *testing.T, hash []byte) {
				t.Helper()
				original, err := merkle.PoseidonHash(left, right)
				require.NoError(t, err)
				assert.NotEqual(t, original, hash)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			hash, err := merkle.PoseidonHash(tt.left, tt.right)
			require.NoError(t, err)
			tt.check(t, hash)
		})
	}
}

func TestIdentityCommitment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input []byte
		check func(t *testing.T, commitment []byte)
	}{
		{
			name:  "return 32-byte commitment for valid input",
			input: []byte("user-1"),
			check: func(t *testing.T, commitment []byte) {
				t.Helper()
				assert.Len(t, commitment, 32)
			},
		},
		{
			name:  "return identical commitment for identical input",
			input: []byte("user-1"),
			check: func(t *testing.T, commitment []byte) {
				t.Helper()
				commitment2, err := merkle.IdentityCommitment([]byte("user-1"))
				require.NoError(t, err)
				assert.Equal(t, commitment, commitment2)
			},
		},
		{
			name:  "return different commitment for different input",
			input: []byte("user-2"),
			check: func(t *testing.T, commitment []byte) {
				t.Helper()
				commitment1, err := merkle.IdentityCommitment([]byte("user-1"))
				require.NoError(t, err)
				assert.NotEqual(t, commitment1, commitment)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			commitment, err := merkle.IdentityCommitment(tt.input)
			require.NoError(t, err)
			tt.check(t, commitment)
		})
	}
}

func TestIdentityCommitment_UUID(t *testing.T) {
	// A 36-byte UUID string exceeds the BN254 field prime (~254 bits).
	// This test verifies that IdentityCommitment handles UUIDs correctly
	// by reducing the input modulo the field prime.
	uuid := []byte("550e8400-e29b-41d4-a716-446655440000") // 36 bytes
	commitment, err := merkle.IdentityCommitment(uuid)
	require.NoError(t, err)
	assert.Len(t, commitment, 32)

	// Same UUID should produce the same commitment.
	commitment2, err := merkle.IdentityCommitment(uuid)
	require.NoError(t, err)
	assert.Equal(t, commitment, commitment2)

	// Different UUID should produce a different commitment.
	uuid2 := []byte("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	commitment3, err := merkle.IdentityCommitment(uuid2)
	require.NoError(t, err)
	assert.NotEqual(t, commitment, commitment3)
}

func TestToFieldElement(t *testing.T) {
	// Verify that values exceeding the BN254 prime are reduced.
	large := new(big.Int).SetBytes([]byte("550e8400-e29b-41d4-a716-446655440000"))
	reduced := merkle.ToFieldElement(large)

	// The reduced value must be less than the BN254 prime.
	assert.True(t, reduced.Cmp(merkle.BN254Prime) < 0, "reduced value should be less than BN254 prime")

	// A small value should remain unchanged.
	small := big.NewInt(42)
	assert.Equal(t, small, merkle.ToFieldElement(small))
}

func TestPoseidonHash_LargeInputs(t *testing.T) {
	// Inputs exceeding the BN254 field prime should be reduced and still work.
	left := []byte("550e8400-e29b-41d4-a716-446655440000")  // 36 bytes
	right := []byte("6ba7b810-9dad-11d1-80b4-00c04fd430c8") // 36 bytes

	hash, err := merkle.PoseidonHash(left, right)
	require.NoError(t, err)
	assert.Len(t, hash, 32)

	// Deterministic.
	hash2, err := merkle.PoseidonHash(left, right)
	require.NoError(t, err)
	assert.Equal(t, hash, hash2)
}
