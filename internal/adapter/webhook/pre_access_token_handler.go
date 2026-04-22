// Package webhook provides HTTP handlers for Zitadel Actions v2 webhook
// Targets. Handlers receive PAYLOAD_TYPE_JWT bodies — the request body is a
// JWT whose claims carry the Actions v2 function payload (user, org, etc.).
// Handlers verify the JWT against a distinct webhook audience and return a
// response that Zitadel merges into the in-flight OIDC/gRPC flow.
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

// PreAccessTokenHandler serves `POST /pre-access-token`. It is bound to the
// Zitadel Actions v2 function `preaccesstoken`: Zitadel invokes it once per
// token issuance before signing, and expects a JSON response with an
// `append_claims` array whose entries are merged into the outgoing access
// token's claim set.
//
// Per identity-management spec, every issued access token MUST carry the
// authenticated user's `email` claim. This handler extracts `email` from the
// webhook payload's `user.human.email` field and returns it as an
// `append_claims` entry. Machine users (no `user.human.email`) are passed
// through with an empty `append_claims` so token issuance still succeeds.
type PreAccessTokenHandler struct {
	validator *auth.WebhookValidator
	logger    *logging.Logger
}

// NewPreAccessTokenHandler constructs a handler bound to the given validator.
// The validator's `expectedAudience` MUST be the webhook-specific audience
// registered on the Zitadel Target (e.g.
// `urn:liverty-music:webhook:pre-access-token`).
func NewPreAccessTokenHandler(validator *auth.WebhookValidator, logger *logging.Logger) *PreAccessTokenHandler {
	return &PreAccessTokenHandler{validator: validator, logger: logger}
}

// preAccessTokenPayload captures the subset of the Actions v2 payload this
// handler consumes. Unknown fields are ignored by json.Unmarshal, so the rest
// of the payload (org, user_metadata, user_grants, …) does not need to be
// modeled here.
type preAccessTokenPayload struct {
	User struct {
		Human *struct {
			Email string `json:"email"`
		} `json:"human,omitempty"`
	} `json:"user"`
}

// appendClaim is one entry of the `append_claims` response array.
type appendClaim struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// preAccessTokenResponse is the JSON shape Zitadel expects back: it merges
// the listed claims into the outgoing access token.
type preAccessTokenResponse struct {
	AppendClaims []appendClaim `json:"append_claims"`
}

// ServeHTTP implements http.Handler.
func (h *PreAccessTokenHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Warn(ctx, "pre-access-token: failed to read body", slog.String("error", err.Error()))
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(body) == 0 {
		h.logger.Warn(ctx, "pre-access-token: empty body")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	token, err := h.validator.ValidateWebhookToken(ctx, string(body))
	if err != nil {
		h.logger.Warn(ctx, "pre-access-token: webhook JWT validation failed",
			slog.String("error", err.Error()),
		)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Private claims carry the Actions v2 payload. Round-trip through JSON
	// so we can target nested `user.human.email` without walking the claim
	// map manually.
	rawClaims, err := json.Marshal(token.PrivateClaims())
	if err != nil {
		h.logger.Error(ctx, "pre-access-token: failed to marshal private claims", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var payload preAccessTokenPayload
	if err := json.Unmarshal(rawClaims, &payload); err != nil {
		h.logger.Error(ctx, "pre-access-token: failed to unmarshal payload", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := preAccessTokenResponse{AppendClaims: []appendClaim{}}
	if payload.User.Human != nil && payload.User.Human.Email != "" {
		resp.AppendClaims = append(resp.AppendClaims, appendClaim{
			Key:   "email",
			Value: payload.User.Human.Email,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		// Response partially written; can't do much but log.
		h.logger.Error(ctx, "pre-access-token: failed to encode response", fmt.Errorf("encode: %w", err))
	}
}
