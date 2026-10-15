package usecase

import (
	"context"
	"errors"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// PayoutAccountUseCase manages Organizer payout-recipient Connect onboarding:
// creating and persisting a payout account and surfacing its status.
type PayoutAccountUseCase interface {
	// Get returns the caller's payout-recipient account and
	// its current status, creating the payout account if none exists yet.
	// It also returns a provider-hosted onboarding URL when the account is
	// not yet Active (payout-blocked), so the Organizer can complete payout account verification.
	// An already-Active account returns the account with an empty onboarding URL.
	//
	// # Possible errors
	//
	//  - NotFound: no Organizer with the given id exists.
	//  - Unavailable: the payment provider is unreachable.
	//  - Internal: persistence failure.
	Get(ctx context.Context, organizerID string) (*entity.PayoutAccount, string, error)
}

// payoutAccountUseCase implements [PayoutAccountUseCase].
type payoutAccountUseCase struct {
	payoutAccountRepo entity.PayoutAccountRepository
	organizerRepo     entity.OrganizerRepository
	returnURL         string
	logger            *logging.Logger
}

// Compile-time interface compliance check.
var _ PayoutAccountUseCase = (*payoutAccountUseCase)(nil)

// NewPayoutAccountUseCase constructs an PayoutAccountUseCase with the given
// dependencies. All parameters are required and must not be nil. returnURL is
// the provider-redirect URL after the Organizer finishes onboarding (typically
// the organizer console's payout settings page).
func NewPayoutAccountUseCase(
	payoutAccountRepo entity.PayoutAccountRepository,
	organizerRepo entity.OrganizerRepository,
	returnURL string,
	logger *logging.Logger,
) PayoutAccountUseCase {
	return &payoutAccountUseCase{
		payoutAccountRepo: payoutAccountRepo,
		organizerRepo:     organizerRepo,
		returnURL:         returnURL,
		logger:            logger,
	}
}

// Get implements [PayoutAccountUseCase].
func (uc *payoutAccountUseCase) Get(ctx context.Context, organizerID string) (*entity.PayoutAccount, string, error) {
	// Verify the organizer exists (propagates NotFound if absent). The record is
	// kept because provisioning needs its contact email — Stripe rejects a
	// recipient account that has none.
	organizer, err := uc.organizerRepo.Get(ctx, organizerID)
	if err != nil {
		return nil, "", err
	}

	// Attempt to load an existing payout account.
	acct, err := uc.payoutAccountRepo.GetByOrganizerID(ctx, organizerID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// No account exists yet — provision one.
			return uc.provisionAndPersist(ctx, organizerID, organizer.OwnerEmail)
		}
		return nil, "", err
	}

	// Refresh the status from the provider so the caller sees the latest
	// capability state (e.g. payout account verification just cleared).
	freshStatus, err := uc.payoutAccountRepo.GetStatus(ctx, acct.ProviderAccountID)
	if err != nil {
		// A provider failure means we cannot verify current status; return
		// the cached state and let the caller retry.
		uc.logger.Warn(ctx, "failed to refresh payout onboarding status from provider; returning cached status",
			slog.String("organizer_id", organizerID),
			slog.String("provider_account_id", acct.ProviderAccountID),
			slog.Any("error", err),
		)
	} else if freshStatus != acct.Status {
		acct.Status = freshStatus
		if updateErr := uc.payoutAccountRepo.UpdateStatus(ctx, organizerID, freshStatus); updateErr != nil {
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
	onboardingURL, err := uc.payoutAccountRepo.CreateOnboardingLink(ctx, acct.ProviderAccountID, uc.returnURL)
	if err != nil {
		uc.logger.Warn(ctx, "failed to create onboarding link; returning account without URL",
			slog.String("organizer_id", organizerID),
			slog.Any("error", err),
		)
		return acct, "", nil
	}

	return acct, onboardingURL, nil
}

// provisionAndPersist creates a payout account at the provider, persists it,
// and returns it together with an onboarding link.
func (uc *payoutAccountUseCase) provisionAndPersist(ctx context.Context, organizerID string, contactEmail string) (*entity.PayoutAccount, string, error) {
	providerAccountID, err := uc.payoutAccountRepo.Create(ctx, organizerID, contactEmail)
	if err != nil {
		return nil, "", err
	}

	acct := &entity.PayoutAccount{
		OrganizerID:       organizerID,
		ProviderAccountID: providerAccountID,
		Status:            entity.PayoutOnboardingStatusPending,
	}
	if err := uc.payoutAccountRepo.Upsert(ctx, acct); err != nil {
		return nil, "", err
	}

	uc.logger.Info(ctx, "provisioned Organizer payout account",
		slog.String("organizer_id", organizerID),
		slog.String("provider_account_id", providerAccountID),
	)

	onboardingURL, err := uc.payoutAccountRepo.CreateOnboardingLink(ctx, providerAccountID, uc.returnURL)
	if err != nil {
		uc.logger.Warn(ctx, "failed to create initial onboarding link after provisioning; returning account without URL",
			slog.String("organizer_id", organizerID),
			slog.Any("error", err),
		)
		return acct, "", nil
	}

	return acct, onboardingURL, nil
}
