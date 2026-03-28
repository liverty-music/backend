package entity

// SafePredictor computes deterministic Safe (ERC-4337) wallet addresses
// for users via CREATE2 address prediction.
type SafePredictor interface {
	// AddressHex returns the checksummed hex string of the predicted Safe address
	// for the given internal user ID.
	AddressHex(userID string) string
}
