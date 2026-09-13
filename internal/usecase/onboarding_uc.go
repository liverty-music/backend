package usecase

import (
	"context"
	"errors"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// OnboardingUseCase manages Organizer payout-recipient Connect onboarding:
// creating and persisting a connected account and surfacing its status.
type OnboardingUseCase interface {
	// GetOrCreateOnboarding returns the caller's payout-recipient account and
	// its current status, creating the connected account if none exists yet.
	// It also returns a provider-hosted onboarding URL when the account is
	// not yet Active (payout-blocked), so the Organizer can complete KYC/KYB.
	// An already-Active account returns the account with an empty onboarding URL.
	//
	// # Possible errors
	//
	//  - NotFound: no Organizer with the given id exists.
	//  - Unavailable: the payment provider is unreachable.
	//  - Internal: persistence failure.
	GetOrCreateOnboarding(ctx context.Context, organizerID string) (*entity.OrganizerConnectedAccount, string, error)
}

// onboardingUseCase implements [OnboardingUseCase].
type onboardingUseCase struct {
	connectedAccountRepo entity.OrganizerConnectedAccountRepository
	organizerRepo        entity.OrganizerRepository
	settlementPort       PaymentSettlementPort
	returnURL            string
	logger               *logging.Logger
}

// Compile-time interface compliance check.
var _ OnboardingUseCase = (*onboardingUseCase)(nil)

// NewOnboardingUseCase constructs an OnboardingUseCase with the given
// dependencies. All parameters are required and must not be nil. returnURL is
// the provider-redirect URL after the Organizer finishes onboarding (typically
// the organizer console's payout settings page).
func NewOnboardingUseCase(
	connectedAccountRepo entity.OrganizerConnectedAccountRepository,
	organizerRepo entity.OrganizerRepository,
	settlementPort PaymentSettlementPort,
	returnURL string,
	logger *logging.Logger,
) OnboardingUseCase {
	return &onboardingUseCase{
		connectedAccountRepo: connectedAccountRepo,
		organizerRepo:        organizerRepo,
		settlementPort:       settlementPort,
		returnURL:            returnURL,
		logger:               logger,
	}
}

// GetOrCreateOnboarding implements [OnboardingUseCase].
func (uc *onboardingUseCase) GetOrCreateOnboarding(ctx context.Context, organizerID string) (*entity.OrganizerConnectedAccount, string, error) {
	// Verify the organizer exists (propagates NotFound if absent).
	if _, err := uc.organizerRepo.Get(ctx, organizerID); err != nil {
		return nil, "", err
	}

	// Attempt to load an existing connected account.
	acct, err := uc.connectedAccountRepo.GetByOrganizerID(ctx, organizerID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// No account exists yet — provision one.
			return uc.provisionAndPersist(ctx, organizerID)
		}
		return nil, "", err
	}

	// Refresh the status from the provider so the caller sees the latest
	// capability state (e.g. KYC just cleared).
	freshStatus, err := uc.settlementPort.GetAccountStatus(ctx, acct.AccountRef)
	if err != nil {
		// A provider failure means we cannot verify current status; return
		// the cached state and let the caller retry.
		uc.logger.Warn(ctx, "failed to refresh payout onboarding status from provider; returning cached status",
			slog.String("organizer_id", organizerID),
			slog.String("account_ref", acct.AccountRef),
			slog.Any("error", err),
		)
	} else if freshStatus != acct.Status {
		acct.Status = freshStatus
		if updateErr := uc.connectedAccountRepo.UpdateStatus(ctx, organizerID, freshStatus); updateErr != nil {
			uc.logger.Warn(ctx, "failed to persist refreshed payout onboarding status",
				slog.String("organizer_id", organizerID),
				slog.Any("error", updateErr),
			)
		}
	}

	if acct.Status.IsPayoutEligible() {
		// Active — no onboarding action required.
		return acct, "", nil
	}

	// Not yet active — generate a fresh onboarding link so the Organizer can
	// continue verification.
	onboardingURL, err := uc.settlementPort.CreateOnboardingLink(ctx, acct.AccountRef, uc.returnURL)
	if err != nil {
		uc.logger.Warn(ctx, "failed to create onboarding link; returning account without URL",
			slog.String("organizer_id", organizerID),
			slog.Any("error", err),
		)
		return acct, "", nil
	}

	return acct, onboardingURL, nil
}

// provisionAndPersist creates a connected account at the provider, persists it,
// and returns it together with an onboarding link.
func (uc *onboardingUseCase) provisionAndPersist(ctx context.Context, organizerID string) (*entity.OrganizerConnectedAccount, string, error) {
	accountRef, err := uc.settlementPort.CreateConnectedAccount(ctx, organizerID)
	if err != nil {
		return nil, "", err
	}

	acct := &entity.OrganizerConnectedAccount{
		OrganizerID: organizerID,
		AccountRef:  accountRef,
		Status:      entity.PayoutOnboardingStatusPending,
	}
	if err := uc.connectedAccountRepo.Upsert(ctx, acct); err != nil {
		return nil, "", err
	}

	uc.logger.Info(ctx, "provisioned Organizer connected account",
		slog.String("organizer_id", organizerID),
		slog.String("account_ref", accountRef),
	)

	onboardingURL, err := uc.settlementPort.CreateOnboardingLink(ctx, accountRef, uc.returnURL)
	if err != nil {
		uc.logger.Warn(ctx, "failed to create initial onboarding link after provisioning; returning account without URL",
			slog.String("organizer_id", organizerID),
			slog.Any("error", err),
		)
		return acct, "", nil
	}

	return acct, onboardingURL, nil
}
