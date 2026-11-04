package mail

import (
	"context"
	"log/slog"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/entity"
)

// resaleEntryEndedTag tags resale entry ended emails in Postmark's activity
// log.
const resaleEntryEndedTag = "resale-entry-ended"

// ResaleEntryStore is what the mailer needs from the ResaleEntry repository.
type ResaleEntryStore interface {
	Get(ctx context.Context, id entity.ResaleEntryID) (*entity.ResaleEntry, error)
	MarkEndedEmailSent(ctx context.Context, id entity.ResaleEntryID, at time.Time) error
}

// PostmarkResaleEntryEndedSender implements
// [entity.ResaleEntryEndedEmailSender] with Postmark's transactional message
// stream.
type PostmarkResaleEntryEndedSender struct {
	postmarkClient
	entries ResaleEntryStore
	clock   func() time.Time
}

// Compile-time interface compliance check.
var _ entity.ResaleEntryEndedEmailSender = (*PostmarkResaleEntryEndedSender)(nil)

// NewPostmarkResaleEntryEndedSender creates a sender for the given server
// token, From address and message stream.
func NewPostmarkResaleEntryEndedSender(entries ResaleEntryStore, serverToken, fromAddress, messageStream string, clock func() time.Time, logger *logging.Logger) *PostmarkResaleEntryEndedSender {
	return newPostmarkResaleEntryEndedSender(entries, postmarkAPIURL, serverToken, fromAddress, messageStream, clock, logger)
}

func newPostmarkResaleEntryEndedSender(entries ResaleEntryStore, apiURL, serverToken, fromAddress, messageStream string, clock func() time.Time, logger *logging.Logger) *PostmarkResaleEntryEndedSender {
	return &PostmarkResaleEntryEndedSender{
		postmarkClient: newPostmarkClient(apiURL, serverToken, fromAddress, messageStream, logger),
		entries:        entries,
		clock:          clock,
	}
}

// SendEndedEmail implements [entity.ResaleEntryEndedEmailSender].
func (s *PostmarkResaleEntryEndedSender) SendEndedEmail(ctx context.Context, id entity.ResaleEntryID, to string, msg entity.ResaleEntryEndedEmail) error {
	attrs := slog.String("resale_entry_id", string(id))
	if to == "" || msg.Subject == "" || msg.TextBody == "" {
		return apperr.New(codes.InvalidArgument, "the address and the message must not be empty", attrs)
	}
	entry, err := s.entries.Get(ctx, id)
	if err != nil {
		return err
	}
	if entry.State != entity.ResaleEntryStateLapsed && entry.State != entity.ResaleEntryStateLost {
		return apperr.New(codes.FailedPrecondition, "only a lapsed or lost resale entry has an ended email", attrs)
	}
	if entry.EndedEmailSendTime != nil {
		return nil
	}
	if err := s.post(ctx, to, msg.Subject, msg.TextBody, resaleEntryEndedTag,
		map[string]string{"resale_entry_id": string(id)}, attrs); err != nil {
		return err
	}
	return s.entries.MarkEndedEmailSent(ctx, id, s.clock())
}

// NoopResaleEntryEndedSender is used when no Postmark token is configured
// (local development): every call fails with Unavailable.
type NoopResaleEntryEndedSender struct {
	logger *logging.Logger
}

// Compile-time interface compliance check.
var _ entity.ResaleEntryEndedEmailSender = (*NoopResaleEntryEndedSender)(nil)

// NewNoopResaleEntryEndedSender creates a NoopResaleEntryEndedSender.
func NewNoopResaleEntryEndedSender(logger *logging.Logger) *NoopResaleEntryEndedSender {
	return &NoopResaleEntryEndedSender{logger: logger}
}

// SendEndedEmail returns Unavailable because no Postmark token is set.
func (s *NoopResaleEntryEndedSender) SendEndedEmail(ctx context.Context, id entity.ResaleEntryID, _ string, _ entity.ResaleEntryEndedEmail) error {
	s.logger.Warn(ctx, "resale entry ended email skipped: POSTMARK_SERVER_TOKEN is not configured",
		slog.String("resale_entry_id", string(id)))
	return apperr.New(codes.Unavailable, "email is not configured")
}
