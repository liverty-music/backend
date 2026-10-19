package usecase

import (
	"context"
	"errors"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// PayoutAccountUseCase manages an Organizer's payout-recipient account at the
// payment provider: reading it with its onboarding status, and opening it and
// handing out onboarding links when a member asks to start onboarding.
type PayoutAccountUseCase interface {
	// Get returns the Organizer's stored payout account with its status
	// refreshed from the payment provider. It only reads: it never opens an
	// account and never creates an onboarding link.
	//
	// # Possible errors
	//
	//  - NotFound: the Organizer has no payout account yet.
	//  - Internal: persistence failure.
	Get(ctx context.Context, organizerID string) (*entity.PayoutAccount, error)

	// StartOnboarding opens the Organizer's payout account when it has none
	// (owner email as contact, stored pending) and returns a fresh
	// provider-hosted onboarding URL on every call, whatever the account's
	// status. An account opened before a later failure stays stored.
	//
	// # Possible errors
	//
	//  - NotFound: no Organizer with the given id exists.
	//  - Unavailable: the payment provider is unreachable.
	//  - Internal: persistence failure.
	StartOnboarding(ctx context.Context, organizerID string) (string, error)
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
// the address the provider brings the member back to when they finish
// onboarding or the link has expired (the organizer console, which opens Home).
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
func (uc *payoutAccountUseCase) Get(ctx context.Context, organizerID string) (*entity.PayoutAccount, error) {
	acct, err := uc.payoutAccountRepo.GetByOrganizerID(ctx, organizerID)
	if err != nil {
		return nil, err
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

	return acct, nil
}

// StartOnboarding implements [PayoutAccountUseCase].
func (uc *payoutAccountUseCase) StartOnboarding(ctx context.Context, organizerID string) (string, error) {
	// The Organizer's owner email is the account's contact: Stripe rejects a
	// recipient account that has none.
	organizer, err := uc.organizerRepo.Get(ctx, organizerID)
	if err != nil {
		return "", err
	}

	acct, err := uc.payoutAccountRepo.GetByOrganizerID(ctx, organizerID)
	if err != nil {
		if !errors.Is(err, apperr.ErrNotFound) {
			return "", err
		}
		acct, err = uc.openAccount(ctx, organizerID, organizer.OwnerEmail)
		if err != nil {
			return "", err
		}
	}

	// A link failure fails the call: the link is what the member asked for.
	return uc.payoutAccountRepo.CreateOnboardingLink(ctx, acct.ProviderAccountID, uc.returnURL)
}

// openAccount opens a payout account at the provider and stores it as pending.
// The provider's idempotency key makes a retry after a storage failure reuse
// the same provider account.
func (uc *payoutAccountUseCase) openAccount(ctx context.Context, organizerID string, contactEmail string) (*entity.PayoutAccount, error) {
	providerAccountID, err := uc.payoutAccountRepo.Create(ctx, organizerID, contactEmail)
	if err != nil {
		return nil, err
	}

	acct := &entity.PayoutAccount{
		OrganizerID:       organizerID,
		ProviderAccountID: providerAccountID,
		Status:            entity.PayoutOnboardingStatusPending,
	}
	if err := uc.payoutAccountRepo.Upsert(ctx, acct); err != nil {
		return nil, err
	}

	uc.logger.Info(ctx, "opened Organizer payout account",
		slog.String("organizer_id", organizerID),
		slog.String("provider_account_id", providerAccountID),
	)

	return acct, nil
}
