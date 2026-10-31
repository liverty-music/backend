package webhook

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/zitadel/zitadel-go/v3/pkg/actions"

	"github.com/pannpers/go-logging/logging"
)

// sessionUserCheckedEventType is the Zitadel event this handler is bound to. It
// is stored once per interactive login through the hosted Login UI (a user is
// verified into a session); a silent refresh_token grant touches only the
// oidc_session aggregate and a machine jwt_profile grant never creates a
// Login-UI session, so this event is login-specific and human-specific by
// construction. Determined empirically via the Zitadel Events API.
const sessionUserCheckedEventType = "session.user.checked"

// loginRecorder records a reported sign-in for its identity
// (usecase.UserUseCase.RecordLogin).
type loginRecorder interface {
	RecordLogin(ctx context.Context, providerUserID string, at time.Time) error
}

// LoginEventHandler serves `POST /account-login-event`. It is bound to a
// Zitadel Actions v2 EVENT execution on `event.event = "session.user.checked"`.
// Unlike a request/response (method) execution — whose webhook return REPLACES
// the API payload and previously broke sign-in — an event execution is
// fire-and-forget: it fires after the event is persisted and Zitadel ignores
// the response, so it can never affect login. The handler records each
// interactive login with UserUseCase.RecordLogin, whose User.RecordLogin
// announces it for product analytics.
//
// The Target is provisioned with PAYLOAD_TYPE_JSON (the Zitadel-recommended
// default): the body is the raw event JSON, signed with an HMAC `ZITADEL-Signature`
// header the handler verifies with the Target's signing key. (PAYLOAD_TYPE_JWT
// is not used because an event-execution JWT target requires an active instance
// web key, which this instance does not have — it fails with Errors.WebKey.NoActive.)
//
// A wrong event type, a missing user identifier or an unreadable payload is
// logged and answered 200 with an empty JSON body, recording nothing. When
// RecordLogin fails the handler answers 500; Zitadel does not deliver the
// event again, so that login is not recorded.
type LoginEventHandler struct {
	signingKey string
	logins     loginRecorder
	logger     *logging.Logger
}

// NewLoginEventHandler constructs a handler bound to the given Target signing
// key and login recorder. The signing key MUST match the key Zitadel
// generated for the login-event Target; an empty key makes every request fail
// with 503.
func NewLoginEventHandler(
	signingKey string,
	logins loginRecorder,
	logger *logging.Logger,
) *LoginEventHandler {
	return &LoginEventHandler{signingKey: signingKey, logins: logins, logger: logger}
}

// eventExecutionPayload captures the subset of the Actions v2 event-execution
// JSON payload this handler consumes. The top-level `userID` is the event
// EDITOR (for session.user.checked, the Login-UI service user), NOT the person
// logging in — the login user lives inside `event_payload`, which is
// base64-encoded. Unknown fields are ignored.
type eventExecutionPayload struct {
	EventType    string          `json:"event_type"`
	EventPayload json.RawMessage `json:"event_payload"`
	// CreatedAt is when Zitadel recorded the event, the time of the login.
	CreatedAt time.Time `json:"created_at"`
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

	// An empty key would not reject anything: actions.ValidateRequestPayload
	// computes the HMAC with whatever key it is given, so a body signed with
	// "" would verify.
	if h.signingKey == "" {
		h.logger.Error(ctx, "login-event: signing key is not configured; recording nothing",
			errors.New("WEBHOOK_LOGIN_EVENT_SIGNING_KEY is empty"))
		http.Error(w, "not configured", http.StatusServiceUnavailable)
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

	// Verify the HMAC ZITADEL-Signature header against the raw JSON body using
	// the Target's signing key. This is the security boundary alongside the
	// private, internal-only webhook listener.
	if err := actions.ValidateRequestPayload(body, &r.Header, h.signingKey); err != nil {
		h.logger.Warn(ctx, "login-event: webhook signature validation failed",
			slog.String("error", err.Error()),
		)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var payload eventExecutionPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		h.logger.Error(ctx, "login-event: failed to unmarshal payload", err)
		h.writeOK(ctx, w)
		return
	}

	if payload.EventType != sessionUserCheckedEventType {
		// The Execution is bound to session.user.checked, but guard anyway so a
		// mis-bound Target never emits a wrong login.
		h.logger.Warn(ctx, "login-event: unexpected event_type, recording nothing",
			slog.String("event_type", payload.EventType),
		)
		h.writeOK(ctx, w)
		return
	}

	sub := h.loginUserID(ctx, payload.EventPayload)
	if sub == "" {
		h.logger.Warn(ctx, "login-event: event_payload missing userID, recording nothing")
		h.writeOK(ctx, w)
		return
	}
	loginTime := payload.CreatedAt
	if loginTime.IsZero() {
		loginTime = time.Now()
	}
	if err := h.logins.RecordLogin(ctx, sub, loginTime); err != nil {
		h.logger.Error(ctx, "login-event: failed to record the login; it is not recorded", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
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
			h.logger.Warn(ctx, "login-event: failed to base64-decode event_payload, recording nothing",
				slog.String("error", err.Error()),
			)
			return ""
		}
		inner = decoded
	}

	var data sessionUserCheckedPayload
	if err := json.Unmarshal(inner, &data); err != nil {
		h.logger.Warn(ctx, "login-event: failed to unmarshal event_payload, recording nothing",
			slog.String("error", err.Error()),
		)
		return ""
	}
	return data.UserID
}

// writeOK writes the 200 response Zitadel receives. The body is an empty JSON
// object: an event execution is fire-and-forget and its return is ignored.
func (h *LoginEventHandler) writeOK(ctx context.Context, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write([]byte("{}")); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		h.logger.Error(ctx, "login-event: failed to write response", err)
	}
}
