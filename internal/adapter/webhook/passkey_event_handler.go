package webhook

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/zitadel/zitadel-go/v3/pkg/actions"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-logging/logging"
)

// Zitadel events the passkey-event Target is bound to. token.verified is a
// finished passkey registration (including one made with a recovery link) and
// carries the passkey's name; token.removed is a removed passkey. Neither the
// user.human.passwordless group (it also fires on every passkey sign-in) nor
// token.added (an unfinished registration challenge) is bound.
const (
	passkeyVerifiedEventType = "user.human.passwordless.token.verified"
	passkeyRemovedEventType  = "user.human.passwordless.token.removed"
)

// PasskeyEventHandler serves `POST /passkey-event` on the internal webhook
// listener. It is bound to two Zitadel Actions v2 EVENT executions, on
// user.human.passwordless.token.verified and token.removed, through a
// PAYLOAD_TYPE_JSON Target whose HMAC `ZITADEL-Signature` header it verifies
// with the Target's signing key.
//
// For a notice about an identity of the product organization it announces a
// passkey change (USER.passkey_changed) for UserUseCase.NotifyPasskeyChange
// and answers 200. Notices of other kinds or about identities of other
// organizations (Organizer members) are answered 200 without an announcement.
// When the announcement fails it answers 500; Zitadel does not deliver an
// event again, so that change is not emailed.
type PasskeyEventHandler struct {
	signingKey   string
	productOrgID string
	publisher    eventPublisher
	logger       *logging.Logger
}

// NewPasskeyEventHandler constructs a handler bound to the passkey-event
// Target's signing key and the product organization's Zitadel id. An empty
// signing key or productOrgID makes every request fail with 503, so a
// misconfigured workload announces nothing rather than unsigned notices or
// every organization's changes.
func NewPasskeyEventHandler(signingKey, productOrgID string, publisher eventPublisher, logger *logging.Logger) *PasskeyEventHandler {
	return &PasskeyEventHandler{signingKey: signingKey, productOrgID: productOrgID, publisher: publisher, logger: logger}
}

// passkeyEventPayload is the subset of the Actions v2 event-execution JSON
// this handler reads. aggregateID is the identity whose passkey changed and
// resourceOwner its organization; event_payload is the base64-encoded event.
type passkeyEventPayload struct {
	AggregateID   string          `json:"aggregateID"`
	ResourceOwner string          `json:"resourceOwner"`
	EventType     string          `json:"event_type"`
	CreatedAt     time.Time       `json:"created_at"`
	EventPayload  json.RawMessage `json:"event_payload"`
}

// passkeyTokenPayload is the decoded event_payload of a token.verified or
// token.removed event. The name and AAGUID are present on token.verified only.
type passkeyTokenPayload struct {
	TokenID   string `json:"webAuthNTokenId"`
	TokenName string `json:"webAuthNTokenName"`
	// AAGUID is base64 in the JSON (a byte slice in Zitadel); encoding/json
	// decodes it.
	AAGUID []byte `json:"aaguid"`
}

// ServeHTTP implements http.Handler.
func (h *PasskeyEventHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		h.logger.Error(ctx, "passkey-event: signing key is not configured; announcing nothing",
			errors.New("WEBHOOK_PASSKEY_EVENT_SIGNING_KEY is empty"))
		http.Error(w, "not configured", http.StatusServiceUnavailable)
		return
	}
	if h.productOrgID == "" {
		h.logger.Error(ctx, "passkey-event: product organization id is not configured; announcing nothing",
			errors.New("ZITADEL_PRODUCT_ORG_ID is empty"))
		http.Error(w, "not configured", http.StatusServiceUnavailable)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil || len(body) == 0 {
		h.logger.Warn(ctx, "passkey-event: missing or unreadable body")
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if err := actions.ValidateRequestPayload(body, &r.Header, h.signingKey); err != nil {
		h.logger.Warn(ctx, "passkey-event: webhook signature validation failed",
			slog.String("error", err.Error()))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var payload passkeyEventPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		h.logger.Error(ctx, "passkey-event: failed to unmarshal payload", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	var kind entity.PasskeyChangeKind
	switch payload.EventType {
	case passkeyVerifiedEventType:
		kind = entity.PasskeyChangeAdded
	case passkeyRemovedEventType:
		kind = entity.PasskeyChangeRemoved
	default:
		h.logger.Warn(ctx, "passkey-event: unexpected event_type, announcing nothing",
			slog.String("event_type", payload.EventType))
		h.writeOK(ctx, w)
		return
	}
	if payload.ResourceOwner != h.productOrgID {
		// An Organizer member's identity: its passkeys are not a fan's.
		h.writeOK(ctx, w)
		return
	}
	if payload.AggregateID == "" {
		h.logger.Warn(ctx, "passkey-event: notice has no identity, announcing nothing",
			slog.String("event_type", payload.EventType))
		h.writeOK(ctx, w)
		return
	}

	token, err := decodePasskeyToken(payload.EventPayload)
	if err != nil {
		h.logger.Warn(ctx, "passkey-event: failed to decode event_payload, announcing nothing",
			slog.String("error", err.Error()), slog.String("provider_user_id", payload.AggregateID))
		h.writeOK(ctx, w)
		return
	}

	data := entity.UserPasskeyChangedData{
		ProviderUserID:    payload.AggregateID,
		ProviderPasskeyID: token.TokenID,
		Kind:              string(kind),
		ChangeTime:        payload.CreatedAt,
	}
	if kind == entity.PasskeyChangeAdded {
		data.Name = token.TokenName
		data.AAGUID = formatAAGUID(token.AAGUID)
	}
	if data.ChangeTime.IsZero() {
		data.ChangeTime = time.Now()
	}

	if err := h.publisher.PublishEvent(ctx, entity.SubjectUserPasskeyChanged, data); err != nil {
		h.logger.Error(ctx, "passkey-event: failed to announce the passkey change; it is not emailed", err,
			slog.String("provider_user_id", data.ProviderUserID),
			slog.String("kind", data.Kind))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.logger.Info(ctx, "passkey-event: passkey change announced",
		slog.String("provider_user_id", data.ProviderUserID),
		slog.String("kind", data.Kind))
	h.writeOK(ctx, w)
}

// decodePasskeyToken decodes event_payload, a base64 JSON string in
// Zitadel's payload or, tolerated, a bare JSON object.
func decodePasskeyToken(raw json.RawMessage) (passkeyTokenPayload, error) {
	var token passkeyTokenPayload
	if len(raw) == 0 {
		return token, errors.New("event_payload is empty")
	}
	inner := []byte(raw)
	var b64 string
	if err := json.Unmarshal(raw, &b64); err == nil {
		decoded, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return token, fmt.Errorf("base64-decode event_payload: %w", err)
		}
		inner = decoded
	}
	if err := json.Unmarshal(inner, &token); err != nil {
		return token, fmt.Errorf("unmarshal event_payload: %w", err)
	}
	return token, nil
}

// formatAAGUID returns the 16-byte AAGUID in canonical lower-case UUID form,
// or "" when it is absent or not 16 bytes.
func formatAAGUID(b []byte) string {
	id, err := uuid.FromBytes(b)
	if err != nil {
		return ""
	}
	return id.String()
}

// writeOK writes the 200 response; Zitadel ignores an event execution's
// response body.
func (h *PasskeyEventHandler) writeOK(ctx context.Context, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write([]byte("{}")); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		h.logger.Error(ctx, "passkey-event: failed to write response", err)
	}
}
