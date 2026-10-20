package mail

import (
	"context"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// resaleSoldTag tags "your ticket sold" emails in Postmark's activity log.
const resaleSoldTag = "resale-sold"

// ResaleListingStore is what the mailer needs from the ResaleListing
// repository.
type ResaleListingStore interface {
	Get(ctx context.Context, id entity.ResaleListingID) (*entity.ResaleListing, error)
	MarkSoldEmailSent(ctx context.Context, id entity.ResaleListingID, at time.Time) error
}

// PostmarkResaleSoldSender implements [entity.ResaleListingSoldEmailSender]
// with Postmark's transactional message stream.
type PostmarkResaleSoldSender struct {
	postmarkClient
	listings ResaleListingStore
	clock    func() time.Time
}

// Compile-time interface compliance check.
var _ entity.ResaleListingSoldEmailSender = (*PostmarkResaleSoldSender)(nil)

// NewPostmarkResaleSoldSender creates a sender for the given server token,
// From address and message stream.
func NewPostmarkResaleSoldSender(listings ResaleListingStore, serverToken, fromAddress, messageStream string, clock func() time.Time, logger *logging.Logger) *PostmarkResaleSoldSender {
	return newPostmarkResaleSoldSender(listings, postmarkAPIURL, serverToken, fromAddress, messageStream, clock, logger)
}

func newPostmarkResaleSoldSender(listings ResaleListingStore, apiURL, serverToken, fromAddress, messageStream string, clock func() time.Time, logger *logging.Logger) *PostmarkResaleSoldSender {
	return &PostmarkResaleSoldSender{
		postmarkClient: newPostmarkClient(apiURL, serverToken, fromAddress, messageStream, logger),
		listings:       listings,
		clock:          clock,
	}
}

// SendSoldEmail implements [entity.ResaleListingSoldEmailSender].
func (s *PostmarkResaleSoldSender) SendSoldEmail(ctx context.Context, id entity.ResaleListingID, to string, msg entity.ResaleListingSoldEmail) error {
	attrs := slog.String("resale_listing_id", string(id))
	if to == "" || msg.Subject == "" || msg.TextBody == "" {
		return apperr.New(codes.InvalidArgument, "the address and the message must not be empty", attrs)
	}
	listing, err := s.listings.Get(ctx, id)
	if err != nil {
		return err
	}
	if listing.SoldEmailSendTime != nil {
		return nil
	}
	if err := s.post(ctx, to, msg.Subject, msg.TextBody, resaleSoldTag,
		map[string]string{"resale_listing_id": string(id)}, attrs); err != nil {
		return err
	}
	return s.listings.MarkSoldEmailSent(ctx, id, s.clock())
}

// NoopResaleSoldSender is used when no Postmark token is configured (local
// development): every call fails with Unavailable.
type NoopResaleSoldSender struct {
	logger *logging.Logger
}

// Compile-time interface compliance check.
var _ entity.ResaleListingSoldEmailSender = (*NoopResaleSoldSender)(nil)

// NewNoopResaleSoldSender creates a NoopResaleSoldSender.
func NewNoopResaleSoldSender(logger *logging.Logger) *NoopResaleSoldSender {
	return &NoopResaleSoldSender{logger: logger}
}

// SendSoldEmail returns Unavailable because no Postmark token is set.
func (s *NoopResaleSoldSender) SendSoldEmail(ctx context.Context, id entity.ResaleListingID, _ string, _ entity.ResaleListingSoldEmail) error {
	s.logger.Warn(ctx, "resale sold email skipped: POSTMARK_SERVER_TOKEN is not configured",
		slog.String("resale_listing_id", string(id)))
	return apperr.New(codes.Unavailable, "email is not configured")
}
