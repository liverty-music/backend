package entity

// CardBrand is a payment-card network name as reported by the payment provider,
// lowercased (e.g. "visa", "mastercard", "jcb", "diners", "discover", "amex").
type CardBrand string

// CardBrandAmex is American Express — the one brand a lottery application rejects.
const CardBrandAmex CardBrand = "amex"

// IsAcceptedCardBrand reports whether a card of the given brand may be used to
// authorize a lottery application.
//
// American Express is rejected: a JP-based Stripe account cannot reliably hold
// an Amex authorization for the up-to-14-day application window, so an Amex hold
// could lapse before the draw captures it. Every other brand — including an
// empty/unknown brand the provider could not classify — is accepted; the
// separate amount and JPY-currency checks still apply.
//
// This is the domain rule; infrastructure adapters only extract the brand string
// from their provider's response and delegate the decision here, so the policy
// is testable without a live payment provider.
func IsAcceptedCardBrand(brand CardBrand) bool {
	return brand != CardBrandAmex
}
