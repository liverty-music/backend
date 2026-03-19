package safe_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/blockchain/safe"
	"github.com/stretchr/testify/assert"
)

func TestPredictAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args struct {
			userID string
		}
		// want holds the expected AddressHex result; empty string means no specific value check.
		want    string
		wantLen int
		// checkUniqueness, when true, asserts the result differs from the second call with altUserID.
		checkDeterminism  bool
		checkUniqueness   bool
		checkOutputFormat bool
		altUserID         string
	}{
		{
			name: "return same address for the same user ID",
			args: struct {
				userID string
			}{userID: "01952d3a-b1c2-7e4f-a5b6-c7d8e9f0a1b2"},
			checkDeterminism: true,
		},
		{
			name: "return different addresses for different user IDs",
			args: struct {
				userID string
			}{userID: "01952d3a-b1c2-7e4f-a5b6-c7d8e9f0a1b2"},
			checkUniqueness: true,
			altUserID:       "01952d3a-0000-7000-8000-000000000001",
		},
		{
			name: "return 0x-prefixed 42-character hex string",
			args: struct {
				userID string
			}{userID: "01952d3a-b1c2-7e4f-a5b6-c7d8e9f0a1b2"},
			checkOutputFormat: true,
		},
		{
			// Regression guard: if the derivation formula changes, this test fails.
			// Expected value computed from Safe v1.4.1 canonical deployment:
			//   CREATE2(deployer=0x4e1DCf7AD4e460CfD30791CCC4F9c8a4f820ec67,
			//           salt=keccak256("00000000-0000-7000-8000-000000000001"),
			//           initCodeHash=keccak256(safeProxyCreationCode++singleton_v1.4.1))
			// Update this expected value only after an intentional formula change + audit.
			name: "return known address for canonical test vector",
			args: struct {
				userID string
			}{userID: "00000000-0000-7000-8000-000000000001"},
			want: "0x0197d0bFbF831238B1b81C2166D2c79B419B3342",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			switch {
			case tt.checkDeterminism:
				addr1 := safe.PredictAddress(tt.args.userID)
				addr2 := safe.PredictAddress(tt.args.userID)
				assert.Equal(t, addr1, addr2, "PredictAddress should be deterministic")

			case tt.checkUniqueness:
				addr1 := safe.PredictAddress(tt.args.userID)
				addr2 := safe.PredictAddress(tt.altUserID)
				assert.NotEqual(t, addr1, addr2, "different user IDs should produce different Safe addresses")

			case tt.checkOutputFormat:
				hex := safe.AddressHex(tt.args.userID)
				assert.Equal(t, 42, len(hex), "AddressHex should be 42 characters (0x + 40 hex chars)")
				assert.Equal(t, "0x", hex[:2], "AddressHex should have 0x prefix")

				raw := safe.AddressBytes(tt.args.userID)
				assert.Equal(t, 40, len(raw), "AddressBytes should be 40 hex chars")

			default:
				got := safe.AddressHex(tt.args.userID)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}
