package mail

import (
	"time"

	"github.com/pannpers/go-logging/logging"
)

// NewPostmarkConfirmationSenderForTest points the sender at a stub Postmark API.
func NewPostmarkConfirmationSenderForTest(orders OrderStore, apiURL, serverToken string, clock func() time.Time, logger *logging.Logger) *PostmarkConfirmationSender {
	return newPostmarkConfirmationSender(orders, apiURL, serverToken, "noreply@mail.dev.liverty-music.app", "outbound", clock, logger)
}
