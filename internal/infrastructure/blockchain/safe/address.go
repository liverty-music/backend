// Package safe provides Safe (ERC-4337) address derivation utilities.
//
// Safe addresses are predicted deterministically from a user's internal ID using
// CREATE2, making them auth-provider-agnostic: if the auth system changes, the
// predicted address for a given users.id remains unchanged.
package safe

import (
	"encoding/hex"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// SafeProxyFactory is the canonical Safe{Wallet} ProxyFactory contract address on all EVM chains.
// https://github.com/safe-global/safe-deployments
const SafeProxyFactory = "0x4e1DCf7AD4e460CfD30791CCC4F9c8a4f820ec67"

// safeProxyInitCodeHash is keccak256(SafeProxy creation bytecode ++ abi.encode(Safe v1.4.1 singleton)).
//
// Computed as:
//
//	keccak256(proxyCreationCode ++ uint256(Safe_v1.4.1_singleton))
//
// where proxyCreationCode is the bytecode of the SafeProxy contract and
// Safe v1.4.1 singleton address is 0x41675C099F32341bf84BFc5382aF534df5C7461a.
//
// This value is chain-agnostic for the canonical Safe v1.4.1 deployment.
// Source: https://github.com/safe-global/safe-deployments/blob/main/src/assets/v1.4.1/
var safeProxyInitCodeHash = common.HexToHash("0x52bede2892dc6ee239117844c91b0bdd458c318980592ab4152f5ea44af17f34")

// PredictAddress computes the CREATE2 address for a user's Safe.
//
// The salt is keccak256(userID), where userID is the internal users.id (UUIDv7 string).
// This ensures the Safe address is tied to the auth-agnostic internal identifier, not to
// any identity-provider-specific value like external_id or a WebAuthn credential public key.
//
// Formula: CREATE2(deployer=SafeProxyFactory, salt=keccak256(userID), initCodeHash=safeProxyInitCodeHash)
func PredictAddress(userID string) common.Address {
	salt := crypto.Keccak256Hash([]byte(userID))
	return create2Address(common.HexToAddress(SafeProxyFactory), salt, safeProxyInitCodeHash)
}

// create2Address computes the EVM CREATE2 address.
// address = keccak256(0xff ++ deployer ++ salt ++ initCodeHash)[12:]
func create2Address(deployer common.Address, salt, initCodeHash common.Hash) common.Address {
	input := make([]byte, 1+20+32+32)
	input[0] = 0xff
	copy(input[1:21], deployer.Bytes())
	copy(input[21:53], salt.Bytes())
	copy(input[53:85], initCodeHash.Bytes())

	hash := crypto.Keccak256Hash(input)
	return common.BytesToAddress(hash.Bytes()[12:])
}

// AddressHex returns the checksummed hex string of the predicted Safe address.
func AddressHex(userID string) string {
	addr := PredictAddress(userID)
	return addr.Hex()
}

// AddressBytes returns the 20-byte address as a lowercase hex string without "0x" prefix.
// Suitable for storage in the database TEXT column (safe_address).
func AddressBytes(userID string) string {
	addr := PredictAddress(userID)
	return strings.ToLower(hex.EncodeToString(addr.Bytes()))
}
