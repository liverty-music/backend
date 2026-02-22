package merkle

import (
	"fmt"
	"math/big"

	"github.com/iden3/go-iden3-crypto/poseidon"
)

// PoseidonHash computes the Poseidon hash of two field elements.
// This is compatible with circomlib's Poseidon implementation used in the
// TicketCheck circuit, ensuring the backend-built Merkle tree matches
// the circuit's expectations.
func PoseidonHash(left, right []byte) ([]byte, error) {
	l := new(big.Int).SetBytes(left)
	r := new(big.Int).SetBytes(right)

	result, err := poseidon.Hash([]*big.Int{l, r})
	if err != nil {
		return nil, fmt.Errorf("poseidon hash: %w", err)
	}

	// Pad to 32 bytes (BN254 field element size).
	buf := make([]byte, 32)
	b := result.Bytes()
	copy(buf[32-len(b):], b)
	return buf, nil
}

// IdentityCommitment computes the Poseidon hash of a user ID (as bytes).
// This serves as the leaf value in the Merkle tree.
// Format: Poseidon(userIDAsFieldElement)
func IdentityCommitment(userIDBytes []byte) ([]byte, error) {
	input := new(big.Int).SetBytes(userIDBytes)

	result, err := poseidon.Hash([]*big.Int{input})
	if err != nil {
		return nil, fmt.Errorf("identity commitment: %w", err)
	}

	buf := make([]byte, 32)
	b := result.Bytes()
	copy(buf[32-len(b):], b)
	return buf, nil
}
