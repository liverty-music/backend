package usecase

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// PayoutSweeperUseCase executes the periodic post-event payout sweep.
// For each held Settlement whose Order has passed the release gate and whose
// Organizer's connected account is payout-active, it resolves pi_ → ch_,
// creates the Stripe Transfer(s), and marks the settlement released.
type PayoutSweeperUseCase interface {
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

// payoutSweeperUseCase implements [PayoutSweeperUseCase].
type payoutSweeperUseCase struct {
	settlementRepo       entity.SettlementRepository
	orderRepo            entity.OrderRepository
	connectedAccountRepo entity.OrganizerConnectedAccountRepository
	eventStartTimeRepo   EventStartTimeRepository
	settlementPort       PaymentSettlementPort
	disputeBuffer        time.Duration
	clock                Clock
	logger               *logging.Logger
}

// Compile-time interface compliance check.
var _ PayoutSweeperUseCase = (*payoutSweeperUseCase)(nil)

// NewPayoutSweeperUseCase constructs a PayoutSweeperUseCase with the given
// dependencies. All parameters are required and must not be nil.
// disputeBuffer is the minimum wait after the event's start_time before funds
// are released (e.g. 7 × 24h = 168h for a 7-day dispute-safety window).
func NewPayoutSweeperUseCase(
	settlementRepo entity.SettlementRepository,
	orderRepo entity.OrderRepository,
	connectedAccountRepo entity.OrganizerConnectedAccountRepository,
	eventStartTimeRepo EventStartTimeRepository,
	settlementPort PaymentSettlementPort,
	disputeBuffer time.Duration,
	clock Clock,
	logger *logging.Logger,
) PayoutSweeperUseCase {
	return &payoutSweeperUseCase{
		settlementRepo:       settlementRepo,
		orderRepo:            orderRepo,
		connectedAccountRepo: connectedAccountRepo,
		eventStartTimeRepo:   eventStartTimeRepo,
		settlementPort:       settlementPort,
		disputeBuffer:        disputeBuffer,
		clock:                clock,
		logger:               logger,
	}
}

// ReleaseDueSettlements implements [PayoutSweeperUseCase].
func (uc *payoutSweeperUseCase) ReleaseDueSettlements(ctx context.Context) error {
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
func (uc *payoutSweeperUseCase) releaseOne(ctx context.Context, s *entity.Settlement) error {
	// -- load the Order to obtain the amount and currency. --
	order, err := uc.orderRepo.Get(ctx, s.OrderID)
	if err != nil {
		return err
	}

	// -- release gate: event start_time + dispute buffer. --
	// A nil start_time means the event time is not yet published; withhold
	// (do not error). On postponement the DB row is updated to the new date
	// so reading it fresh each sweep automatically resets the gate.
	eventStartTime, err := uc.eventStartTimeRepo.GetEventStartTime(ctx, s.EventID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			uc.logger.Warn(ctx, "payout sweep: event not found for settlement; skipping",
				slog.String("settlement_id", string(s.ID)),
				slog.String("event_id", s.EventID),
			)
			return nil
		}
		return err
	}

	now := uc.clock()

	var startTime time.Time
	if eventStartTime != nil {
		startTime = *eventStartTime
	}
	if !entity.IsReleaseEligible(now, startTime, uc.disputeBuffer) {
		// Gate not yet passed; not an error — the next sweep will retry.
		return nil
	}

	// -- Organizer payout eligibility: transfers capability must be active. --
	acct, err := uc.connectedAccountRepo.GetByOrganizerID(ctx, s.OrganizerID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// No connected account yet; payout withheld (not failed).
			uc.logger.Info(ctx, "payout sweep: no connected account for organizer; payout withheld",
				slog.String("settlement_id", string(s.ID)),
				slog.String("organizer_id", s.OrganizerID),
			)
			return nil
		}
		return err
	}

	// Refresh capability status from the provider before deciding.
	freshStatus, err := uc.settlementPort.GetAccountStatus(ctx, acct.AccountRef)
	if err != nil {
		uc.logger.Warn(ctx, "payout sweep: failed to refresh account status; using cached status",
			slog.String("settlement_id", string(s.ID)),
			slog.String("account_ref", acct.AccountRef),
			slog.Any("error", err),
		)
		freshStatus = acct.Status
	}

	if !freshStatus.IsPayoutEligible() {
		// KYC not complete; payout withheld (not failed).
		uc.logger.Info(ctx, "payout sweep: organizer account not payout-active; payout withheld",
			slog.String("settlement_id", string(s.ID)),
			slog.String("organizer_id", s.OrganizerID),
			slog.String("account_status", freshStatus.String()),
		)
		return nil
	}

	// -- splits engine: validate splits and build the Transfer work list. --
	splits, err := buildSplits(s, order.Amount)
	if err != nil {
		return err
	}

	// -- resolve pi_ → ch_ (retrieve PaymentIntent with latest_charge). --
	chargeRef, err := uc.settlementPort.ResolveChargeRef(ctx, order.Payment.PaymentIntentRef)
	if err != nil {
		return err
	}

	// -- create Transfer(s) for each split. --
	// NOTE: NO on_behalf_of and NO statement descriptor are set on the
	// Transfer. The platform is the Stripe settlement merchant; the statement
	// descriptor is platform account configuration owned by cloud-provisioning
	// task 5.1. Setting on_behalf_of would require the Organizer's connected
	// account to hold the card_payments capability active before the original
	// charge, which would gate ticket sale on merchant KYB — contradicting the
	// design requirement that onboarding never blocks sale.
	releasedSplits := make([]entity.SettlementSplit, len(splits))
	for i, split := range splits {
		transferRef, err := uc.settlementPort.CreateTransfer(ctx, TransferParams{
			SettlementID:         s.ID,
			PayeeAccountRef:      acct.AccountRef,
			SourceTransactionRef: chargeRef,
			Amount:               split.Amount,
			Currency:             order.Currency,
		})
		if err != nil {
			return err
		}
		releasedSplits[i] = entity.SettlementSplit{
			PayeeOrganizerID: split.PayeeOrganizerID,
			Amount:           split.Amount,
			TransferRef:      transferRef,
		}
	}

	// -- persist the released state atomically. --
	if err := uc.settlementRepo.MarkReleased(ctx, s.ID, chargeRef, now, releasedSplits); err != nil {
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

	uc.logger.Info(ctx, "payout sweep: settlement released",
		slog.String("settlement_id", string(s.ID)),
		slog.String("order_id", string(s.OrderID)),
		slog.String("charge_ref", chargeRef),
		slog.Int("split_count", len(releasedSplits)),
	)
	return nil
}

// buildSplits validates the settlement's splits and returns the work list for
// Transfer creation. The splits engine enforces:
//  1. At least one split must be present.
//  2. Every split amount must be positive.
//  3. The sum of all split amounts must not exceed the Order's captured charge.
//
// MVP: exactly one split (the Organizer). The model is extensible to N payees.
func buildSplits(s *entity.Settlement, chargeAmount int64) ([]entity.SettlementSplit, error) {
	if len(s.Splits) == 0 {
		return nil, apperr.New(codes.InvalidArgument,
			"settlement has no splits; cannot release without a payee")
	}

	var total int64
	for _, split := range s.Splits {
		if split.Amount <= 0 {
			return nil, apperr.New(codes.InvalidArgument,
				"settlement split amount must be positive")
		}
		total += split.Amount
	}

	if total > chargeAmount {
		return nil, apperr.New(codes.InvalidArgument,
			"sum of settlement splits exceeds the captured charge amount")
	}

	return s.Splits, nil
}
