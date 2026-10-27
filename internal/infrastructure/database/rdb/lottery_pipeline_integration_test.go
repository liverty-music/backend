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
	stripe "github.com/stripe/stripe-go/v87"
	"uuid"
)

// TestLotteryPipeline_Integration exercises the full lottery pipeline against a
// real local Postgres AND the real Stripe test API, minus the auth/browser
// shell. It drives:
//
//	a Lottery ticket type of 2 tickets → 3 fans each AuthorizeCard (real card
//	authorization), complete card authentication and Confirm, and a 4th fan
//	authorizes but never authenticates → draw → exactly 2 winners captured
//	and 1 loser released with its draw position; the 4th fan has no entry.
//
// Winners' PaymentIntents must end `succeeded` (captured); the loser's must
// end `canceled` (card authorization released); the 4th fan's is never
// charged. Opt-in: runs only with
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

	// -- a Lottery ticket type of 2 tickets, so 3 single-ticket entries force one loss --
	const (
		quantity = 2
		entrants = 3
	)
	tt := seedTicketType(t, entity.TicketSaleMethodLottery, quantity, 1)
	// The entry is stored only before the end time by the database clock, so
	// the window is placed around it and the use case reads the same time.
	setSaleEndByDatabaseClock(t, tt.TicketSaleID, 24*time.Hour)
	now := time.Now()
	clock := usecase.Clock(func() time.Time { return now })
	uc := usecase.NewLotteryEntryUseCase(saleRepo, entryRepo, rdb.NewTicketRepository(testDB), rdb.NewIdentityVerificationRepository(testDB),
		rdb.NewUserRepository(testDB), stripePort, &recordingPublisher{}, clock, logger)

	// enter authorizes one fan's card, completes card authentication and
	// confirms the entry.
	enter := func() entity.LotteryEntryID {
		t.Helper()
		userID := entity.UserID(seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String()))
		authorization, err := uc.AuthorizeCard(ctx, tt.ID, userID, 1, "Test Fan", "+819012345678", nil)
		require.NoError(t, err)
		confirmCardAuthorization(t, key, authorization.ID) // → requires_capture
		entered, err := uc.Confirm(ctx, authorization.ID, &userID)
		require.NoError(t, err)
		require.Equal(t, entity.LotteryEntryStateEntered, entered.State)
		return entered.ID
	}
	entryIDs := make([]entity.LotteryEntryID, 0, entrants)
	for range entrants {
		entryIDs = append(entryIDs, enter())
	}
	// A 4th fan authorizes but never completes card authentication.
	abandonedFan := entity.UserID(seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String()))
	abandoned, err := uc.AuthorizeCard(ctx, tt.ID, abandonedFan, 1, "Test Fan", "+819012345678", nil)
	require.NoError(t, err)

	// -- run the draw --
	require.NoError(t, uc.RunDraw(ctx, tt.TicketSaleID))

	// -- assert outcomes: exactly `quantity` winners captured, the rest lost/released --
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
			assert.NotNil(t, e.DrawPosition, "loser must carry a draw position")
		default:
			t.Fatalf("unexpected entry state after draw: %v", e.State)
		}
	}
	assert.Equal(t, quantity, won, "winners must exactly fill quantity (no oversell)")
	assert.Equal(t, entrants-quantity, lost, "the remaining entrants must lose")

	none, err := entryRepo.ListByTicketTypeAndUser(ctx, tt.ID, abandonedFan)
	require.NoError(t, err)
	assert.Empty(t, none, "a fan who never authenticated has no entry")
	assert.Equal(t, stripe.PaymentIntentStatusRequiresPaymentMethod, piStatus(t, key, abandoned.ID),
		"the unauthenticated card authorization holds nothing")

	sale, err := saleRepo.Get(ctx, tt.TicketSaleID, now)
	require.NoError(t, err)
	assert.True(t, sale.IsDrawn())
}

// confirmCardAuthorization drives a freshly-created manual-capture PaymentIntent to
// requires_capture using a non-3DS test card, standing in for the fan's
// in-browser 3DS confirmation.
func confirmCardAuthorization(t *testing.T, key, cardAuthorizationID string) {
	t.Helper()
	sc := stripe.NewClient(key)
	pi, err := sc.V1PaymentIntents.Confirm(context.Background(), cardAuthorizationID, &stripe.PaymentIntentConfirmParams{
		PaymentMethod: stripe.String("pm_card_visa"),
	})
	require.NoError(t, err)
	require.Equal(t, stripe.PaymentIntentStatusRequiresCapture, pi.Status)
}

// piStatus fetches the current status of a PaymentIntent from the Stripe test API.
func piStatus(t *testing.T, key, cardAuthorizationID string) stripe.PaymentIntentStatus {
	t.Helper()
	sc := stripe.NewClient(key)
	pi, err := sc.V1PaymentIntents.Retrieve(context.Background(), cardAuthorizationID, nil)
	require.NoError(t, err)
	return pi.Status
}
