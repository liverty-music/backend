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
//	a 4th fan authorizes but never authenticates → draw, then the issue
//	workflow of each winner and the release workflow of each loser, run in
//	one go → exactly 2 winners captured with their Orders Paid and 1 loser
//	released with its draw position and its Order Void; the 4th fan has no
//	entry and no Order.
//
// Winners' PaymentIntents must end `succeeded` (captured); the loser's must
// end `canceled` (card authorization released); the 4th fan's is never
// charged. Opt-in: runs only with
// STRIPE_INTEGRATION_TEST=1, a test-mode key, and a local DB. make check / CI
// skip it.
func TestLotteryPipeline_Integration(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	if os.Getenv("STRIPE_INTEGRATION_TEST") != "1" {
		t.Skip("opt-in: set STRIPE_INTEGRATION_TEST=1 and STRIPE_SECRET_KEY=sk_test_… to run")
	}
	key := os.Getenv("STRIPE_SECRET_KEY")
	if !strings.HasPrefix(key, "sk_test_") && !strings.HasPrefix(key, "rk_test_") {
		t.Skip("STRIPE_SECRET_KEY must be a test-mode key (sk_test_… or rk_test_…)")
	}

	ctx := context.Background()
	logger, _ := logging.New()

	entryRepo := rdb.NewLotteryEntryRepository(db, testOutbox)
	orderRepo := orderRepository(t, rdb.NewOrderRepository(db))
	stripePort := rdb.NewCardAuthorizationRepository(db, payment.NewStripeCardAuthorizationProvider(key, payment.LotteryCardAuthorizationPolicy, logger))

	// -- a Lottery sale of a stock of 2 tickets, so 3 single-ticket entries force one loss --
	const (
		quantity = 2
		entrants = 3
	)
	f := seedStock(t, db, entity.TicketSaleMethodLottery, quantity, 1)
	// The entry is stored only before the end time by the database clock, so
	// the window is placed around it and the use case reads the same time.
	setSaleEndByDatabaseClock(t, db, f.SaleID, 24*time.Hour)
	now := time.Now()
	uc := usecase.NewLotteryEntryUseCase(lotteryEntryDeps(t, db, stripePort, func() time.Time { return now }, logger))

	// enter authorizes one fan's card, completes card authentication and
	// confirms the entry.
	enter := func() entity.LotteryEntryID {
		t.Helper()
		userID := entity.UserID(seedUser(t, db, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String()))
		authorization, err := uc.AuthorizeCard(ctx, f.SaleID, f.ID, userID, 1, "Test Fan", "+819012345678", nil)
		require.NoError(t, err)
		confirmCardAuthorization(t, db, key, authorization.ID) // → requires_capture
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
	abandonedFan := entity.UserID(seedUser(t, db, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String()))
	abandoned, err := uc.AuthorizeCard(ctx, f.SaleID, f.ID, abandonedFan, 1, "Test Fan", "+819012345678", nil)
	require.NoError(t, err)

	// -- run the draw, then the workflow that follows each drawn entry --
	_, err = uc.Draw(ctx, inlineFlow{}, f.SaleID)
	require.NoError(t, err)
	orders := usecase.NewOrderUseCase(usecase.OrderDeps{
		IssuanceRepo:             rdb.NewIssuanceRepository(db, testOutbox),
		OrderRepo:                orderRepo,
		EntryRepo:                entryRepo,
		EventOrganizerRepo:       rdb.NewEventOrganizerRepository(db),
		OrganizerRepo:            rdb.NewOrganizerRepository(db, testOutbox),
		IdentityVerificationRepo: rdb.NewIdentityVerificationRepository(db),
		TicketSaleRepo:           rdb.NewTicketSaleRepository(db, testOutbox),
		StockRepo:                rdb.NewTicketStockRepository(db),
		CardAuthorizations:       stripePort,
		Clock:                    func() time.Time { return now },
		Logger:                   logger,
	})
	for _, id := range entryIDs {
		e, err := entryRepo.Get(ctx, id)
		require.NoError(t, err)
		switch e.State {
		case entity.LotteryEntryStateWon:
			_, err = orders.Issue(ctx, inlineFlow{}, usecase.IssueRequest{LotteryEntryID: id})
		case entity.LotteryEntryStateLost:
			_, err = orders.ReleaseCardAuthorization(ctx, inlineFlow{}, e.OrderID)
		}
		require.NoError(t, err)
	}

	// -- assert outcomes: exactly `quantity` winners captured, the rest lost/released --
	var won, lost int
	for _, id := range entryIDs {
		e, err := entryRepo.Get(ctx, id)
		require.NoError(t, err)
		switch e.State {
		case entity.LotteryEntryStateWon:
			won++
			assert.Equal(t, stripe.PaymentIntentStatusSucceeded, piStatus(t, db, key, e.Order.CardAuthorizationID),
				"winner PaymentIntent must be captured (succeeded)")
			assert.Equal(t, entity.OrderStatusPaid, e.Order.Status, "a winner's Order is issued")
		case entity.LotteryEntryStateLost:
			lost++
			assert.Equal(t, stripe.PaymentIntentStatusCanceled, piStatus(t, db, key, e.Order.CardAuthorizationID),
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
	assert.Equal(t, stripe.PaymentIntentStatusRequiresPaymentMethod, piStatus(t, db, key, abandoned.ID),
		"the unauthenticated card authorization holds nothing")

	entered, err := entryRepo.ListEnteredByTicketSaleAndTicketStock(ctx, f.SaleID, f.ID)
	require.NoError(t, err)
	assert.Empty(t, entered, "the sale is drawn: no entry is Entered")
}

// inlineFlow runs a workflow's steps at once, in the test's goroutine, as a
// first run with no recorded step does.
type inlineFlow struct{}

func (inlineFlow) Step(ctx context.Context, _ string, fn func(context.Context) ([]byte, error)) ([]byte, error) {
	return fn(ctx)
}

func (inlineFlow) Start(context.Context, string, string, any, ...usecase.StartOption) error {
	return nil
}

func (inlineFlow) Recv(context.Context, string, time.Duration) ([]byte, bool, error) {
	return nil, false, nil
}

func (inlineFlow) Patched(context.Context, string) (bool, error) { return true, nil }

func (inlineFlow) DeprecatePatch(context.Context, string) error { return nil }

// lotteryEntryDeps wires the lottery entry use case over the real
// repositories, with the given card authorizations, publisher and clock.
func lotteryEntryDeps(t *testing.T, db *rdb.Database, cardAuthorizations entity.CardAuthorizationRepository, clock usecase.Clock, logger *logging.Logger) usecase.LotteryEntryDeps {
	t.Helper()
	return usecase.LotteryEntryDeps{
		SaleRepo:                 rdb.NewTicketSaleRepository(db, testOutbox),
		StockRepo:                rdb.NewTicketStockRepository(db),
		EntryRepo:                rdb.NewLotteryEntryRepository(db, testOutbox),
		OrderRepo:                orderRepository(t, rdb.NewOrderRepository(db)),
		EventRepo:                rdb.NewEventRepository(db),
		EventState:               rdb.NewEventPublishStateRepository(db),
		IdentityVerificationRepo: rdb.NewIdentityVerificationRepository(db),
		UserRepo:                 rdb.NewUserRepository(db, testOutbox),
		CardAuthorizations:       cardAuthorizations,
		Clock:                    clock,
		Logger:                   logger,
	}
}

// confirmCardAuthorization drives a freshly-created manual-capture PaymentIntent to
// requires_capture using a non-3DS test card, standing in for the fan's
// in-browser 3DS confirmation.
func confirmCardAuthorization(t *testing.T, db *rdb.Database, key, cardAuthorizationID string) {
	t.Helper()
	sc := stripe.NewClient(key)
	pi, err := sc.V1PaymentIntents.Confirm(context.Background(), providerCardAuthorizationID(t, db, cardAuthorizationID), &stripe.PaymentIntentConfirmParams{
		PaymentMethod: stripe.String("pm_card_visa"),
	})
	require.NoError(t, err)
	require.Equal(t, stripe.PaymentIntentStatusRequiresCapture, pi.Status)
}

// piStatus fetches the current status of a PaymentIntent from the Stripe test API.
func piStatus(t *testing.T, db *rdb.Database, key, cardAuthorizationID string) stripe.PaymentIntentStatus {
	t.Helper()
	sc := stripe.NewClient(key)
	pi, err := sc.V1PaymentIntents.Retrieve(context.Background(), providerCardAuthorizationID(t, db, cardAuthorizationID), nil)
	require.NoError(t, err)
	return pi.Status
}
