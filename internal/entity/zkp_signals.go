package entity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// ZKPPublicSignals holds the parsed public signals from a ZK proof.
// All values are BN254 field elements encoded as decimal strings.
//
// Expected JSON input format (index order matters):
//
//	["<merkleRoot>", "<eventId>", "<nullifierHash>"]
type ZKPPublicSignals struct {
	// MerkleRoot is the Merkle root of the event membership set (index 0).
	MerkleRoot []byte
	// EventID is the event UUID encoded as BigInt(hex(uuid_without_hyphens)) (index 1).
	EventID *big.Int
	// NullifierHash prevents double-entry for a given (identity, event) pair (index 2).
	NullifierHash []byte
}

// VerifyEventID checks that the EventID in the proof matches the expected UUID.
// The frontend encodes eventId as BigInt(hex(uuid_without_hyphens)).
func (s *ZKPPublicSignals) VerifyEventID(expectedUUID string) error {
	hex := strings.ReplaceAll(expectedUUID, "-", "")
	expected := new(big.Int)
	if _, ok := expected.SetString(hex, 16); !ok {
		return fmt.Errorf("invalid event UUID: %s", expectedUUID)
	}
	if s.EventID.Cmp(expected) != 0 {
		return fmt.Errorf("proof eventId %s does not match request event %s", s.EventID.String(), expectedUUID)
	}
	return nil
}

// ParseZKPPublicSignals parses the public signals JSON array and extracts all fields:
// merkleRoot (index 0), eventId (index 1), nullifierHash (index 2).
func ParseZKPPublicSignals(publicSignalsJSON string) (*ZKPPublicSignals, error) {
	var raw []string
	if err := json.Unmarshal([]byte(publicSignalsJSON), &raw); err != nil {
		return nil, fmt.Errorf("unmarshal public signals: %w", err)
	}

	if len(raw) < 3 {
		return nil, fmt.Errorf("expected at least 3 public signals, got %d", len(raw))
	}

	// Index 0: merkleRoot
	rootInt := new(big.Int)
	if _, ok := rootInt.SetString(raw[0], 10); !ok {
		return nil, fmt.Errorf("invalid merkle root: %s", raw[0])
	}
	merkleRoot, err := BigIntToBytes32(rootInt, "merkle root")
	if err != nil {
		return nil, err
	}

	// Index 1: eventId
	eventID := new(big.Int)
	if _, ok := eventID.SetString(raw[1], 10); !ok {
		return nil, fmt.Errorf("invalid event ID: %s", raw[1])
	}

	// Index 2: nullifierHash
	nullInt := new(big.Int)
	if _, ok := nullInt.SetString(raw[2], 10); !ok {
		return nil, fmt.Errorf("invalid nullifier hash: %s", raw[2])
	}
	nullifierHash, err := BigIntToBytes32(nullInt, "nullifier hash")
	if err != nil {
		return nil, err
	}

	return &ZKPPublicSignals{
		MerkleRoot:    merkleRoot,
		EventID:       eventID,
		NullifierHash: nullifierHash,
	}, nil
}

// BigIntToBytes32 converts a big.Int to a 32-byte big-endian slice.
// Returns an error if the value exceeds 32 bytes (> 2^256-1, outside BN254 field).
func BigIntToBytes32(n *big.Int, label string) ([]byte, error) {
	b := n.Bytes()
	if len(b) > 32 {
		return nil, fmt.Errorf("%s exceeds 32 bytes (BN254 field element size): got %d bytes", label, len(b))
	}
	buf := make([]byte, 32)
	copy(buf[32-len(b):], b)
	return buf, nil
}

// BytesEqual reports whether a and b contain identical bytes.
// It is a thin wrapper around [bytes.Equal] exposed for use in
// domain verification logic (e.g., Merkle root comparison).
func BytesEqual(a, b []byte) bool {
	return bytes.Equal(a, b)
}
