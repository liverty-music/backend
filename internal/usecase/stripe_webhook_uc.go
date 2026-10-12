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

// StripeEventType is the Stripe webhook event type string.
// We define only the subset the settlement layer handles.
type StripeEventType string

const (
	// StripeEventProviderChargeIDunded fires when a Stripe Refund is applied against
	// a Charge. Used to confirm refund-initiated state changes.
	StripeEventProviderChargeIDunded StripeEventType = "charge.refunded"
	// StripeEventChargeDisputeCreated fires when a chargeback is opened. The
	// platform must respond and may need to reverse the Organizer's Transfer.
	StripeEventChargeDisputeCreated StripeEventType = "charge.dispute.created"
	// StripeEventTransferReversed fires when a Transfer is reversed via a
	// transfer_reversal. Used for audit confirmation.
	StripeEventTransferReversed StripeEventType = "transfer.reversed"
	// StripeEventPayoutPaid fires when the platform's own payout to its bank
	// account completes. Informational for the settlement layer.
	StripeEventPayoutPaid StripeEventType = "payout.paid"
	// StripeEventPayoutFailed fires when the platform's own payout fails.
	StripeEventPayoutFailed StripeEventType = "payout.failed"
	// StripeEventPaymentIntentSucceeded fires when a PaymentIntent is charged:
	// a checkout's capture or a lottery win's capture at the draw. It drives
	// the fulfillment that announces the paid Order.
	StripeEventPaymentIntentSucceeded StripeEventType = "payment_intent.succeeded"
)

// OrderRefunder refunds an Order under the refund policy. OrderUseCase
// satisfies it. Interfaces are defined where consumed (AGENTS.md rule).
type OrderRefunder interface {
	// Refund applies the refund policy for the reason; see [OrderUseCase].
	Refund(ctx context.Context, orderID entity.OrderID, reason RefundReason, now time.Time) (*entity.Order, error)
}

// PaymentFulfiller turns a completed charge into an issued, announced Order.
// OrderUseCase satisfies it. Interfaces are defined where consumed
// (AGENTS.md rule).
type PaymentFulfiller interface {
	// FulfillPayment issues, or finds, the Order of the charge's source and
	// announces it as paid. A charge of no source succeeds without effect.
	FulfillPayment(ctx context.Context, paymentRef string) error
}

// StripeWebhookEvent carries the parsed, signature-verified payload from a
// Stripe webhook call. The caller (HTTP handler) is responsible for signature
// verification and JSON decoding; the use case receives the already-parsed
// envelope.
type StripeWebhookEvent struct {
	// ProviderEventID is the unique Stripe event id (e.g. "evt_..."). Used as
	// the deduplication key to ensure each event is applied at most once.
	ProviderEventID string
	// Type is the Stripe event type string.
	Type StripeEventType
	// ProviderChargeID is the affected Charge id ("ch_..."), populated for charge.*
	// events. May be empty for other event types.
	ProviderChargeID string
	// ProviderPaymentID is the PaymentIntent id ("pi_...") associated with the
	// charge, if the handler could extract it from the event payload. The use
	// case layer resolves this to an Order via OrderRepository. For
	// charge.dispute.created Stripe embeds the payment_intent field on the
	// Charge object inside the event data.
	// May be empty when unavailable or for non-charge events.
	ProviderPaymentID string
	// ReceivedAt is the UTC time the webhook was received (not Stripe's event
	// created time). Used for audit.
	ReceivedAt time.Time
}

// StripeWebhookService processes incoming Stripe webhook events idempotently.
// It is the settlement-side entry point for inbound refund/dispute/transfer/
// payout events. Idempotency is enforced by the ProcessedWebhookEventRepository:
// each provider event id is applied at most once regardless of how many times
// Stripe delivers it.
type StripeWebhookService interface {
	// HandleEvent processes one Stripe webhook event. The caller must have
	// already verified the Stripe-Signature header (via
	// stripe/webhook.ConstructEvent) before calling this method. HandleEvent
	// persists an idempotency record and dispatches based on event type.
	// Unknown event types are accepted (200 OK) and logged at INFO level;
	// they are NOT errors.
	//
	// # Possible errors
	//
	//  - Internal: database or payment-provider failure during dispatch.
	HandleEvent(ctx context.Context, event StripeWebhookEvent) error
}

// stripeWebhookService implements [StripeWebhookService].
type stripeWebhookService struct {
	processedRepo ProcessedWebhookEventRepository
	// orderRepo is used to resolve a dispute's payment_intent → Order, the
	// real production path for the dispute handler. Previously these fields
	// were injected but never called — the handler early-returned because it
	// relied on the HTTP handler to pre-resolve the OrderID (which it could
	// not, since extracting the pi_ from the event data was not being passed
	// through). Fixed: the HTTP handler now extracts ProviderChargeID+ProviderPaymentID;
	// this service calls GetByProviderPaymentID.
	orderRepo entity.OrderRepository
	refunder  OrderRefunder
	fulfiller PaymentFulfiller
	logger    *logging.Logger
}

// Compile-time interface compliance check.
var _ StripeWebhookService = (*stripeWebhookService)(nil)

// NewStripeWebhookService constructs a StripeWebhookService with the given
// dependencies. All parameters are required and must not be nil.
func NewStripeWebhookService(
	processedRepo ProcessedWebhookEventRepository,
	orderRepo entity.OrderRepository,
	refunder OrderRefunder,
	fulfiller PaymentFulfiller,
	logger *logging.Logger,
) StripeWebhookService {
	return &stripeWebhookService{
		processedRepo: processedRepo,
		orderRepo:     orderRepo,
		refunder:      refunder,
		fulfiller:     fulfiller,
		logger:        logger,
	}
}

// HandleEvent implements [StripeWebhookService].
func (s *stripeWebhookService) HandleEvent(ctx context.Context, event StripeWebhookEvent) error {
	// -- idempotency fast-path: skip duplicate deliveries immediately. --
	// TODO: for very high throughput, move dispatch to an async queue and call
	// HandleEvent from a worker. The current synchronous model is safe but
	// couples Stripe's 30-second response timeout to our processing latency.
	// The idempotency check here stays as-is; the queue handles back-pressure.
	already, err := s.processedRepo.IsProcessed(ctx, event.ProviderEventID)
	if err != nil {
		return err
	}
	if already {
		s.logger.Info(ctx, "stripe webhook: duplicate event; already applied (idempotent skip)",
			slog.String("event_id", event.ProviderEventID),
			slog.String("event_type", string(event.Type)),
		)
		return nil
	}

	// Dispatch first. On failure the event is NOT marked processed so Stripe
	// retries. Fix #4: MarkProcessed failure now propagates (returns an error
	// → 500) so the idempotency table is durably written before we return 200.
	// Combined with idempotent dispatch, a double delivery on retry is safe.
	if err := s.dispatch(ctx, event); err != nil {
		return err
	}

	// -- durably record the event as processed. --
	// Fix #4: propagate MarkProcessed failures instead of swallowing them.
	// If MarkProcessed fails we return an error → HTTP 500 → Stripe retries.
	// The retry hits the idempotency fast-path (if the row was written by a
	// concurrent winner) or re-dispatches (idempotent) and re-attempts the write.
	if err := s.processedRepo.MarkProcessed(ctx, event.ProviderEventID); err != nil {
		s.logger.Error(ctx, "stripe webhook: failed to mark event as processed; returning 500 so Stripe retries", err,
			slog.String("event_id", event.ProviderEventID),
			slog.String("event_type", string(event.Type)),
		)
		return apperr.Wrap(err, codes.Internal, "stripe webhook: failed to record processed event")
	}

	return nil
}

// dispatch routes the event to its type-specific handler.
func (s *stripeWebhookService) dispatch(ctx context.Context, event StripeWebhookEvent) error {
	switch event.Type {
	case StripeEventChargeDisputeCreated:
		return s.handleDisputeCreated(ctx, event)
	case StripeEventPaymentIntentSucceeded:
		return s.handlePaymentIntentSucceeded(ctx, event)
	case StripeEventProviderChargeIDunded:
		// A refund that we initiated via RefundOrder is already applied by the
		// use case. This event confirms it at the Stripe level; informational.
		s.logger.Info(ctx, "stripe webhook: charge.refunded event received (informational; refund already applied)",
			slog.String("event_id", event.ProviderEventID),
			slog.String("provider_charge_id", event.ProviderChargeID),
		)
		return nil
	case StripeEventTransferReversed, StripeEventPayoutPaid, StripeEventPayoutFailed:
		// Informational events: log and mark processed; no additional action.
		// Reserve / chargeback-after-payout concerns (task 4.2) are handled
		// in the dispute path. The platform's reserve strategy is a Stripe
		// account configuration concern (cloud-provisioning task 5.1), not code.
		s.logger.Info(ctx, "stripe webhook: informational event received",
			slog.String("event_id", event.ProviderEventID),
			slog.String("event_type", string(event.Type)),
		)
		return nil
	default:
		// Unknown event type: accept (do not return error → Stripe must not retry).
		s.logger.Info(ctx, "stripe webhook: unknown event type; accepted and skipped",
			slog.String("event_id", event.ProviderEventID),
			slog.String("event_type", string(event.Type)),
		)
		return nil
	}
}

// handlePaymentIntentSucceeded handles payment_intent.succeeded: the charge
// is fulfilled (its Order issued, or found, and announced as paid). A failure
// is returned as Internal so the event is not marked processed and Stripe
// delivers it again.
func (s *stripeWebhookService) handlePaymentIntentSucceeded(ctx context.Context, event StripeWebhookEvent) error {
	if event.ProviderPaymentID == "" {
		s.logger.Warn(ctx, "stripe webhook: payment_intent.succeeded without a PaymentIntent id; skipped",
			slog.String("event_id", event.ProviderEventID))
		return nil
	}
	if err := s.fulfiller.FulfillPayment(ctx, event.ProviderPaymentID); err != nil {
		return apperr.Wrap(err, codes.Internal, "stripe webhook: failed to fulfill the completed charge",
			slog.String("event_id", event.ProviderEventID),
			slog.String("provider_payment_id", event.ProviderPaymentID))
	}
	return nil
}

// handleDisputeCreated handles charge.dispute.created — a chargeback opened
// by the cardholder.
//
// Order resolution: the HTTP handler extracts the charge ref and the
// payment_intent id from the dispute event data. This method uses the pi_ to
// resolve the Order via OrderRepository.GetByProviderPaymentID. Previously this
// path was dead because the HTTP handler was not extracting the pi_ and
// the service was checking a pre-set OrderID field that was always empty.
//
// If the Order is not found (test event or external charge) we warn and accept
// with no retry. Any other error propagates → 500 → Stripe retries.
//
// Reserve / negative-balance responsibility:
//
//	A chargeback debits the platform balance. The platform absorbs it
//	(losses_collector = application, per design). Stripe debits the charge
//	amount + dispute fee. The transfer_reversal re-credits the platform with
//	the Organizer's share. The reserve itself is a Stripe account-level setting
//	(cloud-provisioning task 5.1) — no code action required here.
func (s *stripeWebhookService) handleDisputeCreated(ctx context.Context, event StripeWebhookEvent) error {
	if event.ProviderPaymentID == "" {
		// No pi_ in the event data — cannot resolve the Order. Log a warning
		// and accept the event (no retry). Admin must handle manually.
		s.logger.Warn(ctx, "stripe webhook: dispute.created with no provider payment ID; manual review required",
			slog.String("event_id", event.ProviderEventID),
			slog.String("provider_charge_id", event.ProviderChargeID),
		)
		return nil
	}

	// Resolve the Order from the payment_intent ref. This is the real
	// production resolution path — the HTTP handler can extract pi_ from the
	// charge event data; the use case resolves it to an Order via the repo.
	order, err := s.orderRepo.GetByProviderPaymentID(ctx, event.ProviderPaymentID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// Possibly a test event or a charge not issued by our platform.
			// Accept without retry; admin must handle manually.
			s.logger.Warn(ctx, "stripe webhook: dispute.created for unknown payment intent; manual review required",
				slog.String("event_id", event.ProviderEventID),
				slog.String("provider_payment_id", event.ProviderPaymentID),
				slog.String("provider_charge_id", event.ProviderChargeID),
			)
			return nil
		}
		return apperr.Wrap(err, codes.Internal, "stripe webhook: failed to resolve order for dispute")
	}

	// Delegate to OrderUseCase.Refund with reason=DISPUTE. Idempotent: if the
	// Order is already Refunded (e.g. admin ran RefundOrder before the webhook
	// arrived) the call is a no-op that returns the existing order.
	if _, err := s.refunder.Refund(ctx, order.ID, RefundReasonDispute, event.ReceivedAt); err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// Order disappeared between resolution and refund; very unlikely —
			// accept without retry.
			s.logger.Warn(ctx, "stripe webhook: order disappeared during dispute refund; manual review required",
				slog.String("event_id", event.ProviderEventID),
				slog.String("order_id", string(order.ID)),
			)
			return nil
		}
		return apperr.Wrap(err, codes.Internal, "stripe webhook: failed to process dispute refund")
	}

	s.logger.Info(ctx, "stripe webhook: dispute refund executed",
		slog.String("event_id", event.ProviderEventID),
		slog.String("order_id", string(order.ID)),
		slog.String("provider_charge_id", event.ProviderChargeID),
		slog.String("provider_payment_id", event.ProviderPaymentID),
	)
	return nil
}
