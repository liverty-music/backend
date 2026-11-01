package rpc

import (
	"context"

	passkeyv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/passkey/v1/passkeyv1connect"
	passkeyv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/passkey/v1"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/usecase"
)

// Compile-time assertion that PasskeyHandler satisfies the generated interface.
var _ passkeyv1connect.PasskeyServiceHandler = (*PasskeyHandler)(nil)

// PasskeyHandler implements the fan-facing PasskeyService: a signed-in fan
// reads their own recorded passkeys. The request never names a User; the
// caller is resolved to their stored User.
type PasskeyHandler struct {
	passkeyUC usecase.PasskeyUseCase
	userUC    usecase.UserUseCase
	logger    *logging.Logger
}

// NewPasskeyHandler creates a new PasskeyHandler.
func NewPasskeyHandler(passkeyUC usecase.PasskeyUseCase, userUC usecase.UserUseCase, logger *logging.Logger) *PasskeyHandler {
	return &PasskeyHandler{passkeyUC: passkeyUC, userUC: userUC, logger: logger}
}

// List returns the caller's recorded passkeys, newest first. It fails with
// NotFound when the caller has no User.
func (h *PasskeyHandler) List(ctx context.Context, _ *passkeyv1.ListRequest) (*passkeyv1.ListResponse, error) {
	userID, err := callerUserID(ctx, h.userUC)
	if err != nil {
		return nil, err
	}
	passkeys, err := h.passkeyUC.List(ctx, string(userID))
	if err != nil {
		return nil, err
	}
	return &passkeyv1.ListResponse{Passkeys: pb.FromPasskeys(passkeys)}, nil
}
