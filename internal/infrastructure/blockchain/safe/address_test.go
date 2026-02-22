package safe_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/blockchain/safe"
)

func TestPredictAddress_Determinism(t *testing.T) {
	t.Parallel()

	userID := "01952d3a-b1c2-7e4f-a5b6-c7d8e9f0a1b2" // example UUIDv7

	addr1 := safe.PredictAddress(userID)
	addr2 := safe.PredictAddress(userID)

	if addr1 != addr2 {
		t.Errorf("PredictAddress not deterministic: got %s and %s", addr1.Hex(), addr2.Hex())
	}
}

func TestPredictAddress_DifferentIDsProduceDifferentAddresses(t *testing.T) {
	t.Parallel()

	addr1 := safe.PredictAddress("01952d3a-b1c2-7e4f-a5b6-c7d8e9f0a1b2")
	addr2 := safe.PredictAddress("01952d3a-0000-7000-8000-000000000001")

	if addr1 == addr2 {
		t.Errorf("Different user IDs produced the same Safe address: %s", addr1.Hex())
	}
}

func TestPredictAddress_OutputFormat(t *testing.T) {
	t.Parallel()

	userID := "01952d3a-b1c2-7e4f-a5b6-c7d8e9f0a1b2"
	hex := safe.AddressHex(userID)

	if len(hex) != 42 {
		t.Errorf("AddressHex length = %d, want 42 (0x + 40 chars); got %q", len(hex), hex)
	}

	if hex[:2] != "0x" {
		t.Errorf("AddressHex missing 0x prefix: %q", hex)
	}

	raw := safe.AddressBytes(userID)
	if len(raw) != 40 {
		t.Errorf("AddressBytes length = %d, want 40 hex chars; got %q", len(raw), raw)
	}
}

func TestPredictAddress_KnownVector(t *testing.T) {
	t.Parallel()

	// Regression guard: if the derivation formula changes, this test fails.
	// Expected value computed from Safe v1.4.1 canonical deployment:
	//   CREATE2(deployer=0x4e1DCf7AD4e460CfD30791CCC4F9c8a4f820ec67,
	//           salt=keccak256("00000000-0000-7000-8000-000000000001"),
	//           initCodeHash=keccak256(safeProxyCreationCode++singleton_v1.4.1))
	// Update this expected value only after an intentional formula change + audit.
	userID := "00000000-0000-7000-8000-000000000001"
	got := safe.AddressHex(userID)

	const expected = "0x0197d0bFbF831238B1b81C2166D2c79B419B3342"
	if got != expected {
		t.Errorf("PredictAddress(%q) = %s, want %s", userID, got, expected)
	}
}
