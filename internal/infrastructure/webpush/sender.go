// Package webpush provides a PushNotificationSender backed by the webpush-go library.
package webpush

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	wpush "github.com/SherClockHolmes/webpush-go"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/httpx"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// Sender implements entity.PushNotificationSender using the webpush-go library.
type Sender struct {
	httpClient     *http.Client
	vapidPublicKey string
	vapidPrivate   string
	vapidContact   string
}

// Compile-time interface compliance check.
var _ entity.PushNotificationSender = (*Sender)(nil)

// NewSender creates a new webpush Sender with VAPID credentials.
// vapidContact must be a mailto: URI (e.g., "mailto:admin@example.com").
func NewSender(vapidPublicKey, vapidPrivateKey, vapidContact string) *Sender {
	return &Sender{
		httpClient:     &http.Client{Timeout: 10 * time.Second},
		vapidPublicKey: vapidPublicKey,
		vapidPrivate:   vapidPrivateKey,
		vapidContact:   vapidContact,
	}
}

// Send delivers the payload to the given push subscription endpoint.
func (s *Sender) Send(_ context.Context, payload []byte, sub *entity.PushSubscription) error {
	resp, err := wpush.SendNotification(payload, &wpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys: wpush.Keys{
			P256dh: sub.P256dh,
			Auth:   sub.Auth,
		},
	}, &wpush.Options{
		HTTPClient:      s.httpClient,
		VAPIDPublicKey:  s.vapidPublicKey,
		VAPIDPrivateKey: s.vapidPrivate,
		Subscriber:      s.vapidContact,
	})

	if resp != nil {
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode == http.StatusGone {
			return apperr.New(codes.NotFound, "push subscription is no longer valid")
		}
		if resp.StatusCode >= http.StatusBadRequest {
			var attrs []slog.Attr
			if body := httpx.CaptureResponseBody(resp.Body); body != "" {
				attrs = append(attrs, slog.String("responseBody", body))
			}
			return apperr.New(codes.Internal, fmt.Sprintf("push service returned status %d", resp.StatusCode), attrs...)
		}
	}

	if err != nil {
		return apperr.Wrap(err, codes.Internal, "send push notification")
	}
	return nil
}
