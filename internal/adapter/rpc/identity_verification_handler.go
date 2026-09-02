package rpc

import (
	"context"
	"errors"

	identityv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/identity/v1/identityv1connect"
	identityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/identity/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// verifiedIdentityLevel derives VerificationLevel from a VerifiedIdentity's
// current status. An ACTIVE status maps to IDENTITY_VERIFIED; any other
// status (e.g. NEEDS_REVERIFICATION) maps to UNVERIFIED so the caller is not
// treated as verified after a failed 現況確認.
func verifiedIdentityLevel(vi *entity.VerifiedIdentity) entity.VerificationLevel {
	if vi != nil && vi.Status == entity.VerificationStatusActive {
		return entity.VerificationLevelIdentityVerified
	}
	return entity.VerificationLevelUnverified
}

// Compile-time assertion that IdentityVerificationHandler satisfies the
// generated interface.
var _ identityv1connect.IdentityVerificationServiceHandler = (*IdentityVerificationHandler)(nil)

// IdentityVerificationHandler implements the fan-facing
// IdentityVerificationService Connect interface.
//
// Authorization: every RPC carries a user_id field that MUST match the
// JWT-derived caller. Mismatches are rejected with PERMISSION_DENIED per the
// rpc-auth-scoping convention. Business logic is delegated to
// IdentityVerificationUseCase; this handler only maps proto↔entity and
// converts errors.
type IdentityVerificationHandler struct {
	identityUC usecase.IdentityVerificationUseCase
	userUC     usecase.UserUseCase
	logger     *logging.Logger
}

// NewIdentityVerificationHandler creates a new IdentityVerificationHandler.
func NewIdentityVerificationHandler(
	identityUC usecase.IdentityVerificationUseCase,
	userUC usecase.UserUseCase,
	logger *logging.Logger,
) *IdentityVerificationHandler {
	return &IdentityVerificationHandler{
		identityUC: identityUC,
		userUC:     userUC,
		logger:     logger,
	}
}

// StartVerify issues a Pocket Sign challenge for the authenticated caller.
//
// Errors:
//   - UNAUTHENTICATED: no valid JWT in the request context.
//   - PERMISSION_DENIED: user_id does not match the JWT-derived caller.
//   - INVALID_ARGUMENT: method is UNSPECIFIED or user_id is empty.
//   - NOT_FOUND: the user account does not exist.
//   - UNAVAILABLE: Pocket Sign Verify API is not configured.
func (h *IdentityVerificationHandler) StartVerify(
	ctx context.Context,
	req *connect.Request[identityv1.StartVerifyRequest],
) (*connect.Response[identityv1.StartVerifyResponse], error) {
	callerUserID, err := h.resolveCallerUserID(ctx)
	if err != nil {
		return nil, err
	}

	reqUserID := req.Msg.GetUserId().GetValue()
	if err := mapper.RequireUserIDMatch(callerUserID, reqUserID); err != nil {
		return nil, err
	}

	method := mapper.VerificationMethodFromProto(req.Msg.GetMethod())
	if method == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("method must be specified"))
	}

	challenge, err := h.identityUC.StartVerify(ctx, callerUserID, method)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&identityv1.StartVerifyResponse{
		SessionId: challenge.SessionID,
		Challenge: challenge.Challenge,
	}), nil
}

// CompleteVerify validates the SDK-signed response and creates a VerifiedIdentity.
//
// Errors:
//   - UNAUTHENTICATED: no valid JWT.
//   - PERMISSION_DENIED: user_id mismatch.
//   - INVALID_ARGUMENT: session_id is unknown/expired or signature is invalid.
//   - NOT_FOUND: user does not exist.
//   - ALREADY_EXISTS: the Pocket Sign User.id is already bound to a different
//     account (duplicate-person); includes a recovery-path message.
//   - UNAVAILABLE: Pocket Sign Verify API is not configured.
func (h *IdentityVerificationHandler) CompleteVerify(
	ctx context.Context,
	req *connect.Request[identityv1.CompleteVerifyRequest],
) (*connect.Response[identityv1.CompleteVerifyResponse], error) {
	callerUserID, err := h.resolveCallerUserID(ctx)
	if err != nil {
		return nil, err
	}

	reqUserID := req.Msg.GetUserId().GetValue()
	if err := mapper.RequireUserIDMatch(callerUserID, reqUserID); err != nil {
		return nil, err
	}

	vi, err := h.identityUC.CompleteVerify(ctx, callerUserID, req.Msg.GetSessionId(), req.Msg.GetSignedResponse())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&identityv1.CompleteVerifyResponse{
		VerifiedIdentity:  mapper.VerifiedIdentityToProto(vi),
		VerificationLevel: mapper.VerificationLevelToProto(verifiedIdentityLevel(vi)),
	}), nil
}

// ReCheck performs a 現況確認 (liveness) re-check for the caller.
//
// Errors:
//   - UNAUTHENTICATED: no valid JWT.
//   - PERMISSION_DENIED: user_id mismatch.
//   - NOT_FOUND: the user has no active VerifiedIdentity.
//   - UNAVAILABLE: Pocket Sign Verify API is not configured.
func (h *IdentityVerificationHandler) ReCheck(
	ctx context.Context,
	req *connect.Request[identityv1.ReCheckRequest],
) (*connect.Response[identityv1.ReCheckResponse], error) {
	callerUserID, err := h.resolveCallerUserID(ctx)
	if err != nil {
		return nil, err
	}

	reqUserID := req.Msg.GetUserId().GetValue()
	if err := mapper.RequireUserIDMatch(callerUserID, reqUserID); err != nil {
		return nil, err
	}

	vi, err := h.identityUC.ReCheck(ctx, callerUserID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&identityv1.ReCheckResponse{
		Status:            mapper.VerificationStatusToProto(vi.Status),
		VerificationLevel: mapper.VerificationLevelToProto(verifiedIdentityLevel(vi)),
	}), nil
}

// GetMyVerificationStatus returns the caller's current verification level and
// backing VerifiedIdentity.
//
// Errors:
//   - UNAUTHENTICATED: no valid JWT.
//   - PERMISSION_DENIED: user_id mismatch.
//   - NOT_FOUND: the user does not exist.
func (h *IdentityVerificationHandler) GetMyVerificationStatus(
	ctx context.Context,
	req *connect.Request[identityv1.GetMyVerificationStatusRequest],
) (*connect.Response[identityv1.GetMyVerificationStatusResponse], error) {
	callerUserID, err := h.resolveCallerUserID(ctx)
	if err != nil {
		return nil, err
	}

	reqUserID := req.Msg.GetUserId().GetValue()
	if err := mapper.RequireUserIDMatch(callerUserID, reqUserID); err != nil {
		return nil, err
	}

	level, vi, err := h.identityUC.GetMyVerificationStatus(ctx, callerUserID)
	if err != nil {
		return nil, err
	}

	resp := &identityv1.GetMyVerificationStatusResponse{
		VerificationLevel: mapper.VerificationLevelToProto(level),
	}
	if vi != nil {
		resp.VerifiedIdentity = mapper.VerifiedIdentityToProto(vi)
	}
	return connect.NewResponse(resp), nil
}

// resolveCallerUserID extracts the external user ID from the JWT context,
// resolves it to an internal user, and returns the internal user ID. Returns a
// Connect error if the context is unauthenticated or the user is not found.
func (h *IdentityVerificationHandler) resolveCallerUserID(ctx context.Context) (string, error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return "", err
	}

	user, err := h.userUC.GetByExternalID(ctx, externalID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return "", connect.NewError(connect.CodeUnauthenticated, errors.New("user not found"))
		}
		return "", err
	}
	return user.ID, nil
}
