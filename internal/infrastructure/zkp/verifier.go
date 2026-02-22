package zkp

import (
	"fmt"
	"os"
	"sync"

	"github.com/vocdoni/circom2gnark/parser"
)

// Verifier wraps gnark Groth16 verification with circom2gnark format conversion.
// The verification key is loaded once at startup and cached for repeated proof
// verifications. This avoids re-parsing the verification key on every request.
type Verifier struct {
	vk *parser.CircomVerificationKey
	mu sync.RWMutex
}

// NewVerifier creates a Verifier by loading a snarkjs verification key from a file.
// vkPath is the path to the verification_key.json file exported by snarkjs.
func NewVerifier(vkPath string) (*Verifier, error) {
	data, err := os.ReadFile(vkPath)
	if err != nil {
		return nil, fmt.Errorf("read verification key: %w", err)
	}

	return NewVerifierFromBytes(data)
}

// NewVerifierFromBytes creates a Verifier from raw verification key JSON bytes.
func NewVerifierFromBytes(vkJSON []byte) (*Verifier, error) {
	vk, err := parser.UnmarshalCircomVerificationKeyJSON(vkJSON)
	if err != nil {
		return nil, fmt.Errorf("unmarshal verification key: %w", err)
	}

	return &Verifier{vk: vk}, nil
}

// Verify checks a Groth16 proof against the cached verification key.
// proofJSON is the snarkjs-format proof JSON string.
// publicSignalsJSON is the JSON array of public signal strings.
func (v *Verifier) Verify(proofJSON string, publicSignalsJSON string) (bool, error) {
	v.mu.RLock()
	vk := v.vk
	v.mu.RUnlock()

	proof, err := parser.UnmarshalCircomProofJSON([]byte(proofJSON))
	if err != nil {
		return false, fmt.Errorf("unmarshal proof: %w", err)
	}

	signals, err := parser.UnmarshalCircomPublicSignalsJSON([]byte(publicSignalsJSON))
	if err != nil {
		return false, fmt.Errorf("unmarshal public signals: %w", err)
	}

	gnarkProof, err := parser.ConvertCircomToGnark(proof, vk, signals)
	if err != nil {
		return false, fmt.Errorf("convert to gnark format: %w", err)
	}

	verified, err := parser.VerifyProof(gnarkProof)
	if err != nil {
		// Verification failure (invalid proof) is not an error; return false.
		return false, nil
	}

	return verified, nil
}
