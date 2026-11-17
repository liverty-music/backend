package mail_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/mail"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubPostmark answers with a fixed status and counts the emails it got.
type stubPostmark struct {
	mu     sync.Mutex
	status int
	sent   []map[string]any
	tokens []string
}

func (s *stubPostmark) handler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.sent = append(s.sent, body)
	s.tokens = append(s.tokens, r.Header.Get("X-Postmark-Server-Token"))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(s.status)
	if s.status == http.StatusOK {
		_ = json.NewEncoder(w).Encode(map[string]any{"ErrorCode": 0, "Message": "OK", "MessageID": "msg-1"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ErrorCode": 500, "Message": "unavailable"})
}

var msg = entity.OrderConfirmationEmail{Subject: "ご購入ありがとうございます", TextBody: "本文"}

func newSender(t *testing.T, stub *stubPostmark) *mail.PostmarkOrderMailer {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(srv.Close)
	logger, err := logging.New()
	require.NoError(t, err)
	return mail.NewPostmarkOrderMailerForTest(srv.URL, "server-token", logger)
}

func TestPostmarkOrderMailer_SendConfirmationEmail(t *testing.T) {
	t.Parallel()

	t.Run("email sent", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order/send-confirmation-email "Email sent"
		stub := &stubPostmark{status: http.StatusOK}
		sender := newSender(t, stub)

		require.NoError(t, sender.SendConfirmationEmail(context.Background(), "order-1", "fan@example.com", msg))

		require.Len(t, stub.sent, 1)
		assert.Equal(t, "fan@example.com", stub.sent[0]["To"])
		assert.Equal(t, msg.Subject, stub.sent[0]["Subject"])
		assert.Equal(t, msg.TextBody, stub.sent[0]["TextBody"])
		assert.Equal(t, "order-confirmation", stub.sent[0]["Tag"])
		assert.Equal(t, map[string]any{"order_id": "order-1"}, stub.sent[0]["Metadata"])
		assert.Equal(t, "outbound", stub.sent[0]["MessageStream"])
		assert.Equal(t, "server-token", stub.tokens[0])
	})

	t.Run("mail unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order/send-confirmation-email "Mail unavailable"
		sender := newSender(t, &stubPostmark{status: http.StatusInternalServerError})

		err := sender.SendConfirmationEmail(context.Background(), "order-1", "fan@example.com", msg)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})

	t.Run("black hole address is not sent", func(t *testing.T) {
		t.Parallel()
		stub := &stubPostmark{status: http.StatusOK}
		sender := newSender(t, stub)

		require.NoError(t, sender.SendConfirmationEmail(context.Background(), "order-1", "e2e-test-password@Blackhole.Postmarkapp.com", msg))

		assert.Empty(t, stub.sent)
	})

	t.Run("empty address", func(t *testing.T) {
		t.Parallel()
		stub := &stubPostmark{status: http.StatusOK}
		sender := newSender(t, stub)

		err := sender.SendConfirmationEmail(context.Background(), "order-1", "", msg)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Empty(t, stub.sent)
	})

	t.Run("empty message", func(t *testing.T) {
		t.Parallel()
		stub := &stubPostmark{status: http.StatusOK}
		sender := newSender(t, stub)

		err := sender.SendConfirmationEmail(context.Background(), "order-1", "fan@example.com", entity.OrderConfirmationEmail{Subject: msg.Subject})

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Empty(t, stub.sent)
	})
}

var notice = entity.OrderEmail{Subject: "公演中止のお知らせ", TextBody: "本文"}

func TestPostmarkOrderMailer_SendCancellationEmail(t *testing.T) {
	t.Parallel()

	t.Run("email sent", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order/send-cancellation-email "Email sent"
		stub := &stubPostmark{status: http.StatusOK}
		sender := newSender(t, stub)

		require.NoError(t, sender.SendCancellationEmail(context.Background(), "order-1", "fan@example.com", notice))

		require.Len(t, stub.sent, 1)
		assert.Equal(t, "fan@example.com", stub.sent[0]["To"])
		assert.Equal(t, notice.Subject, stub.sent[0]["Subject"])
		assert.Equal(t, notice.TextBody, stub.sent[0]["TextBody"])
		assert.Equal(t, "order-cancellation", stub.sent[0]["Tag"])
	})

	t.Run("address missing", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order/send-cancellation-email "Address missing"
		stub := &stubPostmark{status: http.StatusOK}
		sender := newSender(t, stub)

		err := sender.SendCancellationEmail(context.Background(), "order-1", "", notice)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Empty(t, stub.sent)
	})

	t.Run("message missing", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order/send-cancellation-email "Message missing"
		stub := &stubPostmark{status: http.StatusOK}
		sender := newSender(t, stub)

		err := sender.SendCancellationEmail(context.Background(), "order-1", "fan@example.com", entity.OrderEmail{})

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Empty(t, stub.sent)
	})

	t.Run("mail unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order/send-cancellation-email "Mail unavailable"
		sender := newSender(t, &stubPostmark{status: http.StatusInternalServerError})

		err := sender.SendCancellationEmail(context.Background(), "order-1", "fan@example.com", notice)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})
}

func TestPostmarkOrderMailer_SendRefundEmail(t *testing.T) {
	t.Parallel()

	t.Run("email sent", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order/send-refund-email "Email sent"
		stub := &stubPostmark{status: http.StatusOK}
		sender := newSender(t, stub)

		require.NoError(t, sender.SendRefundEmail(context.Background(), "order-1", "fan@example.com", notice))

		require.Len(t, stub.sent, 1)
		assert.Equal(t, "fan@example.com", stub.sent[0]["To"])
		assert.Equal(t, notice.TextBody, stub.sent[0]["TextBody"])
		assert.Equal(t, "order-refund", stub.sent[0]["Tag"])
	})

	t.Run("message missing", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order/send-refund-email "Message missing"
		stub := &stubPostmark{status: http.StatusOK}
		sender := newSender(t, stub)

		err := sender.SendRefundEmail(context.Background(), "order-1", "fan@example.com", entity.OrderEmail{})

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Empty(t, stub.sent)
	})

	t.Run("address missing", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order/send-refund-email "Address missing"
		stub := &stubPostmark{status: http.StatusOK}
		sender := newSender(t, stub)

		err := sender.SendRefundEmail(context.Background(), "order-1", "", notice)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Empty(t, stub.sent)
	})

	t.Run("mail unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order/send-refund-email "Mail unavailable"
		sender := newSender(t, &stubPostmark{status: http.StatusInternalServerError})

		err := sender.SendRefundEmail(context.Background(), "order-1", "fan@example.com", notice)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})
}

// TestPostmarkOrderMailer_TestServerToken sends through the real
// Postmark API with its documented test token, which validates the request
// without delivering it. Opt-in, since it reaches the network:
//
//	POSTMARK_INTEGRATION_TEST=1 go test ./internal/infrastructure/mail/ -run TestServerToken -v
func TestPostmarkOrderMailer_TestServerToken(t *testing.T) {
	if os.Getenv("POSTMARK_INTEGRATION_TEST") != "1" {
		t.Skip("opt-in: set POSTMARK_INTEGRATION_TEST=1 to run against Postmark's test server token")
	}
	logger, err := logging.New()
	require.NoError(t, err)
	sender := mail.NewPostmarkOrderMailer("POSTMARK_API_TEST",
		"noreply@mail.dev.liverty-music.app", "outbound", logger)

	// Not a black hole address: the client skips those without a request.
	// The test token delivers nothing whatever the address.
	require.NoError(t, sender.SendConfirmationEmail(context.Background(), "order-1", "fan@example.com", msg))
}
