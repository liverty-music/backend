// Package mail sends transactional email through Postmark's HTTP API, on the
// same Postmark account (and sender domain) Zitadel already uses. The server
// API token is provisioned in GCP Secret Manager and injected through ESO as
// POSTMARK_SERVER_TOKEN.
package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// postmarkAPIURL is Postmark's single-email endpoint.
const postmarkAPIURL = "https://api.postmarkapp.com/email"

// postmarkHTTPTimeout bounds every Postmark call.
const postmarkHTTPTimeout = 15 * time.Second

// orderConfirmationTag tags confirmation emails in Postmark's activity log.
const orderConfirmationTag = "order-confirmation"

// OrderStore is what the mailer needs from the Order repository.
type OrderStore interface {
	Get(ctx context.Context, id entity.OrderID) (*entity.Order, error)
	MarkConfirmationSent(ctx context.Context, id entity.OrderID, at time.Time) error
}

// PostmarkConfirmationSender implements [entity.OrderConfirmationSender] with
// Postmark's transactional message stream.
type PostmarkConfirmationSender struct {
	orders        OrderStore
	httpClient    *http.Client
	apiURL        string
	serverToken   string
	fromAddress   string
	messageStream string
	clock         func() time.Time
	logger        *logging.Logger
}

// Compile-time interface compliance check.
var _ entity.OrderConfirmationSender = (*PostmarkConfirmationSender)(nil)

// NewPostmarkConfirmationSender creates a sender for the given server token,
// From address and message stream.
func NewPostmarkConfirmationSender(orders OrderStore, serverToken, fromAddress, messageStream string, clock func() time.Time, logger *logging.Logger) *PostmarkConfirmationSender {
	return newPostmarkConfirmationSender(orders, postmarkAPIURL, serverToken, fromAddress, messageStream, clock, logger)
}

func newPostmarkConfirmationSender(orders OrderStore, apiURL, serverToken, fromAddress, messageStream string, clock func() time.Time, logger *logging.Logger) *PostmarkConfirmationSender {
	return &PostmarkConfirmationSender{
		orders:        orders,
		httpClient:    &http.Client{Timeout: postmarkHTTPTimeout},
		apiURL:        apiURL,
		serverToken:   serverToken,
		fromAddress:   fromAddress,
		messageStream: messageStream,
		clock:         clock,
		logger:        logger,
	}
}

// SendConfirmationEmail implements [entity.OrderConfirmationSender].
func (s *PostmarkConfirmationSender) SendConfirmationEmail(ctx context.Context, orderID entity.OrderID, to string, msg entity.OrderConfirmationEmail) error {
	if to == "" || msg.Subject == "" || msg.TextBody == "" {
		return apperr.New(codes.InvalidArgument, "the address and the message must not be empty",
			slog.String("order_id", string(orderID)))
	}
	order, err := s.orders.Get(ctx, orderID)
	if err != nil {
		return err
	}
	if order.ConfirmationSentTime != nil {
		return nil
	}
	if err := s.send(ctx, orderID, to, msg); err != nil {
		return err
	}
	return s.orders.MarkConfirmationSent(ctx, orderID, s.clock())
}

// postmarkEmail is the request body of Postmark's single-email endpoint.
type postmarkEmail struct {
	From          string            `json:"From"`
	To            string            `json:"To"`
	Subject       string            `json:"Subject"`
	TextBody      string            `json:"TextBody"`
	Tag           string            `json:"Tag"`
	MessageStream string            `json:"MessageStream"`
	Metadata      map[string]string `json:"Metadata"`
}

// postmarkResponse is the response body of Postmark's single-email endpoint.
type postmarkResponse struct {
	ErrorCode int    `json:"ErrorCode"`
	Message   string `json:"Message"`
	MessageID string `json:"MessageID"`
}

// send posts one email to Postmark.
func (s *PostmarkConfirmationSender) send(ctx context.Context, orderID entity.OrderID, to string, msg entity.OrderConfirmationEmail) error {
	attrs := []slog.Attr{slog.String("order_id", string(orderID))}
	body, err := json.Marshal(postmarkEmail{
		From:          s.fromAddress,
		To:            to,
		Subject:       msg.Subject,
		TextBody:      msg.TextBody,
		Tag:           orderConfirmationTag,
		MessageStream: s.messageStream,
		Metadata:      map[string]string{"order_id": string(orderID)},
	})
	if err != nil {
		return apperr.Wrap(err, codes.Internal, "failed to encode the email", attrs...)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.apiURL, bytes.NewReader(body))
	if err != nil {
		return apperr.Wrap(err, codes.Internal, "failed to build the Postmark request", attrs...)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Postmark-Server-Token", s.serverToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return apperr.Wrap(err, codes.Unavailable, "email cannot be sent", attrs...)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	var result postmarkResponse
	_ = json.Unmarshal(raw, &result)
	if resp.StatusCode == http.StatusOK && result.ErrorCode == 0 {
		s.logger.Info(ctx, "order confirmation email sent",
			slog.String("order_id", string(orderID)),
			slog.String("postmark_message_id", result.MessageID),
		)
		return nil
	}

	cause := fmt.Errorf("postmark: HTTP %d, error code %d: %s", resp.StatusCode, result.ErrorCode, result.Message)
	s.logger.Warn(ctx, "order confirmation email rejected by Postmark",
		slog.String("order_id", string(orderID)),
		slog.Int("http_status", resp.StatusCode),
		slog.Int("postmark_error_code", result.ErrorCode),
	)
	// A request Postmark refuses on its merits (422) is not retried into
	// success; anything else (5xx, 429, auth) means mail cannot be sent now.
	if resp.StatusCode == http.StatusUnprocessableEntity {
		return apperr.Wrap(cause, codes.InvalidArgument, "Postmark rejected the email", attrs...)
	}
	return apperr.Wrap(cause, codes.Unavailable, "email cannot be sent", attrs...)
}

// NoopConfirmationSender is used when no Postmark token is configured (local
// development): every call fails with Unavailable.
type NoopConfirmationSender struct {
	logger *logging.Logger
}

// Compile-time interface compliance check.
var _ entity.OrderConfirmationSender = (*NoopConfirmationSender)(nil)

// NewNoopConfirmationSender creates a NoopConfirmationSender.
func NewNoopConfirmationSender(logger *logging.Logger) *NoopConfirmationSender {
	return &NoopConfirmationSender{logger: logger}
}

// SendConfirmationEmail returns Unavailable because no Postmark token is set.
func (s *NoopConfirmationSender) SendConfirmationEmail(ctx context.Context, orderID entity.OrderID, to string, msg entity.OrderConfirmationEmail) error {
	s.logger.Warn(ctx, "order confirmation email skipped: POSTMARK_SERVER_TOKEN is not configured",
		slog.String("order_id", string(orderID)))
	return apperr.New(codes.Unavailable, "email is not configured")
}
