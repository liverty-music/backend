package payment_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
)

func TestNewStripeSettlementPort(t *testing.T) {
	t.Parallel()

	port := payment.NewStripeSettlementPort("sk_test_placeholder", mustLogger(t))
	assert.NotNil(t, port)
}

// CreateConnectedAccount rejects blank inputs before reaching Stripe.
//
// The organizer id is both the account's metadata link back to our Organizer and
// the seed of the idempotency key, so a blank one would let two provisioning
// calls share a key and silently collapse into one account. The contact email is
// mandatory on Stripe's side whenever a recipient configuration is supplied, so
// catching it here turns a 400 round-trip into an immediate InvalidArgument.
func TestStripeSettlementPort_CreateConnectedAccount_RejectsBlankInput(t *testing.T) {
	t.Parallel()

	type args struct {
		organizerID  string
		contactEmail string
	}
	tests := []struct {
		name    string
		args    args
		wantErr error
	}{
		{
			name:    "empty organizer id",
			args:    args{organizerID: "", contactEmail: "organizer@example.test"},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "empty contact email",
			args:    args{organizerID: "organizer-1", contactEmail: ""},
			wantErr: apperr.ErrInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// A placeholder key is safe here: validation rejects the input
			// before any request is built, so no network call is attempted.
			port := payment.NewStripeSettlementPort("sk_test_placeholder", mustLogger(t))

			ref, err := port.CreateConnectedAccount(context.Background(), tt.args.organizerID, tt.args.contactEmail)

			assert.ErrorIs(t, err, tt.wantErr)
			assert.Empty(t, ref)
		})
	}
}

// The noop adapter stands in wherever no Stripe key is configured — notably the
// dev environment, which has none by design. It must fail closed rather than
// pretend an account was provisioned.
func TestNoopSettlementPort_CreateConnectedAccount(t *testing.T) {
	t.Parallel()

	port := payment.NewNoopSettlementPort(mustLogger(t))

	ref, err := port.CreateConnectedAccount(context.Background(), "organizer-1", "organizer@example.test")

	assert.ErrorIs(t, err, apperr.ErrUnavailable)
	assert.Empty(t, ref)
}
