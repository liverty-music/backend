package payment

import "github.com/pannpers/go-logging/logging"

// NewStripeAuthorizationPortForTest points the lottery entry's card-hold port
// at a stub Stripe API.
func NewStripeAuthorizationPortForTest(secretKey, apiURL string, logger *logging.Logger) *StripeAuthorizationPort {
	return newStripeAuthorizationPort(secretKey, apiURL, logger)
}
