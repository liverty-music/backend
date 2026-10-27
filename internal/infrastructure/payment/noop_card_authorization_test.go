package payment_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustLogger(t *testing.T) *logging.Logger {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return logger
}

// TestNoopCardAuthorizationRepository checks that every call fails with
// Unavailable when no Stripe key is configured.
func TestNoopCardAuthorizationRepository(t *testing.T) {
	t.Parallel()
	repo := payment.NewNoopCardAuthorizationRepository(mustLogger(t))
	ctx := context.Background()

	_, err := repo.Create(ctx, entity.CardAuthorizationPurchase{Purpose: entity.CardAuthorizationPurposeCheckout, CheckoutID: "checkout-1"}, 6000)
	assert.ErrorIs(t, err, apperr.ErrUnavailable)
	_, err = repo.Get(ctx, "pi_1")
	assert.ErrorIs(t, err, apperr.ErrUnavailable)
	assert.ErrorIs(t, repo.Capture(ctx, "pi_1"), apperr.ErrUnavailable)
	assert.ErrorIs(t, repo.Release(ctx, "pi_1"), apperr.ErrUnavailable)
}
