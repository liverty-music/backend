package rpc

import (
	"context"
	"time"

	walletpublickeyv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/wallet_public_key/v1/wallet_public_keyv1connect"
	walletpublickeyv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/wallet_public_key/v1"
	"connectrpc.com/connect/v2"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that WalletPublicKeyHandler satisfies the generated
// interface.
var _ walletpublickeyv1connect.WalletPublicKeyServiceHandler = (*WalletPublicKeyHandler)(nil)

// WalletPublicKeyHandler implements the fan-facing WalletPublicKeyService:
// the signed-in fan reads and registers the public key of the device that
// shows their entry QR code. The caller is always resolved from the token.
type WalletPublicKeyHandler struct {
	walletUC usecase.WalletPublicKeyUseCase
	userRepo entity.UserRepository
	logger   *logging.Logger
}

// NewWalletPublicKeyHandler creates a new WalletPublicKeyHandler.
func NewWalletPublicKeyHandler(
	walletUC usecase.WalletPublicKeyUseCase,
	userRepo entity.UserRepository,
	logger *logging.Logger,
) *WalletPublicKeyHandler {
	return &WalletPublicKeyHandler{walletUC: walletUC, userRepo: userRepo, logger: logger}
}

// storedUserID resolves the caller's Zitadel subject to their stored users.id.
func (h *WalletPublicKeyHandler) storedUserID(ctx context.Context, providerUserID string) (entity.UserID, error) {
	user, err := h.userRepo.GetByProviderUserID(ctx, providerUserID)
	if err != nil {
		return "", err
	}
	return entity.UserID(user.ID), nil
}

// Register records the public key of the caller's device as the caller's
// WalletPublicKey, replacing any other device's key.
func (h *WalletPublicKeyHandler) Register(
	ctx context.Context,
	req *walletpublickeyv1.RegisterRequest,
) (*walletpublickeyv1.RegisterResponse, error) {
	providerUserID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}
	if len(req.GetPublicKey().GetValue()) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "public_key is required")
	}
	userID, err := h.storedUserID(ctx, providerUserID)
	if err != nil {
		return nil, err
	}

	result, err := h.walletUC.Register(ctx, userID, entity.PublicKey(req.GetPublicKey().GetValue()), time.Now())
	if err != nil {
		return nil, err
	}
	return &walletpublickeyv1.RegisterResponse{
		WalletPublicKey:  mapper.WalletPublicKeyToProto(result.Key),
		ReplacedOtherKey: result.ReplacedOtherKey,
	}, nil
}

// Get returns the caller's current WalletPublicKey.
func (h *WalletPublicKeyHandler) Get(
	ctx context.Context,
	_ *walletpublickeyv1.GetRequest,
) (*walletpublickeyv1.GetResponse, error) {
	providerUserID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return nil, err
	}
	userID, err := h.storedUserID(ctx, providerUserID)
	if err != nil {
		return nil, err
	}
	key, err := h.walletUC.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &walletpublickeyv1.GetResponse{
		WalletPublicKey: mapper.WalletPublicKeyToProto(key),
	}, nil
}
