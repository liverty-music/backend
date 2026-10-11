package rdb_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v86"
	"uuid"
)

// TestLotteryPipeline_Integration exercises the full lottery pipeline against a
// real local Postgres AND the real Stripe test API, minus the auth/browser
// shell. It drives:
//
//	a Lottery ticket type of 2 tickets → 3 fans each Start, Authorize (real
//	hold) and Confirm, and a 4th fan leaves an authorized Draft → draw →
//	exactly 2 winners captured, 1 loser released with its draw position, and
//	the Draft withdrawn with its hold released.
//
// Winners' PaymentIntents must end `succeeded` (captured); the loser's and the
// Draft's must end `canceled` (hold released). Opt-in: runs only with
// STRIPE_INTEGRATION_TEST=1, a test-mode key, and a local DB. make check / CI
// skip it.
func TestLotteryPipeline_Integration(t *testing.T) {
	if os.Getenv("STRIPE_INTEGRATION_TEST") != "1" {
		t.Skip("opt-in: set STRIPE_INTEGRATION_TEST=1 and STRIPE_SECRET_KEY=sk_test_… to run")
	}
	key := os.Getenv("STRIPE_SECRET_KEY")
	if !strings.HasPrefix(key, "sk_test_") && !strings.HasPrefix(key, "rk_test_") {
		t.Skip("STRIPE_SECRET_KEY must be a test-mode key (sk_test_… or rk_test_…)")
	}
	if testDB == nil {
		t.Skip("no local database available")
	}

	ctx := context.Background()
	logger, _ := logging.New()

	saleRepo := rdb.NewTicketSaleRepository(testDB)
	entryRepo := rdb.NewLotteryEntryRepository(testDB)
	stripePort := payment.NewStripeCardAuthorizationRepository(key, payment.LotteryCardAuthorizationPolicy, logger)

	// A fixed clock inside the sale's window so every step passes the window
	// check deterministically.
	now := saleWindowStart.Add(time.Hour)
	clock := usecase.Clock(func() time.Time { return now })
	uc := usecase.NewLotteryEntryUseCase(saleRepo, entryRepo, rdb.NewTicketRepository(testDB), rdb.NewIdentityVerificationRepository(testDB),
		rdb.NewUserRepository(testDB), stripePort, clock, logger)

	// -- a Lottery ticket type of 2 tickets, so 3 single-ticket entries force one loss --
	const (
		capacity = 2
		entrants = 3
	)
	tt := seedTicketType(t, entity.TicketSaleMethodLottery, capacity, 1)

	// enter starts, authorizes and (when confirm) confirms one fan's entry.
	enter := func(confirm bool) entity.LotteryEntryID {
		t.Helper()
		userID := entity.UserID(seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String()))
		draft, err := uc.Start(ctx, tt.ID, userID, 1)
		require.NoError(t, err)
		_, err = uc.AuthorizeCard(ctx, draft.ID, userID, "Test Fan", "+819012345678")
		require.NoError(t, err)
		authorized, err := entryRepo.Get(ctx, draft.ID)
		require.NoError(t, err)
		confirmHold(t, key, authorized.CardAuthorizationID) // → requires_capture
		if confirm {
			entered, err := uc.Confirm(ctx, draft.ID, userID)
			require.NoError(t, err)
			require.Equal(t, entity.LotteryEntryStateEntered, entered.State)
		}
		return draft.ID
	}
	entryIDs := make([]entity.LotteryEntryID, 0, entrants)
	for range entrants {
		entryIDs = append(entryIDs, enter(true))
	}
	draftID := enter(false)

	// -- run the draw --
	require.NoError(t, uc.RunDraw(ctx, tt.TicketSaleID))

	// -- assert outcomes: exactly `capacity` winners captured, the rest lost/released --
	var won, lost int
	for _, id := range entryIDs {
		e, err := entryRepo.Get(ctx, id)
		require.NoError(t, err)
		switch e.State {
		case entity.LotteryEntryStateWon:
			won++
			assert.Equal(t, stripe.PaymentIntentStatusSucceeded, piStatus(t, key, e.CardAuthorizationID),
				"winner PaymentIntent must be captured (succeeded)")
		case entity.LotteryEntryStateLost:
			lost++
			assert.Equal(t, stripe.PaymentIntentStatusCanceled, piStatus(t, key, e.CardAuthorizationID),
				"loser PaymentIntent must be released (canceled)")
			assert.NotNil(t, e.DrawPosition, "loser must carry a waitlist position")
		default:
			t.Fatalf("unexpected entry state after draw: %v", e.State)
		}
	}
	assert.Equal(t, capacity, won, "winners must exactly fill capacity (no oversell)")
	assert.Equal(t, entrants-capacity, lost, "the remaining entrants must lose")

	draft, err := entryRepo.Get(ctx, draftID)
	require.NoError(t, err)
	assert.Equal(t, entity.LotteryEntryStateWithdrawn, draft.State, "an unconfirmed draft is withdrawn at the draw")
	assert.Equal(t, stripe.PaymentIntentStatusCanceled, piStatus(t, key, draft.CardAuthorizationID),
		"the draft's hold must be released")

	sale, err := saleRepo.Get(ctx, tt.TicketSaleID, now)
	require.NoError(t, err)
	assert.True(t, sale.IsDrawn())
}

// confirmHold drives a freshly-created manual-capture PaymentIntent to
// requires_capture using a non-3DS test card, standing in for the fan's
// in-browser 3DS confirmation.
func confirmHold(t *testing.T, key, ref string) {
	t.Helper()
	sc := stripe.NewClient(key)
	pi, err := sc.V1PaymentIntents.Confirm(context.Background(), ref, &stripe.PaymentIntentConfirmParams{
		PaymentMethod: stripe.String("pm_card_visa"),
	})
	require.NoError(t, err)
	require.Equal(t, stripe.PaymentIntentStatusRequiresCapture, pi.Status)
}

// piStatus fetches the current status of a PaymentIntent from the Stripe test API.
func piStatus(t *testing.T, key, ref string) stripe.PaymentIntentStatus {
	t.Helper()
	sc := stripe.NewClient(key)
	pi, err := sc.V1PaymentIntents.Retrieve(context.Background(), ref, nil)
	require.NoError(t, err)
	return pi.Status
}
