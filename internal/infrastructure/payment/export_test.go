package payment

import "github.com/pannpers/go-logging/logging"

// NewStripeReservationAuthorizationPortForTest points the ticket sale's
// card-hold port at a stub Stripe API.
func NewStripeReservationAuthorizationPortForTest(secretKey, apiURL string, logger *logging.Logger) *StripeReservationAuthorizationPort {
	return newStripeReservationAuthorizationPort(secretKey, apiURL, logger)
}
