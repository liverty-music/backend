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

// TestSender_Send tests the Send method of the webpush Sender across the
// relevant HTTP response paths.
func TestSender_Send(t *testing.T) {
	// VAPID test keys: the private key is the same short value used by
	// the upstream webpush-go test suite. It decodes to a valid P-256
	// scalar so VAPID JWT signing succeeds without needing real keys.
	const (
		testVAPIDPublic  = "testPublic"
		testVAPIDPrivate = "testKey"
		testVAPIDContact = "mailto:test@example.com"
	)

	tests := []struct {
		name         string
		statusCode   int
		responseBody string
		wantErr      error
		// check performs additional assertions beyond wantErr matching.
		check func(t *testing.T, err error)
	}{
		{
			name:       "return nil when server returns 201 Created",
			statusCode: http.StatusCreated,
			wantErr:    nil,
		},
		{
			name:       "return ErrNotFound when server returns 410 Gone",
			statusCode: http.StatusGone,
			wantErr:    apperr.ErrNotFound,
		},
		{
			name:       "return ErrInternal when server returns 500",
			statusCode: http.StatusInternalServerError,
			wantErr:    apperr.ErrInternal,
		},
		{
			name:         "return ErrInternal with responseBody attr when server returns 400 with body",
			statusCode:   http.StatusBadRequest,
			responseBody: "invalid subscription endpoint",
			wantErr:      apperr.ErrInternal,
			check: func(t *testing.T, err error) {
				t.Helper()
				var appErr *apperr.AppErr
				if assert.ErrorAs(t, err, &appErr) {
					assert.Contains(t, appErr.Msg, "400", "error message should include status code")
					assert.NotEmpty(t, appErr.Attrs, "responseBody attr should be present for 4xx with body")
				}
			},
		},
		{
			name: "return ErrNotFound without body attrs when server returns 410 Gone",
			// Body is intentionally non-empty to confirm the 410 path returns early
			// before body capture, so Attrs must remain empty.
			statusCode:   http.StatusGone,
			responseBody: "gone",
			wantErr:      apperr.ErrNotFound,
			check: func(t *testing.T, err error) {
				t.Helper()
				var appErr *apperr.AppErr
				if assert.ErrorAs(t, err, &appErr) {
					assert.Empty(t, appErr.Attrs, "410 Gone path should not capture response body")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				if tt.responseBody != "" {
					_, _ = w.Write([]byte(tt.responseBody))
				}
			}))
			defer server.Close()

			sender := webpush.NewSender(testVAPIDPublic, testVAPIDPrivate, testVAPIDContact)
			sub := testSubscription(t, server.URL)

			err := sender.Send(context.Background(), []byte("test payload"), sub)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}

			if tt.check != nil {
				tt.check(t, err)
			}
		})
	}
}
