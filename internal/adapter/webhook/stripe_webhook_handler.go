package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	stripe "github.com/stripe/stripe-go/v86"
	stripewh "github.com/stripe/stripe-go/v86/webhook"

	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// stripeWebhookUseCase is the subset of [usecase.StripeWebhookService] that
// the handler depends on.
type stripeWebhookUseCase interface {
	HandleEvent(ctx context.Context, event usecase.StripeWebhookEvent) error
}

// StripeWebhookHandler serves `POST /stripe-webhook`. It verifies the
// Stripe-Signature header against the configured webhook signing secret (using
// the Stripe SDK's webhook.ConstructEvent) and then dispatches the parsed event
// to [usecase.StripeWebhookService].
//
// # Public endpoint provisioning
//
// The actual HTTPS endpoint (URL, Stripe webhook registration, and the signing
// secret stored in GCP Secret Manager / exposed via ESO as
// STRIPE_WEBHOOK_SIGNING_SECRET) is a cloud-provisioning task (task 5.1).
// This handler is mounted on the existing WebhookServer (port 9090) which is
// currently an internal-only ClusterIP Service. A separate public-facing route
// (GKE Gateway HTTPRoute → `server-webhook-svc`) must be added in
// cloud-provisioning to expose this path to Stripe's delivery IP ranges.
// The WebhookServer itself supports the additional route without code changes;
// only the Kubernetes ingress rule and Stripe dashboard configuration are
// outstanding.
//
// # Security note
//
// The Stripe-Signature header is verified with a 300-second tolerance window
// (Stripe's default). Requests with an invalid signature or expired timestamps
// are rejected 401. The signing secret is never logged.
type StripeWebhookHandler struct {
	signingSecret string
	webhookSvc    stripeWebhookUseCase
	logger        *logging.Logger
}

// NewStripeWebhookHandler constructs a handler for Stripe webhook events.
// signingSecret is the webhook endpoint's signing secret from the Stripe
// dashboard (whsec_...); it is verified against the Stripe-Signature header.
// When signingSecret is empty (local dev / pre-provisioning) the handler
// rejects all requests with 503 so the caller is warned rather than silently
// accepted with an empty key.
func NewStripeWebhookHandler(
	signingSecret string,
	webhookSvc stripeWebhookUseCase,
	logger *logging.Logger,
) *StripeWebhookHandler {
	return &StripeWebhookHandler{
		signingSecret: signingSecret,
		webhookSvc:    webhookSvc,
		logger:        logger,
	}
}

// ServeHTTP implements http.Handler.
func (h *StripeWebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Guard: reject early if the signing secret is not yet configured.
	// This prevents the endpoint from accepting unverified events before
	// cloud-provisioning task 5.1 is complete.
	if h.signingSecret == "" {
		h.logger.Warn(ctx, "stripe webhook: signing secret not configured; rejecting request")
		http.Error(w, "stripe webhook endpoint not yet configured", http.StatusServiceUnavailable)
		return
	}

	// Read the raw body — required for Stripe signature verification.
	// Stripe signs the raw bytes; parsing before verification invalidates it.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Warn(ctx, "stripe webhook: failed to read request body",
			slog.String("error", err.Error()))
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Verify the Stripe-Signature header using the v1 scheme.
	// Use ConstructEventWithOptions with IgnoreAPIVersionMismatch=true: Stripe
	// stamps each event with the account/endpoint's configured API version, which
	// generally differs from the pinned stripe-go SDK version. Without this option
	// ConstructEvent rejects such events and the mismatch would be misreported below
	// as a signature failure (401), so Stripe would retry valid deliveries forever.
	event, err := stripewh.ConstructEventWithOptions(
		body, r.Header.Get("Stripe-Signature"), h.signingSecret,
		stripewh.ConstructEventOptions{IgnoreAPIVersionMismatch: true},
	)
	if err != nil {
		// Signature invalid or timestamp too old — reject with 401.
		h.logger.Warn(ctx, "stripe webhook: signature verification failed",
			slog.String("error", err.Error()))
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	// Extract charge ref and payment_intent ref from the event payload for
	// charge.* events. The pi_ is used by the use case to resolve the Order;
	// previously only the charge ref was extracted, leaving PaymentIntentRef
	// empty and making the dispute path permanently dead.
	chargeRef, paymentIntentRef := h.extractChargeContext(ctx, event)

	parsed := usecase.StripeWebhookEvent{
		ProviderEventID:  event.ID,
		Type:             usecase.StripeEventType(event.Type),
		ChargeRef:        chargeRef,
		PaymentIntentRef: paymentIntentRef,
		ReceivedAt:       time.Now().UTC(),
	}

	if err := h.webhookSvc.HandleEvent(ctx, parsed); err != nil {
		h.logger.Error(ctx, "stripe webhook: dispatch error", err,
			slog.String("event_id", event.ID),
			slog.String("event_type", string(event.Type)),
		)
		// Return 500 so Stripe retries. The service is idempotent; retries are safe.
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("{}")); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		h.logger.Error(ctx, "stripe webhook: failed to write response", err)
	}
}

// chargeObjectData is the minimal JSON shape of the `data.object` for
// charge.* events. Only the fields the handler needs are declared; unknown
// fields are ignored by json.Unmarshal.
//
// For charge.refunded the data.object IS a Charge. For charge.dispute.created
// Stripe's data.object is a Dispute whose `charge` field is the charge id
// string; the Dispute also has a `payment_intent` field. We handle both
// shapes by reading both fields and letting the caller use whichever is set.
type chargeObjectData struct {
	// ID is the object's own id: "ch_..." for a Charge, "dp_..." for a Dispute.
	ID string `json:"id"`
	// Charge is set on a Dispute object and holds the "ch_..." charge id.
	// Empty on a Charge object (the Charge's own id is in ID).
	Charge string `json:"charge"`
	// PaymentIntent is the "pi_..." id, present on both Charge and Dispute.
	// Stripe may encode it as a string or as an expanded object; we handle the
	// string form only (the webhook endpoint should not request expansions).
	PaymentIntent any `json:"payment_intent"`
}

// extractChargeContext parses the charge ref and payment_intent ref from
// charge.* events. For charge.refunded the data.object is a Charge so the
// charge ref is object.id. For charge.dispute.created the data.object is a
// Dispute so the charge ref is object.charge. In both cases payment_intent is
// a top-level field on the object.
//
// Returns empty strings for non-charge events or on parse failure.
func (h *StripeWebhookHandler) extractChargeContext(ctx context.Context, event stripe.Event) (chargeRef, paymentIntentRef string) {
	switch string(event.Type) {
	case string(usecase.StripeEventChargeRefunded),
		string(usecase.StripeEventChargeDisputeCreated):
	default:
		return "", ""
	}

	var obj chargeObjectData
	if err := json.Unmarshal(event.Data.Raw, &obj); err != nil {
		h.logger.Warn(ctx, "stripe webhook: failed to unmarshal charge/dispute data",
			slog.String("event_id", event.ID),
			slog.String("error", err.Error()),
		)
		return "", ""
	}

	// Resolve the charge ref: for a Charge object use its own id; for a Dispute
	// object use the `charge` field.
	switch string(event.Type) {
	case string(usecase.StripeEventChargeRefunded):
		chargeRef = obj.ID
	case string(usecase.StripeEventChargeDisputeCreated):
		chargeRef = obj.Charge
	}

	// payment_intent may be encoded as a plain string or as an expanded object
	// (if the endpoint registered expansions). We only decode the string form.
	switch v := obj.PaymentIntent.(type) {
	case string:
		paymentIntentRef = v
	case map[string]any:
		// Expanded object — extract the id field.
		if id, ok := v["id"].(string); ok {
			paymentIntentRef = id
		}
	}

	if chargeRef == "" || paymentIntentRef == "" {
		h.logger.Warn(ctx, "stripe webhook: could not extract charge or payment_intent ref from event",
			slog.String("event_id", event.ID),
			slog.String("event_type", string(event.Type)),
			slog.String("charge_ref", chargeRef),
			slog.String("payment_intent_ref", fmt.Sprintf("%v", obj.PaymentIntent)),
		)
	}

	return chargeRef, paymentIntentRef
}
