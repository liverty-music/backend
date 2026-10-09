package mail_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/mail"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeOrders is an in-memory OrderStore.
type fakeOrders struct {
	mu     sync.Mutex
	orders map[entity.OrderID]*entity.Order
}

func (f *fakeOrders) Get(_ context.Context, id entity.OrderID) (*entity.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.orders[id]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	copied := *o
	return &copied, nil
}

func (f *fakeOrders) MarkConfirmationSent(_ context.Context, id entity.OrderID, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if o := f.orders[id]; o.ConfirmationSentTime == nil {
		o.ConfirmationSentTime = &at
	}
	return nil
}

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

var (
	sentAt = time.Date(2026, 11, 5, 18, 20, 0, 0, time.UTC)
	msg    = entity.OrderConfirmationEmail{Subject: "ご購入ありがとうございます", TextBody: "本文"}
)

func newSender(t *testing.T, stub *stubPostmark, orders *fakeOrders) *mail.PostmarkConfirmationSender {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(srv.Close)
	logger, err := logging.New()
	require.NoError(t, err)
	return mail.NewPostmarkConfirmationSenderForTest(orders, srv.URL, "server-token", func() time.Time { return sentAt }, logger)
}

func TestPostmarkConfirmationSender_SendConfirmationEmail(t *testing.T) {
	t.Parallel()

	t.Run("email sent", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order/send-confirmation-email "Email sent"
		orders := &fakeOrders{orders: map[entity.OrderID]*entity.Order{"order-1": {ID: "order-1"}}}
		stub := &stubPostmark{status: http.StatusOK}
		sender := newSender(t, stub, orders)

		require.NoError(t, sender.SendConfirmationEmail(context.Background(), "order-1", "fan@example.com", msg))

		require.Len(t, stub.sent, 1)
		assert.Equal(t, "fan@example.com", stub.sent[0]["To"])
		assert.Equal(t, "outbound", stub.sent[0]["MessageStream"])
		assert.Equal(t, "server-token", stub.tokens[0])
		got, _ := orders.Get(context.Background(), "order-1")
		assert.Equal(t, &sentAt, got.ConfirmationSentTime)
	})

	t.Run("redelivered event", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order/send-confirmation-email "Redelivered event"
		earlier := sentAt.Add(-time.Hour)
		orders := &fakeOrders{orders: map[entity.OrderID]*entity.Order{"order-1": {ID: "order-1", ConfirmationSentTime: &earlier}}}
		stub := &stubPostmark{status: http.StatusOK}
		sender := newSender(t, stub, orders)

		require.NoError(t, sender.SendConfirmationEmail(context.Background(), "order-1", "fan@example.com", msg))

		assert.Empty(t, stub.sent)
	})

	t.Run("mail unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/order/send-confirmation-email "Mail unavailable"
		orders := &fakeOrders{orders: map[entity.OrderID]*entity.Order{"order-1": {ID: "order-1"}}}
		stub := &stubPostmark{status: http.StatusInternalServerError}
		sender := newSender(t, stub, orders)

		err := sender.SendConfirmationEmail(context.Background(), "order-1", "fan@example.com", msg)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		got, _ := orders.Get(context.Background(), "order-1")
		assert.Nil(t, got.ConfirmationSentTime)
	})

	t.Run("empty address", func(t *testing.T) {
		t.Parallel()
		orders := &fakeOrders{orders: map[entity.OrderID]*entity.Order{"order-1": {ID: "order-1"}}}
		sender := newSender(t, &stubPostmark{status: http.StatusOK}, orders)

		err := sender.SendConfirmationEmail(context.Background(), "order-1", "", msg)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("unknown order", func(t *testing.T) {
		t.Parallel()
		sender := newSender(t, &stubPostmark{status: http.StatusOK}, &fakeOrders{orders: map[entity.OrderID]*entity.Order{}})

		err := sender.SendConfirmationEmail(context.Background(), "order-x", "fan@example.com", msg)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

// TestPostmarkConfirmationSender_TestServerToken sends through the real
// Postmark API with its documented test token, which validates the request
// without delivering it. Opt-in, since it reaches the network:
//
//	POSTMARK_INTEGRATION_TEST=1 go test ./internal/infrastructure/mail/ -run TestServerToken -v
func TestPostmarkConfirmationSender_TestServerToken(t *testing.T) {
	if os.Getenv("POSTMARK_INTEGRATION_TEST") != "1" {
		t.Skip("opt-in: set POSTMARK_INTEGRATION_TEST=1 to run against Postmark's test server token")
	}
	logger, err := logging.New()
	require.NoError(t, err)
	orders := &fakeOrders{orders: map[entity.OrderID]*entity.Order{"order-1": {ID: "order-1"}}}
	sender := mail.NewPostmarkConfirmationSender(orders, "POSTMARK_API_TEST",
		"noreply@mail.dev.liverty-music.app", "outbound", func() time.Time { return sentAt }, logger)

	require.NoError(t, sender.SendConfirmationEmail(context.Background(), "order-1", "test@blackhole.postmarkapp.com", msg))

	got, _ := orders.Get(context.Background(), "order-1")
	assert.NotNil(t, got.ConfirmationSentTime)
}
