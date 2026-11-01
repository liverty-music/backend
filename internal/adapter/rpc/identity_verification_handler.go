package rpc

import (
	"context"
	"errors"

	identityverificationv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/identity_verification/v1/identity_verificationv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	identityverificationv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/identity_verification/v1"
	"connectrpc.com/connect/v2"
	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that IdentityVerificationHandler satisfies the generated interface.
var _ identityverificationv1connect.IdentityVerificationServiceHandler = (*IdentityVerificationHandler)(nil)

// IdentityVerificationHandler implements the fan-facing IdentityVerificationService Connect interface: a fan's
// 本人確認 (identity check) via Pocket Sign and its result, the fan's IdentityVerification.
//
// Every call acts for the signed-in caller; no request names a user. Business
// logic is delegated to IdentityVerificationUseCase; this handler only maps proto↔entity.
type IdentityVerificationHandler struct {
	identityVerificationUC usecase.IdentityVerificationUseCase
	userUC                 usecase.UserUseCase
	logger                 *logging.Logger
}

// NewIdentityVerificationHandler creates a new IdentityVerificationHandler.
func NewIdentityVerificationHandler(
	identityVerificationUC usecase.IdentityVerificationUseCase,
	userUC usecase.UserUseCase,
	logger *logging.Logger,
) *IdentityVerificationHandler {
	return &IdentityVerificationHandler{
		identityVerificationUC: identityVerificationUC,
		userUC:                 userUC,
		logger:                 logger,
	}
}

// Start creates a Pocket Sign Stamp session for the caller and returns the
// session id and the redirect URL the client opens in the verification provider's app so
// the fan can read their card.
//
// Errors:
//   - UNAUTHENTICATED: no valid JWT, or the caller has no account.
//   - INVALID_ARGUMENT: method is UNSPECIFIED.
//   - UNAVAILABLE: Pocket Sign Stamp API is not configured.
func (h *IdentityVerificationHandler) Start(
	ctx context.Context,
	req *identityverificationv1.StartRequest,
) (*identityverificationv1.StartResponse, error) {
	callerUserID, err := h.resolveCallerUserID(ctx)
	if err != nil {
		return nil, err
	}

	method := pb.ToVerificationMethod(req.GetMethod())
	if method == entity.VerificationMethodUnspecified {
		return nil, connect.NewError(connect.CodeInvalidArgument, "method must be specified")
	}

	sessionID, redirectURL, err := h.identityVerificationUC.Start(ctx, callerUserID, method)
	if err != nil {
		return nil, err
	}

	return &identityverificationv1.StartResponse{
		SessionId:   sessionID,
		RedirectUrl: &entityv1.Url{Value: redirectURL},
	}, nil
}

// Create finalizes the Stamp session and records the caller's IdentityVerification. The card
// signature is performed in the verification provider's app; no signed payload is sent on
// this request.
//
// Errors:
//   - UNAUTHENTICATED: no valid JWT, or the caller has no account.
//   - PERMISSION_DENIED: nonce mismatch (tamper/replay).
//   - FAILED_PRECONDITION: session not yet completed by the fan, or cert invalid.
//   - ALREADY_EXISTS: the Pocket Sign User.id is already bound to a different
//     account (duplicate-person); includes a recovery-path message.
//   - UNAVAILABLE: Pocket Sign Stamp API is not configured.
func (h *IdentityVerificationHandler) Create(
	ctx context.Context,
	req *identityverificationv1.CreateRequest,
) (*identityverificationv1.CreateResponse, error) {
	callerUserID, err := h.resolveCallerUserID(ctx)
	if err != nil {
		return nil, err
	}

	identityVerification, err := h.identityVerificationUC.Create(ctx, callerUserID, req.GetSessionId())
	if err != nil {
		return nil, err
	}

	return &identityverificationv1.CreateResponse{
		IdentityVerification: pb.FromIdentityVerification(identityVerification),
	}, nil
}

// Recheck performs a 現況確認 (liveness) re-check of the caller's IdentityVerification.
//
// Errors:
//   - UNAUTHENTICATED: no valid JWT, or the caller has no account.
//   - FAILED_PRECONDITION: the caller has no IdentityVerification to re-check.
//   - UNAVAILABLE: Pocket Sign Verify API is not configured.
func (h *IdentityVerificationHandler) Recheck(
	ctx context.Context,
	_ *identityverificationv1.RecheckRequest,
) (*identityverificationv1.RecheckResponse, error) {
	callerUserID, err := h.resolveCallerUserID(ctx)
	if err != nil {
		return nil, err
	}

	identityVerification, err := h.identityVerificationUC.Recheck(ctx, callerUserID)
	if err != nil {
		return nil, err
	}
	// Defensive: the usecase must return a non-nil IdentityVerification when err == nil.
	// Guard against a contract violation so Recheck never nil-derefs identityVerification.Status.
	if identityVerification == nil {
		return nil, connect.NewError(connect.CodeInternal,
			"recheck returned no identity verification without an error")
	}

	return &identityverificationv1.RecheckResponse{
		Status: pb.FromVerificationStatus(identityVerification.Status),
	}, nil
}

// Get returns the caller's IdentityVerification, whatever its status.
//
// Errors:
//   - UNAUTHENTICATED: no valid JWT, or the caller has no account.
//   - NOT_FOUND: the caller has never verified their identity.
func (h *IdentityVerificationHandler) Get(
	ctx context.Context,
	_ *identityverificationv1.GetRequest,
) (*identityverificationv1.GetResponse, error) {
	callerUserID, err := h.resolveCallerUserID(ctx)
	if err != nil {
		return nil, err
	}

	identityVerification, err := h.identityVerificationUC.Get(ctx, callerUserID)
	if err != nil {
		return nil, err
	}

	return &identityverificationv1.GetResponse{IdentityVerification: pb.FromIdentityVerification(identityVerification)}, nil
}

// resolveCallerUserID extracts the external user ID from the JWT context,
// resolves it to the caller's internal user, and returns its id. A caller
// without an account fails with Unauthenticated.
func (h *IdentityVerificationHandler) resolveCallerUserID(ctx context.Context) (string, error) {
	providerUserID, err := pb.GetExternalUserID(ctx)
	if err != nil {
		return "", err
	}

	user, err := h.userUC.ResolveCaller(ctx, providerUserID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return "", connect.NewError(connect.CodeUnauthenticated, "user not found")
		}
		return "", err
	}
	return user.ID, nil
}
