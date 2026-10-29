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
//	a Lottery sale of a TicketStock of 2 tickets → 3 fans each AuthorizeCard
//	(real card authorization), complete card authentication and Confirm, and
//	a 4th fan authorizes but never authenticates → draw → exactly 2 winners
//	captured with their Orders Pending and 1 loser released with its draw
//	position and its Order Void; the 4th fan has no entry and no Order.
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

	entryRepo := rdb.NewLotteryEntryRepository(testDB)
	orderRepo := orderRepository(t, rdb.NewOrderRepository(testDB))
	stripePort := payment.NewStripeCardAuthorizationRepository(key, payment.LotteryCardAuthorizationPolicy, logger)

	// -- a Lottery sale of a stock of 2 tickets, so 3 single-ticket entries force one loss --
	const (
		quantity = 2
		entrants = 3
	)
	f := seedStock(t, entity.TicketSaleMethodLottery, quantity, 1)
	// The entry is stored only before the end time by the database clock, so
	// the window is placed around it and the use case reads the same time.
	setSaleEndByDatabaseClock(t, f.SaleID, 24*time.Hour)
	now := time.Now()
	uc := usecase.NewLotteryEntryUseCase(lotteryEntryDeps(t, stripePort, &recordingPublisher{},
		func() time.Time { return now }, logger))

	// enter authorizes one fan's card, completes card authentication and
	// confirms the entry.
	enter := func() entity.LotteryEntryID {
		t.Helper()
		userID := entity.UserID(seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String()))
		authorization, err := uc.AuthorizeCard(ctx, f.SaleID, f.ID, userID, 1, "Test Fan", "+819012345678", nil)
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
	abandoned, err := uc.AuthorizeCard(ctx, f.SaleID, f.ID, abandonedFan, 1, "Test Fan", "+819012345678", nil)
	require.NoError(t, err)

	// -- run the draw --
	require.NoError(t, uc.RunDraw(ctx, f.SaleID))

	// -- assert outcomes: exactly `quantity` winners captured, the rest lost/released --
	var won, lost int
	for _, id := range entryIDs {
		e, err := entryRepo.Get(ctx, id)
		require.NoError(t, err)
		switch e.State {
		case entity.LotteryEntryStateWon:
			won++
			assert.Equal(t, stripe.PaymentIntentStatusSucceeded, piStatus(t, key, e.Order.CardAuthorizationID),
				"winner PaymentIntent must be captured (succeeded)")
			assert.Equal(t, entity.OrderStatusPending, e.Order.Status, "a winner's Order awaits issuance")
		case entity.LotteryEntryStateLost:
			lost++
			assert.Equal(t, stripe.PaymentIntentStatusCanceled, piStatus(t, key, e.Order.CardAuthorizationID),
				"loser PaymentIntent must be released (canceled)")
			assert.NotNil(t, e.DrawPosition, "loser must carry a draw position")
			assert.Equal(t, entity.OrderStatusVoid, e.Order.Status, "a loser's Order is Void")
		default:
			t.Fatalf("unexpected entry state after draw: %v", e.State)
		}
	}
	assert.Equal(t, quantity, won, "winners must exactly fill quantity (no oversell)")
	assert.Equal(t, entrants-quantity, lost, "the remaining entrants must lose")

	none, err := entryRepo.ListByTicketSaleAndUser(ctx, f.SaleID, abandonedFan)
	require.NoError(t, err)
	assert.Empty(t, none, "a fan who never authenticated has no entry")
	held, err := orderRepo.GetHeldForEvent(ctx, abandonedFan, f.EventID, nil, now)
	require.NoError(t, err)
	assert.Zero(t, held.Count, "a fan who never authenticated holds nothing")
	assert.Equal(t, stripe.PaymentIntentStatusRequiresPaymentMethod, piStatus(t, key, abandoned.ID),
		"the unauthenticated card authorization holds nothing")

	entered, err := entryRepo.ListEnteredByTicketSaleAndTicketStock(ctx, f.SaleID, f.ID)
	require.NoError(t, err)
	assert.Empty(t, entered, "the sale is drawn: no entry is Entered")
}

// lotteryEntryDeps wires the lottery entry use case over the real
// repositories, with the given card authorizations, publisher and clock.
func lotteryEntryDeps(t *testing.T, cardAuthorizations entity.CardAuthorizationRepository, publisher usecase.EventPublisher, clock usecase.Clock, logger *logging.Logger) usecase.LotteryEntryDeps {
	t.Helper()
	return usecase.LotteryEntryDeps{
		SaleRepo:                 rdb.NewTicketSaleRepository(testDB),
		StockRepo:                rdb.NewTicketStockRepository(testDB),
		EntryRepo:                rdb.NewLotteryEntryRepository(testDB),
		OrderRepo:                orderRepository(t, rdb.NewOrderRepository(testDB)),
		EventRepo:                rdb.NewEventRepository(testDB),
		IdentityVerificationRepo: rdb.NewIdentityVerificationRepository(testDB),
		UserRepo:                 rdb.NewUserRepository(testDB),
		CardAuthorizations:       cardAuthorizations,
		Publisher:                publisher,
		Clock:                    clock,
		Logger:                   logger,
	}
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
