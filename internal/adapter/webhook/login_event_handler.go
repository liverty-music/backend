package webhook

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/pannpers/go-logging/logging"
)

// sessionUserCheckedEventType is the Zitadel event this handler is bound to. It
// is stored once per interactive login through the hosted Login UI (a user is
// verified into a session); a silent refresh_token grant touches only the
// oidc_session aggregate and a machine jwt_profile grant never creates a
// Login-UI session, so this event is login-specific and human-specific by
// construction. Determined empirically via the Zitadel Events API.
const sessionUserCheckedEventType = "session.user.checked"

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

// LoginEventHandler serves `POST /account-login-event`. It is bound to a
// Zitadel Actions v2 EVENT execution on `event.event = "session.user.checked"`.
// Unlike a request/response (method) execution — whose webhook return REPLACES
// the API payload and previously broke sign-in — an event execution is
// fire-and-forget: it fires after the event is persisted and Zitadel ignores
// the response, so it can never affect login. The handler emits `account.login`
// once per interactive login.
//
// The analytics path is best-effort and non-fatal: it resolves the login user
// to the platform UserID and publishes an `ACCOUNT.login` domain event, but
// ALWAYS returns 200 with an empty JSON body — a wrong event type, a missing
// user identifier, a failed lookup, or a publish error is logged and skipped.
type LoginEventHandler struct {
	validator *auth.WebhookValidator
	users     userResolver
	publisher eventPublisher
	logger    *logging.Logger
}

// NewLoginEventHandler constructs a handler bound to the given validator, user
// resolver, and event publisher. The validator's audience MUST be the
// webhook-specific audience registered on the Zitadel login-event Target.
func NewLoginEventHandler(
	validator *auth.WebhookValidator,
	users userResolver,
	publisher eventPublisher,
	logger *logging.Logger,
) *LoginEventHandler {
	return &LoginEventHandler{validator: validator, users: users, publisher: publisher, logger: logger}
}

// eventExecutionPayload captures the subset of the Actions v2 event-execution
// payload this handler consumes. The top-level `userID` is the event EDITOR
// (for session.user.checked, the Login-UI service user), NOT the person
// logging in — the login user lives inside `event_payload`, which is
// base64-encoded. Unknown fields are ignored.
type eventExecutionPayload struct {
	EventType    string          `json:"event_type"`
	EventPayload json.RawMessage `json:"event_payload"`
}

// sessionUserCheckedPayload is the decoded `event_payload` of a
// session.user.checked event. Its `userID` is the logging-in user.
type sessionUserCheckedPayload struct {
	UserID string `json:"userID"`
}

// ServeHTTP implements http.Handler.
func (h *LoginEventHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Warn(ctx, "login-event: failed to read body", slog.String("error", err.Error()))
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(body) == 0 {
		h.logger.Warn(ctx, "login-event: empty body")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	token, err := h.validator.ValidateWebhookToken(ctx, string(body))
	if err != nil {
		h.logger.Warn(ctx, "login-event: webhook JWT validation failed",
			slog.String("error", err.Error()),
		)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Private claims carry the Actions v2 event payload. Round-trip through
	// JSON so we can decode the nested, base64-encoded `event_payload` without
	// walking the claim map manually.
	rawClaims, err := json.Marshal(token.PrivateClaims())
	if err != nil {
		// A verified token whose claims cannot be re-marshalled is not worth
		// failing login over; log and return success without emitting.
		h.logger.Error(ctx, "login-event: failed to marshal private claims", err)
		h.writeOK(ctx, w)
		return
	}
	var payload eventExecutionPayload
	if err := json.Unmarshal(rawClaims, &payload); err != nil {
		h.logger.Error(ctx, "login-event: failed to unmarshal payload", err)
		h.writeOK(ctx, w)
		return
	}

	if payload.EventType != sessionUserCheckedEventType {
		// The Execution is bound to session.user.checked, but guard anyway so a
		// mis-bound Target never emits a wrong login.
		h.logger.Warn(ctx, "login-event: unexpected event_type, skipping account.login",
			slog.String("event_type", payload.EventType),
		)
		h.writeOK(ctx, w)
		return
	}

	h.emitAccountLogin(ctx, h.loginUserID(ctx, payload.EventPayload))
	h.writeOK(ctx, w)
}

// loginUserID decodes the base64-encoded event_payload and returns the
// session.user.checked payload's `userID` (the logging-in user). It tolerates
// event_payload arriving either as a base64 JSON string or as a raw JSON
// object. Any decode failure yields "" (skip), never an error.
func (h *LoginEventHandler) loginUserID(ctx context.Context, raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	inner := []byte(raw)
	// event_payload is base64-encoded (a JSON string) in Zitadel's payload; if
	// it arrives as a bare object instead, use it as-is.
	var b64 string
	if err := json.Unmarshal(raw, &b64); err == nil {
		decoded, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			h.logger.Warn(ctx, "login-event: failed to base64-decode event_payload, skipping account.login",
				slog.String("error", err.Error()),
			)
			return ""
		}
		inner = decoded
	}

	var data sessionUserCheckedPayload
	if err := json.Unmarshal(inner, &data); err != nil {
		h.logger.Warn(ctx, "login-event: failed to unmarshal event_payload, skipping account.login",
			slog.String("error", err.Error()),
		)
		return ""
	}
	return data.UserID
}

// emitAccountLogin resolves the Zitadel `sub` to the platform UserID and
// publishes `ACCOUNT.login` best-effort. Every failure mode — an absent
// identifier, a lookup miss/error, or a publish error — is logged and
// swallowed; it never affects the HTTP response.
func (h *LoginEventHandler) emitAccountLogin(ctx context.Context, sub string) {
	if sub == "" {
		h.logger.Warn(ctx, "login-event: event_payload missing userID, skipping account.login")
		return
	}

	user, err := h.users.GetByExternalID(ctx, sub)
	if err != nil {
		// User not yet provisioned, or a transient lookup error. Bounded
		// under-count is acceptable; never fail login.
		h.logger.Warn(ctx, "login-event: sub to UserID lookup failed, skipping account.login",
			slog.String("error", err.Error()),
		)
		return
	}

	if err := h.publisher.PublishEvent(ctx, entity.SubjectAccountLogin, entity.AccountLoginData{UserID: user.ID}); err != nil {
		h.logger.Error(ctx, "login-event: failed to publish ACCOUNT.login", err)
	}
}

// writeOK writes the 200 response Zitadel receives. The body is an empty JSON
// object: an event execution is fire-and-forget and its return is ignored.
func (h *LoginEventHandler) writeOK(ctx context.Context, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write([]byte("{}")); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		h.logger.Error(ctx, "login-event: failed to write response", err)
	}
}
