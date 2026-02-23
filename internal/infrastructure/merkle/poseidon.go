package merkle

import (
	"fmt"
	"math/big"

	"github.com/iden3/go-iden3-crypto/poseidon"
)

// bn254Prime is the BN254 scalar field prime.
// All Poseidon inputs must be reduced modulo this value.
var bn254Prime, _ = new(big.Int).SetString(
	"21888242871839275222246405745257275088548364400416034343698204186575808495617", 10,
)

// toFieldElement reduces a big.Int modulo the BN254 field prime.
func toFieldElement(v *big.Int) *big.Int {
	return new(big.Int).Mod(v, bn254Prime)
}

// PoseidonHash computes the Poseidon hash of two field elements.
//
// Compatibility note: both iden3/go-iden3-crypto/poseidon (Go) and
// circomlib/circuits/poseidon.circom (circuit) use the same Poseidon
// parameters over the BN254 scalar field:
//   - Field prime: 21888242871839275222246405745257275088548364400416034343698204186575808495617
//   - t = nInputs + 1 (width), full/partial rounds per the Poseidon paper
//   - Same MDS matrix and round constants from the iden3 reference implementation
//
// This ensures the backend-built Merkle tree matches the circuit's expectations.
func PoseidonHash(left, right []byte) ([]byte, error) {
	l := toFieldElement(new(big.Int).SetBytes(left))
	r := toFieldElement(new(big.Int).SetBytes(right))

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
// The input is reduced modulo the BN254 field prime to ensure it fits
// within the field, since UUIDs (36 bytes) exceed the ~254-bit prime.
// Format: Poseidon(userID mod p)
func IdentityCommitment(userIDBytes []byte) ([]byte, error) {
	input := toFieldElement(new(big.Int).SetBytes(userIDBytes))

	result, err := poseidon.Hash([]*big.Int{input})
	if err != nil {
		return nil, fmt.Errorf("identity commitment: %w", err)
	}

	buf := make([]byte, 32)
	b := result.Bytes()
	copy(buf[32-len(b):], b)
	return buf, nil
}
