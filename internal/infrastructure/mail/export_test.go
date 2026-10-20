package mail

import (
	"time"

	"github.com/pannpers/go-logging/logging"
)

// NewPostmarkConfirmationSenderForTest points the sender at a stub Postmark API.
func NewPostmarkConfirmationSenderForTest(orders OrderStore, apiURL, serverToken string, clock func() time.Time, logger *logging.Logger) *PostmarkConfirmationSender {
	return newPostmarkConfirmationSender(orders, apiURL, serverToken, "noreply@mail.dev.liverty-music.app", "outbound", clock, logger)
}

// NewPostmarkSignInLinkSenderForTest points the sender at a stub Postmark API.
func NewPostmarkSignInLinkSenderForTest(consoleBaseURL, apiURL, serverToken string, logger *logging.Logger) *PostmarkSignInLinkSender {
	return newPostmarkSignInLinkSender(consoleBaseURL, apiURL, serverToken, "noreply@mail.dev.liverty-music.app", "outbound", logger)
}

// NewPostmarkResaleSoldSenderForTest points the sender at a stub Postmark API.
func NewPostmarkResaleSoldSenderForTest(listings ResaleListingStore, apiURL, serverToken string, clock func() time.Time, logger *logging.Logger) *PostmarkResaleSoldSender {
	return newPostmarkResaleSoldSender(listings, apiURL, serverToken, "noreply@mail.dev.liverty-music.app", "outbound", clock, logger)
}
