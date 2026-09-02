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
	stripe "github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/paymentintent"
	"uuid"
)

// TestLotteryPipeline_Integration exercises the full lottery pipeline against a
// real local Postgres AND the real Stripe test API — the substance of task 7.2
// minus the auth/browser shell (dev Zitadel is intentionally stopped, so the
// OIDC browser E2E cannot run). It drives:
//
//	configure phase (capacity 2) → 3 fans each authorize (real hold) + apply
//	→ draw → assert exactly 2 winners captured / 1 loser released, no oversell,
//	  and the loser's waitlist position (draw_sequence) is recorded.
//
// Winners' PaymentIntents must end `succeeded` (captured); the loser's must end
// `canceled` (hold released). Opt-in: runs only with STRIPE_INTEGRATION_TEST=1,
// an sk_test_ key, and a local DB. make check / CI skip it.
func TestLotteryPipeline_Integration(t *testing.T) {
	if os.Getenv("STRIPE_INTEGRATION_TEST") != "1" {
		t.Skip("opt-in: set STRIPE_INTEGRATION_TEST=1 and STRIPE_SECRET_KEY=sk_test_… to run")
	}
	key := os.Getenv("STRIPE_SECRET_KEY")
	if !strings.HasPrefix(key, "sk_test_") {
		t.Skip("STRIPE_SECRET_KEY must be a test-mode key (sk_test_…)")
	}
	if testDB == nil {
		t.Skip("no local database available")
	}

	ctx := context.Background()
	logger, _ := logging.New()

	phaseRepo := rdb.NewLotteryPhaseRepository(testDB)
	appRepo := rdb.NewTicketApplicationRepository(testDB)
	eventState := rdb.NewEventPublishStateRepository(testDB)
	stripePort := payment.NewStripeAuthorizationPort(key, logger)

	// A fixed clock inside the application window (open in the past, close in the
	// future) so every Apply passes the window check deterministically.
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	clock := usecase.Clock(func() time.Time { return now })

	uc := usecase.NewLotteryUseCase(phaseRepo, appRepo, eventState, stripePort, clock, logger)

	// -- seed a phase with capacity 2 so 3 single-ticket applicants force one loss --
	artistID := seedArtist(t, "pipeline-artist", uuid.NewV7().String())
	venueID := seedVenue(t, "pipeline-venue")
	eventID := seedEvent(t, venueID, artistID, "pipeline-concert", "2026-12-01")

	const (
		capacity   = 2
		ticketPX   = 5000
		applicants = 3
	)
	phase, err := phaseRepo.Create(ctx, &entity.LotterySalesPhase{
		ID:                       entity.LotteryPhaseID(entity.NewID()),
		EventID:                  eventID,
		OpenTime:                 now.Add(-1 * time.Hour),
		CloseTime:                now.Add(1 * time.Hour),
		TicketCapacity:           capacity,
		MaxTicketsPerApplication: 1,
		TicketPrice:              ticketPX,
	})
	require.NoError(t, err)

	// -- each fan: authorize a real hold, confirm 3DS (server-side test card), apply --
	appIDs := make([]entity.TicketApplicationID, 0, applicants)
	for i := 0; i < applicants; i++ {
		userID := seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())

		auth, err := uc.CreateAuthorization(ctx, usecase.CreateAuthorizationInput{
			PhaseID:              phase.ID,
			RequestedTicketCount: 1,
		})
		require.NoError(t, err)
		confirmHold(t, key, auth.PaymentIntentRef) // → requires_capture

		app, err := uc.Apply(ctx, usecase.ApplyInput{
			PhaseID:              phase.ID,
			ApplicantID:          entity.UserID(userID),
			RequestedTicketCount: 1,
			Identity:             entity.ApplicantIdentity{FullName: "Test Fan", PhoneNumber: "+819012345678"},
			PaymentIntentRef:     auth.PaymentIntentRef,
		})
		require.NoError(t, err)
		require.Equal(t, entity.TicketApplicationStateApplied, app.State)
		appIDs = append(appIDs, app.ID)
	}

	// -- run the draw --
	require.NoError(t, uc.RunDraw(ctx, phase.ID))

	// -- assert outcomes: exactly `capacity` winners captured, the rest lost/released --
	var won, lost int
	for _, id := range appIDs {
		app, err := appRepo.Get(ctx, id)
		require.NoError(t, err)
		switch app.State {
		case entity.TicketApplicationStateWon:
			won++
			assert.Equal(t, stripe.PaymentIntentStatusSucceeded, piStatus(t, key, app.Authorization.PaymentIntentRef),
				"winner PaymentIntent must be captured (succeeded)")
		case entity.TicketApplicationStateLost:
			lost++
			assert.Equal(t, stripe.PaymentIntentStatusCanceled, piStatus(t, key, app.Authorization.PaymentIntentRef),
				"loser PaymentIntent must be released (canceled)")
			assert.GreaterOrEqual(t, app.DrawSequence, int64(0), "loser must carry a waitlist position")
		default:
			t.Fatalf("unexpected application state after draw: %v", app.State)
		}
	}

	assert.Equal(t, capacity, won, "winners must exactly fill capacity (no oversell)")
	assert.Equal(t, applicants-capacity, lost, "the remaining applicants must lose")
}

// confirmHold drives a freshly-created manual-capture PaymentIntent to
// requires_capture using a non-3DS test card, standing in for the fan's
// in-browser 3DS confirmation.
func confirmHold(t *testing.T, key, ref string) {
	t.Helper()
	client := paymentintent.Client{B: stripe.GetBackend(stripe.APIBackend), Key: key}
	pi, err := client.Confirm(ref, &stripe.PaymentIntentConfirmParams{
		PaymentMethod: stripe.String("pm_card_visa"),
	})
	require.NoError(t, err)
	require.Equal(t, stripe.PaymentIntentStatusRequiresCapture, pi.Status)
}

// piStatus fetches the current status of a PaymentIntent from the Stripe test API.
func piStatus(t *testing.T, key, ref string) stripe.PaymentIntentStatus {
	t.Helper()
	client := paymentintent.Client{B: stripe.GetBackend(stripe.APIBackend), Key: key}
	pi, err := client.Get(ref, nil)
	require.NoError(t, err)
	return pi.Status
}
