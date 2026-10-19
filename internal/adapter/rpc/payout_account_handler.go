package rpc

import (
	"context"
	"errors"

	payoutaccountv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/organizer/payout_account/v1/payout_accountv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	payoutaccountv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/payout_account/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that PayoutAccountHandler satisfies the generated
// interface.
var _ payoutaccountv1connect.PayoutAccountServiceHandler = (*PayoutAccountHandler)(nil)

// PayoutAccountHandler implements the organizer-facing
// PayoutAccountService Connect interface. Org-scoped authorization (token
// audience check, login-scope org derivation, role cross-check) is enforced
// structurally by the OrgScopedInterceptor before this handler runs.
// The handler resolves the caller's Organizer from the context-stored Zitadel
// org id, checks its lifecycle status, and delegates to the use case.
type PayoutAccountHandler struct {
	payoutAccountUC usecase.PayoutAccountUseCase
	organizerUC     usecase.OrganizerUseCase
	logger          *logging.Logger
}

// NewPayoutAccountHandler creates a new PayoutAccountHandler.
func NewPayoutAccountHandler(
	payoutAccountUC usecase.PayoutAccountUseCase,
	organizerUC usecase.OrganizerUseCase,
	logger *logging.Logger,
) *PayoutAccountHandler {
	return &PayoutAccountHandler{
		payoutAccountUC: payoutAccountUC,
		organizerUC:     organizerUC,
		logger:          logger,
	}
}

// Get returns the caller's own payout-recipient account and its onboarding
// status. A NotFound from the use case means the Organizer has no payout
// account yet and is returned unchanged: the caller's Organizer was already
// resolved, so it reveals nothing about another org.
//
// Possible errors:
//   - FAILED_PRECONDITION: The caller's Organizer has been deactivated.
//   - NOT_FOUND: The caller's Organizer has no payout account yet.
//   - PERMISSION_DENIED: Token carries no organizer-console role or Org
//     resolution failed. Response never reveals whether such an Organizer exists.
func (h *PayoutAccountHandler) Get(
	ctx context.Context,
	_ *connect.Request[payoutaccountv1.GetRequest],
) (*connect.Response[payoutaccountv1.GetResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	acct, err := h.payoutAccountUC.Get(ctx, organizer.ID)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&payoutaccountv1.GetResponse{
		PayoutAccount: mapper.PayoutAccountToProto(acct),
	}), nil
}

// StartOnboarding opens the caller's Organizer's payout account when it has
// none and returns a fresh provider-hosted onboarding URL.
//
// Possible errors:
//   - FAILED_PRECONDITION: The caller's Organizer has been deactivated.
//   - PERMISSION_DENIED: Token carries no organizer-console role, Org
//     resolution failed, or the Organizer was deleted meanwhile. Response never
//     reveals whether such an Organizer exists.
//   - UNAVAILABLE: The payment provider cannot be reached.
func (h *PayoutAccountHandler) StartOnboarding(
	ctx context.Context,
	_ *connect.Request[payoutaccountv1.StartOnboardingRequest],
) (*connect.Response[payoutaccountv1.StartOnboardingResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	onboardingURL, err := h.payoutAccountUC.StartOnboarding(ctx, organizer.ID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// The organizer was deleted between the resolve call and the
			// onboarding call. Non-revealing, like the audience gate.
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
		}
		return nil, err
	}

	return connect.NewResponse(&payoutaccountv1.StartOnboardingResponse{
		OnboardingUrl: &entityv1.Url{Value: onboardingURL},
	}), nil
}

// resolveCallerOrganizer reads the caller's Zitadel org id from the context
// (placed there by OrgScopedInterceptor) and delegates to the use case, which
// looks up the linked Organizer and enforces its lifecycle status. Mirrors
// the same pattern as OrganizerHandler.resolveCallerOrganizer.
func (h *PayoutAccountHandler) resolveCallerOrganizer(ctx context.Context) (*entity.Organizer, error) {
	callerOrgID, ok := auth.GetCallerOrgID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	}

	return h.organizerUC.ResolveCaller(ctx, callerOrgID)
}
