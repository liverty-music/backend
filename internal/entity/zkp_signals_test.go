package entity_test

import (
	"encoding/json"
	"fmt"
	"math/big"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validSignalsJSON builds a JSON array of 3 decimal-encoded big.Int strings
// representing [merkleRoot, eventId, nullifierHash].
func validSignalsJSON(t *testing.T, merkleRoot, eventID, nullifier *big.Int) string {
	t.Helper()
	data, err := json.Marshal([]string{
		merkleRoot.String(),
		eventID.String(),
		nullifier.String(),
	})
	require.NoError(t, err)
	return string(data)
}

func TestParseZKPPublicSignals(t *testing.T) {
	t.Parallel()

	// Small values that fit in 32 bytes.
	merkleRoot := big.NewInt(123456789)
	eventID := big.NewInt(987654321)
	nullifier := big.NewInt(111111111)

	type args struct {
		publicSignalsJSON string
	}
	tests := []struct {
		name            string
		args            args
		wantErrContains string // non-empty means an error is expected
	}{
		{
			name: "valid signals parse successfully",
			args: args{publicSignalsJSON: validSignalsJSON(t, merkleRoot, eventID, nullifier)},
		},
		{
			name:            "invalid JSON returns error",
			args:            args{publicSignalsJSON: "not-json"},
			wantErrContains: "unmarshal public signals",
		},
		{
			name:            "fewer than 3 signals returns error",
			args:            args{publicSignalsJSON: `["123", "456"]`},
			wantErrContains: "expected at least 3 public signals",
		},
		{
			name:            "invalid merkle root decimal returns error",
			args:            args{publicSignalsJSON: `["not-a-number", "456", "789"]`},
			wantErrContains: "invalid merkle root",
		},
		{
			name:            "invalid event ID decimal returns error",
			args:            args{publicSignalsJSON: `["123", "not-a-number", "789"]`},
			wantErrContains: "invalid event ID",
		},
		{
			name:            "invalid nullifier hash decimal returns error",
			args:            args{publicSignalsJSON: `["123", "456", "not-a-number"]`},
			wantErrContains: "invalid nullifier hash",
		},
		{
			name:            "empty JSON array returns error",
			args:            args{publicSignalsJSON: `[]`},
			wantErrContains: "expected at least 3 public signals",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := entity.ParseZKPPublicSignals(tt.args.publicSignalsJSON)
			if tt.wantErrContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrContains)
				assert.Nil(t, got)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Len(t, got.MerkleRoot, 32)
			assert.Len(t, got.NullifierHash, 32)
			assert.NotNil(t, got.EventID)
		})
	}
}

func TestParseZKPPublicSignals_RoundTrip(t *testing.T) {
	t.Parallel()

	merkleRoot := big.NewInt(42)
	eventID := big.NewInt(99)
	nullifier := big.NewInt(7)

	got, err := entity.ParseZKPPublicSignals(validSignalsJSON(t, merkleRoot, eventID, nullifier))
	require.NoError(t, err)

	// Verify merkle root round-trips via big.Int.
	recoveredRoot := new(big.Int).SetBytes(got.MerkleRoot)
	assert.Equal(t, merkleRoot, recoveredRoot)

	// Verify event ID preserved.
	assert.Equal(t, 0, got.EventID.Cmp(eventID))

	// Verify nullifier round-trips.
	recoveredNull := new(big.Int).SetBytes(got.NullifierHash)
	assert.Equal(t, nullifier, recoveredNull)
}

func TestZKPPublicSignals_VerifyEventID(t *testing.T) {
	t.Parallel()

	// UUID without hyphens: "550e8400e29b41d4a716446655440000"
	// BigInt of that hex value.
	uuidStr := "550e8400-e29b-41d4-a716-446655440000"
	hexNoHyphens := "550e8400e29b41d4a716446655440000"
	expectedBigInt := new(big.Int)
	expectedBigInt.SetString(hexNoHyphens, 16)

	validSignals := &entity.ZKPPublicSignals{
		EventID: new(big.Int).Set(expectedBigInt),
	}

	mismatchedSignals := &entity.ZKPPublicSignals{
		EventID: big.NewInt(12345),
	}

	tests := []struct {
		name            string
		signals         *entity.ZKPPublicSignals
		expectedUUID    string
		wantErrContains string
	}{
		{
			name:         "matching UUID returns nil error",
			signals:      validSignals,
			expectedUUID: uuidStr,
		},
		{
			name:            "mismatched UUID returns error",
			signals:         mismatchedSignals,
			expectedUUID:    uuidStr,
			wantErrContains: "does not match request event",
		},
		{
			name:            "invalid UUID format returns error",
			signals:         validSignals,
			expectedUUID:    "not-a-uuid",
			wantErrContains: "invalid event UUID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.signals.VerifyEventID(tt.expectedUUID)
			if tt.wantErrContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrContains)
				return
			}

			require.NoError(t, err)
		})
	}
}

func TestBigIntToBytes32(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		input           *big.Int
		label           string
		wantErrContains string
	}{
		{
			name:  "zero converts to 32 zero bytes",
			input: big.NewInt(0),
			label: "test",
		},
		{
			name:  "small value pads to 32 bytes",
			input: big.NewInt(1),
			label: "test",
		},
		{
			name:  "32-byte value succeeds",
			input: new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(1)), // 2^255 - 1
			label: "test",
		},
		{
			name:            "value exceeding 32 bytes returns error",
			input:           new(big.Int).Lsh(big.NewInt(1), 256), // 2^256
			label:           "overflow field",
			wantErrContains: "overflow field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := entity.BigIntToBytes32(tt.input, tt.label)
			if tt.wantErrContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrContains)
				return
			}

			require.NoError(t, err)
			assert.Len(t, got, 32)
			// Round-trip: recover big.Int from bytes and compare.
			recovered := new(big.Int).SetBytes(got)
			assert.Equal(t, 0, recovered.Cmp(tt.input), "round-trip mismatch for %s", tt.input)
		})
	}
}

func TestBytesEqual(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a    []byte
		b    []byte
		want bool
	}{
		{
			name: "identical slices return true",
			a:    []byte{1, 2, 3},
			b:    []byte{1, 2, 3},
			want: true,
		},
		{
			name: "different values return false",
			a:    []byte{1, 2, 3},
			b:    []byte{1, 2, 4},
			want: false,
		},
		{
			name: "different lengths return false",
			a:    []byte{1, 2},
			b:    []byte{1, 2, 3},
			want: false,
		},
		{
			name: "both empty return true",
			a:    []byte{},
			b:    []byte{},
			want: true,
		},
		{
			name: "both nil return true",
			a:    nil,
			b:    nil,
			want: true,
		},
		{
			name: "nil and empty slice are equal",
			a:    nil,
			b:    []byte{},
			want: true,
		},
		{
			name: "32-byte all-zeros are equal",
			a:    make([]byte, 32),
			b:    make([]byte, 32),
			want: true,
		},
		{
			name: "32-byte slices differing in last byte",
			a:    append(make([]byte, 31), 0x00),
			b:    append(make([]byte, 31), 0x01),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := entity.BytesEqual(tt.a, tt.b)
			assert.Equal(t, tt.want, got, fmt.Sprintf("BytesEqual(%v, %v)", tt.a, tt.b))
		})
	}
}
