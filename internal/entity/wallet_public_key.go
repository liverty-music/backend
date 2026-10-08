package entity

import (
	"context"
	"time"
)

// WalletPublicKey is the public key of the device a fan shows tickets from.
// The device keeps the private half (WebCrypto, non-extractable) and signs
// [AdmissionCode]s with it offline; the platform keeps only this public key
// and verifies codes against it. A User has at most one: registering another
// device's key replaces it, so codes from the replaced device are refused.
//
// Mirrors liverty_music.entity.v1.WalletPublicKey.
type WalletPublicKey struct {
	// UserID is the User whose device created the key; one key per User.
	UserID UserID
	// PublicKey is the device's ECDSA P-256 public key.
	PublicKey PublicKey
	// RegisteredTime is when the device registered the key.
	RegisteredTime time.Time
}

// WalletPublicKeyRepository defines the persistence contract for
// [WalletPublicKey]. Implementations live in internal/infrastructure/database/rdb/.
type WalletPublicKeyRepository interface {
	// Register makes key the User's WalletPublicKey with registeredTime,
	// replacing any earlier key in one step so the User never has two. It
	// reports replaced=true only when a different key was replaced.
	// Registering the key the User already has changes nothing (the stored
	// registered time is kept) and reports replaced=false.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the key is not a valid P-256 point (nothing changes).
	//  - Internal: database failure.
	Register(ctx context.Context, userID UserID, key PublicKey, registeredTime time.Time) (registered *WalletPublicKey, replaced bool, err error)

	// GetByUser returns the User's WalletPublicKey.
	//
	// # Possible errors
	//
	//  - NotFound: the User has no key.
	//  - Internal: database failure.
	GetByUser(ctx context.Context, userID UserID) (*WalletPublicKey, error)
}
