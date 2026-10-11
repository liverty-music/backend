package rpc

import (
	"context"
	"errors"

	kycv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/kyc/v1/kycv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	kycv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/kyc/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that KycHandler satisfies the generated interface.
var _ kycv1connect.KycServiceHandler = (*KycHandler)(nil)

// KycHandler implements the fan-facing KycService Connect interface: a fan's
// 本人確認 (identity check) via Pocket Sign and its result, the fan's Kyc.
//
// Every call acts for the signed-in caller; no request names a user. Business
// logic is delegated to KycUseCase; this handler only maps proto↔entity.
type KycHandler struct {
	kycUC  usecase.KycUseCase
	userUC usecase.UserUseCase
	logger *logging.Logger
}

// NewKycHandler creates a new KycHandler.
func NewKycHandler(
	kycUC usecase.KycUseCase,
	userUC usecase.UserUseCase,
	logger *logging.Logger,
) *KycHandler {
	return &KycHandler{
		kycUC:  kycUC,
		userUC: userUC,
		logger: logger,
	}
}

// Start creates a Pocket Sign Stamp session for the caller and returns the
// session id and the redirect URL the client opens in the PocketSign app so
// the fan can read their card.
//
// Errors:
//   - UNAUTHENTICATED: no valid JWT, or the caller has no account.
//   - INVALID_ARGUMENT: method is UNSPECIFIED.
//   - UNAVAILABLE: Pocket Sign Stamp API is not configured.
func (h *KycHandler) Start(
	ctx context.Context,
	req *connect.Request[kycv1.StartRequest],
) (*connect.Response[kycv1.StartResponse], error) {
	callerUserID, err := h.resolveCallerUserID(ctx)
	if err != nil {
		return nil, err
	}

	method := mapper.VerificationMethodFromProto(req.Msg.GetMethod())
	if method == entity.VerificationMethodUnspecified {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("method must be specified"))
	}

	sessionID, redirectURL, err := h.kycUC.Start(ctx, callerUserID, method)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&kycv1.StartResponse{
		SessionId:   sessionID,
		RedirectUrl: &entityv1.Url{Value: redirectURL},
	}), nil
}

// Create finalizes the Stamp session and records the caller's Kyc. The card
// signature is performed in the PocketSign app; no signed payload is sent on
// this request.
//
// Errors:
//   - UNAUTHENTICATED: no valid JWT, or the caller has no account.
//   - PERMISSION_DENIED: nonce mismatch (tamper/replay).
//   - FAILED_PRECONDITION: session not yet completed by the fan, or cert invalid.
//   - ALREADY_EXISTS: the Pocket Sign User.id is already bound to a different
//     account (duplicate-person); includes a recovery-path message.
//   - UNAVAILABLE: Pocket Sign Stamp API is not configured.
func (h *KycHandler) Create(
	ctx context.Context,
	req *connect.Request[kycv1.CreateRequest],
) (*connect.Response[kycv1.CreateResponse], error) {
	callerUserID, err := h.resolveCallerUserID(ctx)
	if err != nil {
		return nil, err
	}

	kyc, err := h.kycUC.Create(ctx, callerUserID, req.Msg.GetSessionId())
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&kycv1.CreateResponse{
		Kyc:               mapper.KycToProto(kyc),
		VerificationLevel: mapper.VerificationLevelToProto(usecase.KycVerificationLevel(kyc)),
	}), nil
}

// Recheck performs a 現況確認 (liveness) re-check of the caller's Kyc.
//
// Errors:
//   - UNAUTHENTICATED: no valid JWT, or the caller has no account.
//   - FAILED_PRECONDITION: the caller has no Kyc to re-check.
//   - UNAVAILABLE: Pocket Sign Verify API is not configured.
func (h *KycHandler) Recheck(
	ctx context.Context,
	_ *connect.Request[kycv1.RecheckRequest],
) (*connect.Response[kycv1.RecheckResponse], error) {
	callerUserID, err := h.resolveCallerUserID(ctx)
	if err != nil {
		return nil, err
	}

	kyc, err := h.kycUC.Recheck(ctx, callerUserID)
	if err != nil {
		return nil, err
	}
	// Defensive: the usecase must return a non-nil Kyc when err == nil.
	// Guard against a contract violation so Recheck never nil-derefs kyc.Status.
	if kyc == nil {
		return nil, connect.NewError(connect.CodeInternal,
			errors.New("recheck returned no kyc without an error"))
	}

	return connect.NewResponse(&kycv1.RecheckResponse{
		Status:            mapper.VerificationStatusToProto(kyc.Status),
		VerificationLevel: mapper.VerificationLevelToProto(usecase.KycVerificationLevel(kyc)),
	}), nil
}

// Get returns the caller's Kyc, whatever its status. The account's
// verification level is read from UserService.Get.
//
// Errors:
//   - UNAUTHENTICATED: no valid JWT, or the caller has no account.
//   - NOT_FOUND: the caller has never passed KYC.
func (h *KycHandler) Get(
	ctx context.Context,
	_ *connect.Request[kycv1.GetRequest],
) (*connect.Response[kycv1.GetResponse], error) {
	callerUserID, err := h.resolveCallerUserID(ctx)
	if err != nil {
		return nil, err
	}

	kyc, err := h.kycUC.Get(ctx, callerUserID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&kycv1.GetResponse{Kyc: mapper.KycToProto(kyc)}), nil
}

// resolveCallerUserID extracts the external user ID from the JWT context,
// resolves it to the caller's internal user, and returns its id. A caller
// without an account fails with Unauthenticated.
func (h *KycHandler) resolveCallerUserID(ctx context.Context) (string, error) {
	externalID, err := mapper.GetExternalUserID(ctx)
	if err != nil {
		return "", err
	}

	user, err := h.userUC.ResolveCaller(ctx, externalID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return "", connect.NewError(connect.CodeUnauthenticated, errors.New("user not found"))
		}
		return "", err
	}
	return user.ID, nil
}
