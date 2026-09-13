package rpc

import (
	"context"
	"errors"

	organizerv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/organizer/v1/organizerv1connect"
	organizerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that PayoutOnboardingHandler satisfies the generated
// interface.
var _ organizerv1connect.PayoutOnboardingServiceHandler = (*PayoutOnboardingHandler)(nil)

// PayoutOnboardingHandler implements the organizer-facing
// PayoutOnboardingService Connect interface. Org-scoped authorization (token
// audience check, login-scope org derivation, role cross-check) is enforced
// structurally by the OrgScopedInterceptor before this handler runs.
// The handler resolves the caller's Organizer from the context-stored Zitadel
// org id, checks its lifecycle status, and delegates to the use case.
type PayoutOnboardingHandler struct {
	onboardingUC usecase.OnboardingUseCase
	organizerUC  usecase.OrganizerUseCase
	logger       *logging.Logger
}

// NewPayoutOnboardingHandler creates a new PayoutOnboardingHandler.
func NewPayoutOnboardingHandler(
	onboardingUC usecase.OnboardingUseCase,
	organizerUC usecase.OrganizerUseCase,
	logger *logging.Logger,
) *PayoutOnboardingHandler {
	return &PayoutOnboardingHandler{
		onboardingUC: onboardingUC,
		organizerUC:  organizerUC,
		logger:       logger,
	}
}

// GetPayoutOnboarding returns the caller's own payout-recipient account and
// its onboarding status, plus an optional provider-hosted URL to start or
// continue verification when the account is not yet Active.
//
// Possible errors:
//   - FAILED_PRECONDITION: The caller's Organizer has been deactivated.
//   - PERMISSION_DENIED: Token carries no organizer-console role or Org
//     resolution failed. Response never reveals whether such an Organizer exists.
func (h *PayoutOnboardingHandler) GetPayoutOnboarding(
	ctx context.Context,
	_ *connect.Request[organizerv1.GetPayoutOnboardingRequest],
) (*connect.Response[organizerv1.GetPayoutOnboardingResponse], error) {
	organizer, err := h.resolveCallerOrganizer(ctx)
	if err != nil {
		return nil, err
	}

	acct, onboardingURL, err := h.onboardingUC.GetOrCreateOnboarding(ctx, organizer.ID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// The organizer was deleted between the resolve call and the
			// onboarding call. Non-revealing per spec D3.
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
		}
		return nil, err
	}

	return connect.NewResponse(&organizerv1.GetPayoutOnboardingResponse{
		Account:       mapper.OrganizerConnectedAccountToProto(acct),
		OnboardingUrl: onboardingURL,
	}), nil
}

// resolveCallerOrganizer reads the caller's Zitadel org id from the context
// (placed there by OrgScopedInterceptor), looks up the linked Organizer via
// the use case, and enforces its lifecycle status. Mirrors the same pattern
// as OrganizerHandler.resolveCallerOrganizer with identical error mapping.
//
// Error mapping:
//   - Zitadel org id absent in context → PERMISSION_DENIED (defence-in-depth)
//   - No Organizer linked to the org id → PERMISSION_DENIED (non-revealing)
//   - Organizer deactivated → FAILED_PRECONDITION (own org, state may be stated)
//   - Any other status (e.g. provisioning) → PERMISSION_DENIED (non-revealing)
func (h *PayoutOnboardingHandler) resolveCallerOrganizer(ctx context.Context) (*entity.Organizer, error) {
	callerOrgID, ok := auth.GetCallerOrgID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	}

	organizer, err := h.organizerUC.GetByZitadelOrgID(ctx, callerOrgID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
		}
		return nil, err
	}

	switch organizer.Status {
	case entity.OrganizerStatusActive:
		return organizer, nil
	case entity.OrganizerStatusDeactivated:
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("organizer is deactivated"))
	default:
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	}
}
