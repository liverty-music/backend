package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/pannpers/go-logging/logging"
)

// userResolver resolves a Zitadel `sub` (identity-provider subject / external
// ID) to the platform user. Satisfied by usecase.UserUseCase; declared here so
// the handler depends only on the one method it needs.
type userResolver interface {
	GetByExternalID(ctx context.Context, externalID string) (*entity.User, error)
}

// eventPublisher publishes a domain event as a CloudEvent to a NATS subject.
// Satisfied by usecase.EventPublisher.
type eventPublisher interface {
	PublishEvent(ctx context.Context, subject string, data any) error
}

// CreateSessionHandler serves `POST /create-session`. It is bound to a Zitadel
// Actions v2 Execution on the RESPONSE side of
// `/zitadel.session.v2.SessionService/CreateSession`: Zitadel invokes it once
// per successful, user-initiated login (a session creation) and never on a
// silent `refresh_token` grant, which reuses the existing session and does not
// call CreateSession. The handler therefore emits `account.login` once per
// login with no refresh over-count and nothing to discriminate.
//
// The analytics path is best-effort and non-fatal: the handler resolves the
// login `sub` to the platform UserID and publishes an `ACCOUNT.login` domain
// event, but ALWAYS returns 200 with an empty JSON body — a missing user
// identifier, a failed lookup, or a publish error is logged and skipped so
// session creation / login is never affected. (The Target is provisioned with
// `interruptOnError: false`, so Zitadel also ignores our response, but
// returning 200 keeps the contract explicit.)
type CreateSessionHandler struct {
	validator *auth.WebhookValidator
	users     userResolver
	publisher eventPublisher
	logger    *logging.Logger
}

// NewCreateSessionHandler constructs a handler bound to the given validator,
// user resolver, and event publisher. The validator's audience MUST be the
// webhook-specific audience registered on the Zitadel CreateSession Target.
func NewCreateSessionHandler(
	validator *auth.WebhookValidator,
	users userResolver,
	publisher eventPublisher,
	logger *logging.Logger,
) *CreateSessionHandler {
	return &CreateSessionHandler{validator: validator, users: users, publisher: publisher, logger: logger}
}

// createSessionPayload captures the subset of the Actions v2 method payload
// this handler consumes. For a response-side Execution the payload carries
// both `request` and `response`; the login user is the CreateSession request's
// user check at `request.checks.user.userId`. Unknown fields are ignored.
type createSessionPayload struct {
	Request struct {
		Checks struct {
			User struct {
				UserID string `json:"userId"`
			} `json:"user"`
		} `json:"checks"`
	} `json:"request"`
}

// ServeHTTP implements http.Handler.
func (h *CreateSessionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Warn(ctx, "create-session: failed to read body", slog.String("error", err.Error()))
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(body) == 0 {
		h.logger.Warn(ctx, "create-session: empty body")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	token, err := h.validator.ValidateWebhookToken(ctx, string(body))
	if err != nil {
		h.logger.Warn(ctx, "create-session: webhook JWT validation failed",
			slog.String("error", err.Error()),
		)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Private claims carry the Actions v2 method payload. Round-trip through
	// JSON so we can target nested `request.checks.user.userId` without walking
	// the claim map manually.
	rawClaims, err := json.Marshal(token.PrivateClaims())
	if err != nil {
		// A verified token whose claims cannot be re-marshalled is not worth
		// failing login over; log and return success without emitting.
		h.logger.Error(ctx, "create-session: failed to marshal private claims", err)
		h.writeOK(ctx, w)
		return
	}
	var payload createSessionPayload
	if err := json.Unmarshal(rawClaims, &payload); err != nil {
		h.logger.Error(ctx, "create-session: failed to unmarshal payload", err)
		h.writeOK(ctx, w)
		return
	}

	h.emitAccountLogin(ctx, payload.Request.Checks.User.UserID)
	h.writeOK(ctx, w)
}

// emitAccountLogin resolves the Zitadel `sub` to the platform UserID and
// publishes `ACCOUNT.login` best-effort. Every failure mode — an absent
// identifier, a lookup miss/error, or a publish error — is logged and
// swallowed; it never affects the HTTP response.
func (h *CreateSessionHandler) emitAccountLogin(ctx context.Context, sub string) {
	if sub == "" {
		// The CreateSession `checks.user` is a oneof (userId | loginName), and
		// some login flows attach the user via a later SetSession, so the
		// identifier can legitimately be absent. Skip rather than guess.
		h.logger.Warn(ctx, "create-session: payload missing request.checks.user.userId, skipping account.login")
		return
	}

	user, err := h.users.GetByExternalID(ctx, sub)
	if err != nil {
		// User not yet provisioned, or a transient lookup error. Bounded
		// under-count is acceptable; never fail login.
		h.logger.Warn(ctx, "create-session: sub to UserID lookup failed, skipping account.login",
			slog.String("error", err.Error()),
		)
		return
	}

	if err := h.publisher.PublishEvent(ctx, entity.SubjectAccountLogin, entity.AccountLoginData{UserID: user.ID}); err != nil {
		h.logger.Error(ctx, "create-session: failed to publish ACCOUNT.login", err)
	}
}

// writeOK writes the 200 response Zitadel receives. The body is an empty JSON
// object: a response-side analytics Execution manipulates nothing.
func (h *CreateSessionHandler) writeOK(ctx context.Context, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write([]byte("{}")); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		h.logger.Error(ctx, "create-session: failed to write response", err)
	}
}
