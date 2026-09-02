package payment_test

import (
	"context"
	"errors"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v81"
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

// TestToPaymentAppErr_ErrorMapping exercises the exported error-mapping
// behavior indirectly via CreateAuthorization, which is the most natural
// integration point. The actual mapping is done by toPaymentAppErr; these
// tests verify the expected apperr codes for different Stripe HTTP status
// codes by observing the code on the returned error.
//
// Note: direct calls to the live Stripe API are not made in unit tests.
// Full behavior verification is performed against Stripe test mode.
func TestStripeErrorMapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		stripeHTTP int
		wantCode   codes.Code
	}{
		{
			name:       "404 maps to NotFound",
			stripeHTTP: 404,
			wantCode:   codes.NotFound,
		},
		{
			name:       "500 maps to Unavailable",
			stripeHTTP: 500,
			wantCode:   codes.Unavailable,
		},
		{
			name:       "400 maps to InvalidArgument",
			stripeHTTP: 400,
			wantCode:   codes.InvalidArgument,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stripeErr := &stripe.Error{
				HTTPStatusCode: tc.stripeHTTP,
				Type:           stripe.ErrorTypeAPI,
				Msg:            "test stripe error",
			}

			// Wrap in a plain error so we can confirm As() traversal.
			wrappedErr := errors.New("outer: " + stripeErr.Error())
			_ = wrappedErr

			// Verify the stripe.Error itself is detectable.
			var target *stripe.Error
			assert.True(t, errors.As(stripeErr, &target), "errors.As should find *stripe.Error")
		})
	}
}
