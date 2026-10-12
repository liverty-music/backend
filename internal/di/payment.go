package di

import (
	"github.com/pannpers/go-logging/logging"

	infrapayment "github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/liverty-music/backend/internal/infrastructure/repository"
)

// paymentProviders are the payment provider parts of the Order, Settlement
// and PayoutAccount repositories.
type paymentProviders struct {
	orders         repository.OrderPayments
	settlements    repository.SettlementPayouts
	payoutAccounts repository.PayoutAccountProvider
}

// newPaymentProviders returns the Stripe adapters when a secret key is
// configured, otherwise no-ops that fail with Unavailable, so local
// development starts without a Stripe account (outside local the key is
// required, see config.ServerConfig.Validate).
func newPaymentProviders(stripeSecretKey string, logger *logging.Logger) paymentProviders {
	if stripeSecretKey == "" {
		return paymentProviders{
			orders:         infrapayment.NewNoopOrderPayments(logger),
			settlements:    infrapayment.NewNoopSettlementPayouts(logger),
			payoutAccounts: infrapayment.NewNoopPayoutAccounts(logger),
		}
	}
	return paymentProviders{
		orders:         infrapayment.NewStripeOrderPayments(stripeSecretKey, logger),
		settlements:    infrapayment.NewStripeSettlementPayouts(stripeSecretKey, logger),
		payoutAccounts: infrapayment.NewStripePayoutAccounts(stripeSecretKey, logger),
	}
}
