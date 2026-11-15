package usecase

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/analyticslog"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// SettlementUseCase executes the periodic post-event payout sweep.
// For each held Settlement whose Order has passed the release gate and whose
// Organizer's payout account is payout-active, it resolves pi_ → ch_,
// creates the Stripe Transfer(s), and marks the settlement released.
type SettlementUseCase interface {
	// ReleaseDueSettlements scans held settlements whose release gate has
	// passed and whose Organizer is payout-active, then releases each by
	// creating the Transfer(s) and recording the result. Per-settlement
	// failures are logged and skipped so one bad settlement never blocks the
	// rest of the sweep. Idempotent: an already-released settlement is skipped.
	//
	// # Possible errors
	//
	//  - Internal: the work-list query failed (per-settlement failures are
	//    logged and skipped, not returned).
	ReleaseDueSettlements(ctx context.Context) error
}

// settlementUseCase implements [SettlementUseCase].
type settlementUseCase struct {
	settlementRepo     entity.SettlementRepository
	orderRepo          entity.OrderRepository
	payoutAccountRepo  entity.PayoutAccountRepository
	eventStartTimeRepo EventStartTimeRepository
	disputeBuffer      time.Duration
	clock              Clock
	logger             *logging.Logger
}

// Compile-time interface compliance check.
var _ SettlementUseCase = (*settlementUseCase)(nil)

// NewSettlementUseCase constructs a SettlementUseCase with the given
// dependencies. All parameters are required and must not be nil.
// disputeBuffer is the minimum wait after the event's start_time before funds
// are released (e.g. 7 × 24h = 168h for a 7-day dispute-safety window).
func NewSettlementUseCase(
	settlementRepo entity.SettlementRepository,
	orderRepo entity.OrderRepository,
	payoutAccountRepo entity.PayoutAccountRepository,
	eventStartTimeRepo EventStartTimeRepository,
	disputeBuffer time.Duration,
	clock Clock,
	logger *logging.Logger,
) SettlementUseCase {
	return &settlementUseCase{
		settlementRepo:     settlementRepo,
		orderRepo:          orderRepo,
		payoutAccountRepo:  payoutAccountRepo,
		eventStartTimeRepo: eventStartTimeRepo,
		disputeBuffer:      disputeBuffer,
		clock:              clock,
		logger:             logger,
	}
}

// ReleaseDueSettlements implements [SettlementUseCase].
func (uc *settlementUseCase) ReleaseDueSettlements(ctx context.Context) error {
	held, err := uc.settlementRepo.ListHeld(ctx)
	if err != nil {
		return err
	}

	for _, s := range held {
		if err := uc.releaseOne(ctx, s); err != nil {
			uc.logger.Warn(ctx, "payout sweep: failed to release settlement; skipping",
				slog.String("settlement_id", string(s.ID)),
				slog.String("order_id", string(s.OrderID)),
				slog.Any("error", err),
			)
		}
	}
	return nil
}

// releaseOne attempts to release a single held settlement. It checks the
// release gate, verifies Organizer payout eligibility, resolves pi_ → ch_,
// runs the splits engine, creates Transfer(s), and marks the settlement
// released. All steps are idempotent via the settlement status guard in
// MarkReleased.
func (uc *settlementUseCase) releaseOne(ctx context.Context, s *entity.Settlement) error {
	// -- load the Order to obtain the amount and currency. --
	order, err := uc.orderRepo.Get(ctx, s.OrderID)
	if err != nil {
		return err
	}

	now := uc.clock()
	passed, err := uc.releaseGatePassed(ctx, s, now)
	if err != nil || !passed {
		return err
	}
	acct, err := uc.payoutActiveAccount(ctx, s)
	if err != nil || acct == nil {
		return err
	}

	// -- splits engine: validate splits and build the Transfer work list. --
	plan, err := s.ReleasePlan(order.Amount)
	if err != nil {
		return err
	}

	// -- resolve the provider payment ID to its charge ID. --
	providerChargeID, err := uc.orderRepo.GetProviderChargeID(ctx, order.Payment.ProviderPaymentID)
	if err != nil {
		return err
	}

	releasedSplits, err := uc.createPayouts(ctx, s, acct, providerChargeID, order.Currency, plan.Splits)
	if err != nil {
		return err
	}

	// -- persist the released state atomically. --
	if err := uc.settlementRepo.MarkReleased(ctx, s.ID, providerChargeID, now, releasedSplits); err != nil {
		// FailedPrecondition from MarkReleased means the status is no longer
		// Held — already released by a concurrent sweep. Idempotent no-op.
		var appErr *apperr.AppErr
		if errors.As(err, &appErr) && appErr.Code == codes.FailedPrecondition {
			uc.logger.Info(ctx, "payout sweep: settlement already released (concurrent sweep); skipping",
				slog.String("settlement_id", string(s.ID)),
			)
			return nil
		}
		return err
	}

	uc.logger.Info(ctx, "payout sweep: settlement released", analyticslog.Attrs(analyticslog.TopicPayment,
		slog.String("settlement_id", string(s.ID)),
		slog.String("order_id", string(s.OrderID)),
		slog.String("provider_charge_id", providerChargeID),
		slog.Int("split_count", len(releasedSplits)),
	)...)
	return nil
}

// releaseGatePassed reports whether the settlement's release gate (event
// start time + dispute buffer) has passed at now. A nil start time means the
// event time is not yet published; it withholds (no error). Reading it fresh
// each sweep picks up a start time filled in after the purchase. An event
// that no longer exists is skipped with a warning.
func (uc *settlementUseCase) releaseGatePassed(ctx context.Context, s *entity.Settlement, now time.Time) (bool, error) {
	eventStartTime, err := uc.eventStartTimeRepo.GetEventStartTime(ctx, s.EventID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			uc.logger.Warn(ctx, "payout sweep: event not found for settlement; skipping",
				slog.String("settlement_id", string(s.ID)),
				slog.String("event_id", s.EventID),
			)
			return false, nil
		}
		return false, err
	}
	var startTime time.Time
	if eventStartTime != nil {
		startTime = *eventStartTime
	}
	// Gate not yet passed is not an error — the next sweep will retry.
	return entity.IsReleaseEligible(now, startTime, uc.disputeBuffer), nil
}

// payoutActiveAccount returns the Organizer's payout account when its
// transfers capability is active, refreshed from the provider (the cached
// status when the refresh fails), and nil when the payout is withheld: no
// account yet, or verification not complete.
func (uc *settlementUseCase) payoutActiveAccount(ctx context.Context, s *entity.Settlement) (*entity.PayoutAccount, error) {
	acct, err := uc.payoutAccountRepo.GetByOrganizerID(ctx, s.OrganizerID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// No payout account yet; payout withheld (not failed).
			uc.logger.Info(ctx, "payout sweep: no payout account for organizer; payout withheld",
				slog.String("settlement_id", string(s.ID)),
				slog.String("organizer_id", s.OrganizerID),
			)
			return nil, nil
		}
		return nil, err
	}

	// Refresh capability status from the provider before deciding.
	freshStatus, err := uc.payoutAccountRepo.GetStatus(ctx, acct.ProviderAccountID)
	if err != nil {
		uc.logger.Warn(ctx, "payout sweep: failed to refresh account status; using cached status",
			slog.String("settlement_id", string(s.ID)),
			slog.String("provider_account_id", acct.ProviderAccountID),
			slog.Any("error", err),
		)
		freshStatus = acct.Status
	}

	if !freshStatus.IsPayoutEligible() {
		// payout account verification not complete; payout withheld (not failed).
		uc.logger.Info(ctx, "payout sweep: organizer account not payout-active; payout withheld",
			slog.String("settlement_id", string(s.ID)),
			slog.String("organizer_id", s.OrganizerID),
			slog.String("account_status", freshStatus.String()),
		)
		return nil, nil
	}
	return acct, nil
}

// createPayouts creates one Transfer per split and returns the splits with
// their provider payout ids.
//
// NOTE: NO on_behalf_of and NO statement descriptor are set on the Transfer.
// The platform is the Stripe settlement merchant; the statement descriptor is
// platform account configuration owned by cloud-provisioning task 5.1. Setting
// on_behalf_of would require the Organizer's payout account to hold the
// card_payments capability active before the original charge, which would
// gate ticket sale on merchant KYB — contradicting the design requirement
// that onboarding never blocks sale.
func (uc *settlementUseCase) createPayouts(ctx context.Context, s *entity.Settlement, acct *entity.PayoutAccount, providerChargeID, currency string, splits []entity.SettlementSplit) ([]entity.SettlementSplit, error) {
	releasedSplits := make([]entity.SettlementSplit, len(splits))
	for i, split := range splits {
		providerPayoutID, err := uc.settlementRepo.CreatePayout(ctx, entity.PayoutParams{
			SettlementID:           s.ID,
			PayeeProviderAccountID: acct.ProviderAccountID,
			ProviderChargeID:       providerChargeID,
			Amount:                 split.Amount,
			Currency:               currency,
		})
		if err != nil {
			return nil, err
		}
		releasedSplits[i] = entity.SettlementSplit{
			PayeeOrganizerID: split.PayeeOrganizerID,
			Amount:           split.Amount,
			ProviderPayoutID: providerPayoutID,
		}
	}
	return releasedSplits, nil
}
