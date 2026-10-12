package payment

import "github.com/pannpers/go-logging/logging"

// NewStripeCardAuthorizationRepositoryForTest points the card authorization
// repository at a stub Stripe API.
func NewStripeCardAuthorizationRepositoryForTest(secretKey, apiURL string, policy CardAuthorizationPolicy, logger *logging.Logger) *StripeCardAuthorizationRepository {
	return newStripeCardAuthorizationRepository(secretKey, apiURL, policy, logger)
}
