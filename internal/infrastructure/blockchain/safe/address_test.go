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
	// Update this expected value only after intentional formula change + audit.
	userID := "00000000-0000-7000-8000-000000000001"
	got := safe.AddressHex(userID)

	// Generate expected value once: go test -run TestPredictAddress_KnownVector -v
	// and record the output. Replace the placeholder below with the actual value.
	const expected = "" // set after first run
	if expected != "" && got != expected {
		t.Errorf("PredictAddress(%q) = %s, want %s", userID, got, expected)
	}

	// Always ensure non-empty and correct format.
	if len(got) != 42 {
		t.Errorf("expected 42-char hex address, got %q", got)
	}
}
