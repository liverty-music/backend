package payment_test

import (
	"context"
	"testing"

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

func TestNewStripeAuthorizationPort(t *testing.T) {
	t.Parallel()

	logger := mustLogger(t)
	// Constructor must not panic and must return a non-nil adapter.
	port := payment.NewStripeAuthorizationPort("sk_test_placeholder", logger)
	assert.NotNil(t, port)
}

func TestNewNoopAuthorizationPort(t *testing.T) {
	t.Parallel()

	logger := mustLogger(t)
	port := payment.NewNoopAuthorizationPort(logger)
	assert.NotNil(t, port)
}

func TestNoopAuthorizationPort_CreateAuthorization(t *testing.T) {
	t.Parallel()

	logger := mustLogger(t)
	port := payment.NewNoopAuthorizationPort(logger)

	ref, secret, err := port.CreateAuthorization(context.Background(), 5000)

	assert.Empty(t, ref)
	assert.Empty(t, secret)
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrUnavailable, "expected Unavailable, got: %v", err)
}

func TestNoopAuthorizationPort_VerifyAuthorization(t *testing.T) {
	t.Parallel()

	logger := mustLogger(t)
	port := payment.NewNoopAuthorizationPort(logger)

	err := port.VerifyAuthorization(context.Background(), "pi_test_123", 5000)

	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrUnavailable)
}

func TestNoopAuthorizationPort_CancelAuthorization(t *testing.T) {
	t.Parallel()

	logger := mustLogger(t)
	port := payment.NewNoopAuthorizationPort(logger)

	err := port.CancelAuthorization(context.Background(), "pi_test_123")

	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrUnavailable)
}

func TestNoopAuthorizationPort_CaptureAuthorization(t *testing.T) {
	t.Parallel()

	logger := mustLogger(t)
	port := payment.NewNoopAuthorizationPort(logger)

	err := port.CaptureAuthorization(context.Background(), "pi_test_123")

	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrUnavailable)
}

// NOTE: the Stripe HTTP-status → apperr-code mapping is no longer duplicated
// here. toPaymentAppErr now delegates to api.FromStatus, whose policy is
// table-tested in pkg/api/errors_test.go (TestFromStatus). The card-brand
// accept/reject rule is tested in internal/entity (TestIsAcceptedCardBrand).
// The adapter's live Stripe wiring is covered by the opt-in integration tests
// (STRIPE_INTEGRATION_TEST=1) in this package and in internal/.../rdb.
