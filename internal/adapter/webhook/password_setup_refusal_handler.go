package webhook

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/zitadel/zitadel-go/v3/pkg/actions"

	"github.com/pannpers/go-logging/logging"
)

// PasswordSetupRefusalHandler serves `POST /refuse-password-setup` on the
// internal webhook listener. It is bound to Zitadel Actions v2 REQUEST
// executions on the API methods that set a password on the caller's own
// identity (zitadel.user.v2.UserService/SetPassword and PasswordReset,
// zitadel.auth.v1.AuthService/UpdateMyPassword), through a PAYLOAD_TYPE_JSON
// Target with interruptOnError, whose HMAC `ZITADEL-Signature` header it
// verifies with the Target's signing key.
//
// It never answers 2xx, so Zitadel refuses every such request: fans and
// members sign in with passkeys, and only provisioning, through the
// management API, sets the end-to-end test fans' passwords. A signed request
// is answered 403 and logged with the caller and the method; the request body
// carries the new password, so it is never logged. An unsigned request is
// answered 401, and a missing signing key 503, both of which still refuse.
type PasswordSetupRefusalHandler struct {
	signingKey string
	logger     *logging.Logger
}

// NewPasswordSetupRefusalHandler constructs a handler bound to the
// password-setup-refusal Target's signing key.
func NewPasswordSetupRefusalHandler(signingKey string, logger *logging.Logger) *PasswordSetupRefusalHandler {
	return &PasswordSetupRefusalHandler{signingKey: signingKey, logger: logger}
}

// passwordSetupRequestPayload is the subset of the Actions v2
// request-execution JSON this handler reads. The `request` field, which holds
// the password, is deliberately not decoded.
type passwordSetupRequestPayload struct {
	FullMethod string `json:"fullMethod"`
	UserID     string `json:"userID"`
	OrgID      string `json:"orgID"`
}

// ServeHTTP implements http.Handler.
func (h *PasswordSetupRefusalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// An empty key would not reject anything: actions.ValidateRequestPayload
	// computes the HMAC with whatever key it is given.
	if h.signingKey == "" {
		h.logger.Error(ctx, "refuse-password-setup: signing key is not configured; refusing",
			errors.New("WEBHOOK_PASSWORD_SETUP_REFUSAL_SIGNING_KEY is empty"))
		http.Error(w, "not configured", http.StatusServiceUnavailable)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil || len(body) == 0 {
		h.logger.Warn(ctx, "refuse-password-setup: missing or unreadable body")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := actions.ValidateRequestPayload(body, &r.Header, h.signingKey); err != nil {
		h.logger.Warn(ctx, "refuse-password-setup: webhook signature validation failed",
			slog.String("error", err.Error()))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var payload passwordSetupRequestPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		h.logger.Warn(ctx, "refuse-password-setup: failed to unmarshal payload; refusing",
			slog.String("error", err.Error()))
	}
	h.logger.Warn(ctx, "refuse-password-setup: refused a password-setting request",
		slog.String("method", payload.FullMethod),
		slog.String("provider_user_id", payload.UserID),
		slog.String("org_id", payload.OrgID))
	http.Error(w, "setting a password is not available", http.StatusForbidden)
}
