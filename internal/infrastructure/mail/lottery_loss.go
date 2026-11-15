package mail

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// lotteryLossTag tags loss emails in Postmark's activity log.
const lotteryLossTag = "lottery-loss"

// PostmarkLotteryLossSender implements [entity.LotteryLossEmailSender] with
// Postmark's transactional message stream.
type PostmarkLotteryLossSender struct {
	postmarkClient
}

// Compile-time interface compliance check.
var _ entity.LotteryLossEmailSender = (*PostmarkLotteryLossSender)(nil)

// NewPostmarkLotteryLossSender creates a sender for the given server token,
// From address and message stream.
func NewPostmarkLotteryLossSender(serverToken, fromAddress, messageStream string, logger *logging.Logger) *PostmarkLotteryLossSender {
	return newPostmarkLotteryLossSender(postmarkAPIURL, serverToken, fromAddress, messageStream, logger)
}

func newPostmarkLotteryLossSender(apiURL, serverToken, fromAddress, messageStream string, logger *logging.Logger) *PostmarkLotteryLossSender {
	return &PostmarkLotteryLossSender{
		postmarkClient: newPostmarkClient(apiURL, serverToken, fromAddress, messageStream, logger),
	}
}

// SendLossEmail implements [entity.LotteryLossEmailSender].
func (s *PostmarkLotteryLossSender) SendLossEmail(ctx context.Context, id entity.LotteryEntryID, to string, msg entity.LotteryLossEmail) error {
	attrs := slog.String("lottery_entry_id", string(id))
	if to == "" || msg.Subject == "" || msg.TextBody == "" {
		return apperr.New(codes.InvalidArgument, "the address and the message must not be empty", attrs)
	}
	return s.post(ctx, to, msg.Subject, msg.TextBody, lotteryLossTag,
		map[string]string{"lottery_entry_id": string(id)}, attrs)
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
