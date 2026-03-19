package webpush_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/webpush"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
)

// testSubscription returns a PushSubscription whose Endpoint points to the
// given server URL. The P256dh and Auth values are taken from the
// webpush-go library's own test fixtures and represent a valid browser
// subscription for encryption purposes.
func testSubscription(t *testing.T, endpointURL string) *entity.PushSubscription {
	t.Helper()
	return &entity.PushSubscription{
		ID:       "test-id",
		UserID:   "user-1",
		Endpoint: endpointURL,
		P256dh:   "BNNL5ZaTfK81qhXOx23-wewhigUeFb632jN6LvRWCFH1ubQr77FE_9qV1FuojuRmHP42zmf34rXgW80OvUVDgTk",
		Auth:     "zqbxT6JKstKSY9JKibZLSQ",
	}
}

// TestSender_Send tests the Send method of the webpush Sender across the three
// relevant HTTP response paths.
func TestSender_Send(t *testing.T) {
	// VAPID test keys: the private key is the same short value used by
	// the upstream webpush-go test suite.  It decodes to a valid P-256
	// scalar so VAPID JWT signing succeeds without needing real keys.
	const (
		testVAPIDPublic  = "testPublic"
		testVAPIDPrivate = "testKey"
		testVAPIDContact = "mailto:test@example.com"
	)

	tests := []struct {
		name       string
		statusCode int
		wantErr    error
	}{
		{
			name:       "success - server returns 201 Created",
			statusCode: http.StatusCreated,
			wantErr:    nil,
		},
		{
			name:       "not found - server returns 410 Gone",
			statusCode: http.StatusGone,
			wantErr:    apperr.ErrNotFound,
		},
		{
			name:       "internal error - server returns 500",
			statusCode: http.StatusInternalServerError,
			wantErr:    apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			sender := webpush.NewSender(testVAPIDPublic, testVAPIDPrivate, testVAPIDContact)
			sub := testSubscription(t, server.URL)

			err := sender.Send(context.Background(), []byte("test payload"), sub)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			assert.NoError(t, err)
		})
	}
}
