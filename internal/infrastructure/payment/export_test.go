package payment

import "github.com/pannpers/go-logging/logging"

// NewStripeCheckoutAuthorizationPortForTest points the checkout's
// card-hold port at a stub Stripe API.
func NewStripeCheckoutAuthorizationPortForTest(secretKey, apiURL string, logger *logging.Logger) *StripeCheckoutAuthorizationPort {
	return newStripeCheckoutAuthorizationPort(secretKey, apiURL, logger)
}
