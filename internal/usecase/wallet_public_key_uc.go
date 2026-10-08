package usecase

import (
	"context"
	"time"

	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/entity"
)

// RegisterWalletPublicKeyResult is what [WalletPublicKeyUseCase.Register]
// returns.
type RegisterWalletPublicKeyResult struct {
	// Key is the fan's WalletPublicKey with its registered time.
	Key *entity.WalletPublicKey
	// ReplacedOtherKey is true when a different device's key was replaced, so
	// the tickets screen can say tickets are now shown from this device only.
	ReplacedOtherKey bool
}

// WalletPublicKeyUseCase records the public key of the key pair the calling
// fan's device created for showing tickets.
type WalletPublicKeyUseCase interface {
	// Register makes key the fan's only WalletPublicKey at now. Codes signed on
	// a device whose key is replaced are refused at the venue from then on.
	// It requires nothing beyond the fan's existing sign-in.
	//
	// # Possible errors
	//
	//  - InvalidArgument: key is not a valid P-256 key (the fan's key is
	//    unchanged).
	//  - Internal: database failure.
	Register(ctx context.Context, userID entity.UserID, key entity.PublicKey, now time.Time) (*RegisterWalletPublicKeyResult, error)
}

// walletPublicKeyUseCase implements [WalletPublicKeyUseCase].
type walletPublicKeyUseCase struct {
	keys   entity.WalletPublicKeyRepository
	logger *logging.Logger
}

// Compile-time interface compliance check.
var _ WalletPublicKeyUseCase = (*walletPublicKeyUseCase)(nil)

// NewWalletPublicKeyUseCase constructs a WalletPublicKeyUseCase. All
// parameters are required.
func NewWalletPublicKeyUseCase(keys entity.WalletPublicKeyRepository, logger *logging.Logger) WalletPublicKeyUseCase {
	return &walletPublicKeyUseCase{keys: keys, logger: logger}
}

// Register implements [WalletPublicKeyUseCase].
func (uc *walletPublicKeyUseCase) Register(ctx context.Context, userID entity.UserID, key entity.PublicKey, now time.Time) (*RegisterWalletPublicKeyResult, error) {
	registered, replaced, err := uc.keys.Register(ctx, userID, key, now)
	if err != nil {
		return nil, err
	}
	return &RegisterWalletPublicKeyResult{Key: registered, ReplacedOtherKey: replaced}, nil
}
