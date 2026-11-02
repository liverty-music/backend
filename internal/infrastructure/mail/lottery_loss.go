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

// lotteryLossTag tags loss emails in Postmark's activity log.
const lotteryLossTag = "lottery-loss"

// LotteryEntryStore is what the mailer needs from the LotteryEntry
// repository.
type LotteryEntryStore interface {
	Get(ctx context.Context, id entity.LotteryEntryID) (*entity.LotteryEntry, error)
	MarkLossEmailSent(ctx context.Context, id entity.LotteryEntryID, at time.Time) error
}

// PostmarkLotteryLossSender implements [entity.LotteryLossEmailSender] with
// Postmark's transactional message stream.
type PostmarkLotteryLossSender struct {
	postmarkClient
	entries LotteryEntryStore
	clock   func() time.Time
}

// Compile-time interface compliance check.
var _ entity.LotteryLossEmailSender = (*PostmarkLotteryLossSender)(nil)

// NewPostmarkLotteryLossSender creates a sender for the given server token,
// From address and message stream.
func NewPostmarkLotteryLossSender(entries LotteryEntryStore, serverToken, fromAddress, messageStream string, clock func() time.Time, logger *logging.Logger) *PostmarkLotteryLossSender {
	return newPostmarkLotteryLossSender(entries, postmarkAPIURL, serverToken, fromAddress, messageStream, clock, logger)
}

func newPostmarkLotteryLossSender(entries LotteryEntryStore, apiURL, serverToken, fromAddress, messageStream string, clock func() time.Time, logger *logging.Logger) *PostmarkLotteryLossSender {
	return &PostmarkLotteryLossSender{
		postmarkClient: newPostmarkClient(apiURL, serverToken, fromAddress, messageStream, logger),
		entries:        entries,
		clock:          clock,
	}
}

// SendLossEmail implements [entity.LotteryLossEmailSender].
func (s *PostmarkLotteryLossSender) SendLossEmail(ctx context.Context, id entity.LotteryEntryID, to string, msg entity.LotteryLossEmail) error {
	attrs := slog.String("lottery_entry_id", string(id))
	if to == "" || msg.Subject == "" || msg.TextBody == "" {
		return apperr.New(codes.InvalidArgument, "the address and the message must not be empty", attrs)
	}
	entry, err := s.entries.Get(ctx, id)
	if err != nil {
		return err
	}
	if entry.LossEmailSendTime != nil {
		return nil
	}
	if err := s.post(ctx, to, msg.Subject, msg.TextBody, lotteryLossTag,
		map[string]string{"lottery_entry_id": string(id)}, attrs); err != nil {
		return err
	}
	return s.entries.MarkLossEmailSent(ctx, id, s.clock())
}

// NoopLotteryLossSender is used when no Postmark token is configured (local
// development): every call fails with Unavailable.
type NoopLotteryLossSender struct {
	logger *logging.Logger
}

// Compile-time interface compliance check.
var _ entity.LotteryLossEmailSender = (*NoopLotteryLossSender)(nil)

// NewNoopLotteryLossSender creates a NoopLotteryLossSender.
func NewNoopLotteryLossSender(logger *logging.Logger) *NoopLotteryLossSender {
	return &NoopLotteryLossSender{logger: logger}
}

// SendLossEmail returns Unavailable because no Postmark token is set.
func (s *NoopLotteryLossSender) SendLossEmail(ctx context.Context, id entity.LotteryEntryID, _ string, _ entity.LotteryLossEmail) error {
	s.logger.Warn(ctx, "lottery loss email skipped: POSTMARK_SERVER_TOKEN is not configured",
		slog.String("lottery_entry_id", string(id)))
	return apperr.New(codes.Unavailable, "email is not configured")
}
