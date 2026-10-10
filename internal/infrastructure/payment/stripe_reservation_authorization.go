package payment

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	stripe "github.com/stripe/stripe-go/v86"
)

// Compile-time interface compliance check.
var _ entity.ReservationAuthorizationPort = (*StripeReservationAuthorizationPort)(nil)

// StripeReservationAuthorizationPort implements
// [entity.ReservationAuthorizationPort]: the card hold of a first-come
// checkout, a manual-capture PaymentIntent with the ticket sale's
// [CardHoldPolicy]. One PaymentIntent per Reservation; a declined card is
// retried on the same PaymentIntent.
//
// Capture and cancel outcomes follow the PaymentIntent's status, not the HTTP
// status of an earlier request: after any error the PaymentIntent is read back
// and classified, so a repeated capture is safe however late it comes,
// beyond Stripe's 24-hour idempotency-key window.
type StripeReservationAuthorizationPort struct {
	client *stripe.Client
	logger *logging.Logger
	policy CardHoldPolicy
}

// NewStripeReservationAuthorizationPort creates the ticket sale's card-hold
// port with the given Stripe secret key.
func NewStripeReservationAuthorizationPort(secretKey string, logger *logging.Logger) *StripeReservationAuthorizationPort {
	return newStripeReservationAuthorizationPort(secretKey, "", logger)
}

func newStripeReservationAuthorizationPort(secretKey, apiURL string, logger *logging.Logger) *StripeReservationAuthorizationPort {
	return &StripeReservationAuthorizationPort{
		client: newStripeClient(secretKey, apiURL),
		logger: logger,
		policy: TicketSaleCardHoldPolicy,
	}
}

// CreateAuthorization implements [entity.ReservationAuthorizationPort]. The
// idempotency key is the Reservation id, and every parameter (the metadata
// included) is derived from the Reservation, so a repeat returns the same
// PaymentIntent and client secret.
func (p *StripeReservationAuthorizationPort) CreateAuthorization(ctx context.Context, amountJPY int64, meta entity.AuthorizationMetadata) (string, string, error) {
	if amountJPY <= 0 {
		return "", "", apperr.New(codes.InvalidArgument, "amountJPY must be positive")
	}

	params := &stripe.PaymentIntentCreateParams{
		Amount:        new(amountJPY),
		Currency:      stripe.String(string(stripe.CurrencyJPY)),
		CaptureMethod: stripe.String(string(stripe.PaymentIntentCaptureMethodManual)),
		AutomaticPaymentMethods: &stripe.PaymentIntentCreateAutomaticPaymentMethodsParams{
			Enabled:        new(true),
			AllowRedirects: stripe.String(string(stripe.PaymentIntentAutomaticPaymentMethodsAllowRedirectsNever)),
		},
	}
	params.AddMetadata("reservation_id", string(meta.ReservationID))
	params.AddMetadata("ticket_sale_id", string(meta.TicketSaleID))
	params.AddMetadata("event_id", meta.EventID)
	params.SetIdempotencyKey(p.policy.idempotencyKey("authorize", string(meta.ReservationID)))

	pi, err := p.client.V1PaymentIntents.Create(ctx, params)
	if err != nil {
		return "", "", toPaymentAppErr(ctx, err, "failed to create PaymentIntent", p.logger)
	}

	p.logger.Info(ctx, "Stripe PaymentIntent created for a reservation",
		slog.String("payment_intent_id", pi.ID),
		slog.String("reservation_id", string(meta.ReservationID)),
		slog.Int64("amount_jpy", amountJPY),
	)
	return pi.ID, pi.ClientSecret, nil
}

// VerifyAuthorization implements [entity.ReservationAuthorizationPort]. Every
// card brand is accepted.
func (p *StripeReservationAuthorizationPort) VerifyAuthorization(ctx context.Context, authorizationRef string, expectedAmountJPY int64) error {
	pi, err := p.retrieve(ctx, authorizationRef)
	if err != nil {
		return err
	}
	if pi.Status != stripe.PaymentIntentStatusRequiresCapture {
		return apperr.New(codes.FailedPrecondition,
			"the card hold is not authenticated or not waiting to be captured")
	}
	if pi.Amount != expectedAmountJPY {
		return apperr.New(codes.InvalidArgument, "the card hold's amount differs from the reservation's amount")
	}
	if pi.Currency != stripe.CurrencyJPY {
		return apperr.New(codes.InvalidArgument, "the card hold's currency must be jpy")
	}
	return nil
}

// CaptureAuthorization implements [entity.ReservationAuthorizationPort].
func (p *StripeReservationAuthorizationPort) CaptureAuthorization(ctx context.Context, authorizationRef string) (*entity.CapturedPayment, error) {
	params := &stripe.PaymentIntentCaptureParams{}
	params.AddExpand("latest_charge")
	params.SetIdempotencyKey(p.policy.idempotencyKey("capture", authorizationRef))

	pi, captureErr := p.client.V1PaymentIntents.Capture(ctx, authorizationRef, params)
	if captureErr == nil {
		p.logger.Info(ctx, "Stripe PaymentIntent captured for a reservation",
			slog.String("payment_intent_id", authorizationRef))
		return capturedPayment(pi), nil
	}
	if isStripeNotFound(captureErr) {
		return nil, toPaymentAppErr(ctx, captureErr, "PaymentIntent not found", p.logger)
	}

	// Classify by the PaymentIntent's status, not by the error.
	current, err := p.retrieve(ctx, authorizationRef)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, err
		}
		return nil, apperr.Wrap(captureErr, codes.Unavailable, "capture outcome is not known yet")
	}
	switch current.Status {
	case stripe.PaymentIntentStatusSucceeded:
		return capturedPayment(current), nil
	case stripe.PaymentIntentStatusCanceled:
		return nil, apperr.Wrap(captureErr, codes.FailedPrecondition, "the card hold was released or expired")
	case stripe.PaymentIntentStatusRequiresCapture:
		if isCardRefusal(captureErr) {
			return nil, apperr.Wrap(captureErr, codes.FailedPrecondition, "the card can no longer be charged")
		}
		return nil, apperr.Wrap(captureErr, codes.Unavailable, "capture outcome is not known yet")
	default:
		// processing, or a capture of the same hold still in flight.
		return nil, apperr.Wrap(captureErr, codes.Unavailable, "capture outcome is not known yet")
	}
}

// CancelAuthorization implements [entity.ReservationAuthorizationPort].
func (p *StripeReservationAuthorizationPort) CancelAuthorization(ctx context.Context, authorizationRef string) error {
	params := &stripe.PaymentIntentCancelParams{}
	params.SetIdempotencyKey(p.policy.idempotencyKey("cancel", authorizationRef))

	_, cancelErr := p.client.V1PaymentIntents.Cancel(ctx, authorizationRef, params)
	if cancelErr == nil {
		p.logger.Info(ctx, "Stripe PaymentIntent cancelled for a reservation",
			slog.String("payment_intent_id", authorizationRef))
		return nil
	}
	if isStripeNotFound(cancelErr) {
		return toPaymentAppErr(ctx, cancelErr, "PaymentIntent not found", p.logger)
	}

	current, err := p.retrieve(ctx, authorizationRef)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return err
		}
		return apperr.Wrap(cancelErr, codes.Unavailable, "cancel outcome is not known yet")
	}
	switch current.Status {
	case stripe.PaymentIntentStatusCanceled:
		// Released earlier, or the hold expired: nothing more to do.
		return nil
	case stripe.PaymentIntentStatusSucceeded:
		return apperr.Wrap(cancelErr, codes.FailedPrecondition, "the card hold has already been charged")
	default:
		return toPaymentAppErr(ctx, cancelErr, "failed to cancel PaymentIntent", p.logger)
	}
}

// retrieve reads the PaymentIntent with its latest charge expanded.
func (p *StripeReservationAuthorizationPort) retrieve(ctx context.Context, ref string) (*stripe.PaymentIntent, error) {
	params := &stripe.PaymentIntentRetrieveParams{}
	params.AddExpand("latest_charge")
	pi, err := p.client.V1PaymentIntents.Retrieve(ctx, ref, params)
	if err != nil {
		return nil, toPaymentAppErr(ctx, err, "failed to retrieve PaymentIntent", p.logger)
	}
	return pi, nil
}

// capturedPayment maps a succeeded PaymentIntent to the charged payment.
func capturedPayment(pi *stripe.PaymentIntent) *entity.CapturedPayment {
	brand, last4 := extractCardFacets(pi)
	return &entity.CapturedPayment{
		Provider:         entity.PaymentProviderStripe,
		PaymentIntentRef: pi.ID,
		AmountJPY:        pi.AmountReceived,
		Currency:         strings.ToUpper(string(pi.Currency)),
		CardBrand:        brand,
		CardLast4:        last4,
	}
}

// isStripeNotFound reports whether err is a Stripe 404.
func isStripeNotFound(err error) bool {
	stripeErr, ok := errors.AsType[*stripe.Error](err)
	return ok && stripeErr.HTTPStatusCode == http.StatusNotFound
}

// isCardRefusal reports whether err is the card network refusing the charge.
func isCardRefusal(err error) bool {
	stripeErr, ok := errors.AsType[*stripe.Error](err)
	return ok && (stripeErr.Type == stripe.ErrorTypeCard || stripeErr.HTTPStatusCode == http.StatusPaymentRequired)
}
