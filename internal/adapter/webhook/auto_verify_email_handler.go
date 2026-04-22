package webhook

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/pannpers/go-logging/logging"
)

// AutoVerifyEmailHandler serves `POST /auto-verify-email`. It is bound to the
// Zitadel Actions v2 ExecutionRequest on
// `/zitadel.user.v2.UserService/AddHumanUser`: Zitadel invokes it with the
// intercepted gRPC request payload before creating the user, and expects the
// handler to return a mutated payload that Zitadel then persists.
//
// This handler force-sets `email.is_verified = true` so Self-Registration via
// passkey does not trap the user in the Hosted Login OTP step (the default
// verification step Zitadel injects when SMTP is configured and the email is
// unverified). See identity-management spec "Auto-Verify Email on
// Self-Registration" for the full rationale.
//
// NOTE: The exact Zitadel v4 ExecutionRequest response shape for mutating an
// AddHumanUser request is not fully public-documented as of 2026-04. This
// handler returns a JSON merge-patch-style object whose shape matches the
// request structure. At cutover time, verify the response shape against the
// actual Zitadel v4 behavior and adjust if needed.
type AutoVerifyEmailHandler struct {
	validator *auth.WebhookValidator
	logger    *logging.Logger
}

// NewAutoVerifyEmailHandler constructs a handler bound to the given validator.
// The validator's `expectedAudience` MUST be the webhook-specific audience
// registered on the Zitadel Target (e.g.
// `urn:liverty-music:webhook:auto-verify-email`).
func NewAutoVerifyEmailHandler(validator *auth.WebhookValidator, logger *logging.Logger) *AutoVerifyEmailHandler {
	return &AutoVerifyEmailHandler{validator: validator, logger: logger}
}

// autoVerifyEmailResponse is the JSON merge-patch Zitadel applies to the
// intercepted AddHumanUser request. Only `email.is_verified` is set; all
// other fields pass through unchanged.
type autoVerifyEmailResponse struct {
	Email struct {
		IsVerified bool `json:"is_verified"`
	} `json:"email"`
}

// ServeHTTP implements http.Handler.
func (h *AutoVerifyEmailHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Warn(ctx, "auto-verify-email: failed to read body", slog.String("error", err.Error()))
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(body) == 0 {
		h.logger.Warn(ctx, "auto-verify-email: empty body")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if _, err := h.validator.ValidateWebhookToken(ctx, string(body)); err != nil {
		h.logger.Warn(ctx, "auto-verify-email: webhook JWT validation failed",
			slog.String("error", err.Error()),
		)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	resp := autoVerifyEmailResponse{}
	resp.Email.IsVerified = true

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		h.logger.Error(ctx, "auto-verify-email: failed to encode response", fmt.Errorf("encode: %w", err))
	}
}
