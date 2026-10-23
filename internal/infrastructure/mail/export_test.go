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

// NewPostmarkPasskeyRegistrationLinkSenderForTest points the sender at a stub Postmark API.
func NewPostmarkPasskeyRegistrationLinkSenderForTest(links PasskeyRegistrationLinkIssuer, apiURL, serverToken string, logger *logging.Logger) *PostmarkPasskeyRegistrationLinkSender {
	return newPostmarkPasskeyRegistrationLinkSender(links, apiURL, serverToken, "noreply@mail.dev.liverty-music.app", "outbound", logger)
}

// NewPostmarkPasskeyChangeSenderForTest points the sender at a stub Postmark API.
func NewPostmarkPasskeyChangeSenderForTest(fanWebBaseURL string, japanTime *time.Location, apiURL, serverToken string, logger *logging.Logger) *PostmarkPasskeyChangeSender {
	return newPostmarkPasskeyChangeSender(fanWebBaseURL, japanTime, apiURL, serverToken, "noreply@mail.dev.liverty-music.app", "outbound", logger)
}

// NewPostmarkEmailVerificationSenderForTest points the sender at a stub Postmark API.
func NewPostmarkEmailVerificationSenderForTest(codes EmailVerificationCodeIssuer, fanWebBaseURL, apiURL, serverToken string, logger *logging.Logger) *PostmarkEmailVerificationSender {
	return newPostmarkEmailVerificationSender(codes, fanWebBaseURL, apiURL, serverToken, "noreply@mail.dev.liverty-music.app", "outbound", logger)
}
