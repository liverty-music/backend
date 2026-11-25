package event_test

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

// The properties in this file: for a generated announcement and any state its
// records are already in, delivering the announcement several times leaves
// the same state as delivering it once, and no delivery returns an error,
// which would send the message to a retry and, after the redelivery limit, to
// the poison queue. Before #684 a redelivered USER.created whose address had
// been verified since ended in the poison queue.

// workflowRun runs the workflow started under id with input.
type workflowRun func(ctx context.Context, workflow, id string, input any) error

// onceWorkflows is the workflow engine as the consumers see it: Start runs a
// workflow once per id, at once and to its end, and starting an id that
// exists starts nothing, as DBOS does.
type onceWorkflows struct {
	ucmocks.MockWorkflows
	mu      sync.Mutex
	run     workflowRun
	started map[string]bool
}

func (w *onceWorkflows) Start(ctx context.Context, workflow, id string, input any, _ ...usecase.StartOption) error {
	w.mu.Lock()
	if w.started[id] {
		w.mu.Unlock()
		return nil
	}
	w.started[id] = true
	w.mu.Unlock()
	if w.run == nil {
		return nil
	}
	return w.run(ctx, workflow, id, input)
}

// startedIDs returns the ids of the started workflows.
func (w *onceWorkflows) startedIDs() map[string]bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return maps.Clone(w.started)
}

// inlineFlow runs a workflow's steps at once as a first run does; a wait
// ends with its timeout, since no test sends a signal.
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

// genID generates a UUID-shaped id.
func genID(rt *rapid.T, label string) string {
	b := rapid.SliceOfN(rapid.Byte(), 16, 16).Draw(rt, label)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// userPrior is the state of the announced User when an announcement arrives.
type userPrior int

const (
	userDeleted userPrior = iota
	userUnverified
	userVerified
)

// userState is what the send-verification path reads and changes.
type userState struct {
	Exists    bool
	Verified  bool
	Emails    int
	Workflows map[string]bool
}

// userScenario is one generated run of the USER.created consumer.
type userScenario struct {
	userID, announcementID string
	prior                  userPrior
	// failedSends is how many sends fail before one succeeds; 4 or more
	// exhausts the workflow's tries.
	failedSends int
	// verifyAfter is the delivery after which the fan verifies the address,
	// or -1 when the fan does not.
	verifyAfter int
}

// runUserCreated delivers sc's announcement deliveries times through the
// real UserUseCase and returns the final state. It fails rt when a delivery
// returns an error.
func runUserCreated(t *testing.T, rt *rapid.T, sc userScenario, deliveries int) userState {
	var mu sync.Mutex
	state := userState{Exists: sc.prior != userDeleted, Verified: sc.prior == userVerified}
	failsLeft := sc.failedSends

	users := entitymocks.NewMockUserRepository(t)
	users.EXPECT().Get(mock.Anything, sc.userID).RunAndReturn(func(context.Context, string) (*entity.User, error) {
		mu.Lock()
		defer mu.Unlock()
		if !state.Exists {
			return nil, apperr.New(codes.NotFound, "user not found")
		}
		return &entity.User{ID: sc.userID, Email: "fan@example.com"}, nil
	}).Maybe()
	// The verifier keeps the contract of entity.EmailVerifier: an address
	// already verified is FailedPrecondition and sends nothing.
	verifier := entitymocks.NewMockEmailVerifier(t)
	verifier.EXPECT().SendVerification(mock.Anything, mock.Anything).RunAndReturn(func(context.Context, *entity.User) error {
		mu.Lock()
		defer mu.Unlock()
		if state.Verified {
			return apperr.New(codes.FailedPrecondition, "email already verified")
		}
		if failsLeft > 0 {
			failsLeft--
			return apperr.New(codes.Internal, "verification email cannot be sent")
		}
		state.Emails++
		return nil
	}).Maybe()

	workflows := &onceWorkflows{started: map[string]bool{}}
	uc := usecase.NewUserUseCase(users, nil, usecase.DeletionBlockerReads{}, workflows, verifier, nil, nil, usecase.PasskeyMailers{}, nil, newTestLogger(t))
	workflows.run = func(ctx context.Context, workflow, _ string, input any) error {
		require.Equal(rt, usecase.WorkflowSendVerification, workflow)
		_, err := uc.RunSendVerification(ctx, inlineFlow{}, input.(usecase.UserRequest))
		return err
	}
	consumer := event.NewUserConsumer(uc, newTestLogger(t))

	for i := range deliveries {
		msg := makeAnnouncementMsg(t, sc.announcementID, "", &entityv1.User{Id: &entityv1.UserId{Value: sc.userID}})
		require.NoError(rt, consumer.Handle(msg), "delivery %d", i+1)
		if i == sc.verifyAfter && state.Exists {
			mu.Lock()
			state.Verified = true
			mu.Unlock()
		}
	}
	state.Workflows = workflows.startedIDs()
	return state
}

// TestUserConsumer_RedeliveryIsIdempotent checks the property of this file
// for USER.created, whatever the User's state: deleted, not verified, or
// verified before the announcement or between its deliveries.
func TestUserConsumer_RedeliveryIsIdempotent(t *testing.T) {
	t.Parallel()

	// @spec components/infrastructure/backend/process/event-delivery "Delivered twice"
	rapid.Check(t, func(rt *rapid.T) {
		deliveries := rapid.IntRange(2, 4).Draw(rt, "deliveries")
		sc := userScenario{
			userID:         genID(rt, "userID"),
			announcementID: genID(rt, "announcementID"),
			prior:          userPrior(rapid.IntRange(int(userDeleted), int(userVerified)).Draw(rt, "prior")),
			failedSends:    rapid.IntRange(0, 5).Draw(rt, "failedSends"),
			verifyAfter:    rapid.IntRange(-1, deliveries-1).Draw(rt, "verifyAfter"),
		}
		once := sc
		if once.verifyAfter > 0 {
			once.verifyAfter = 0
		}

		want := runUserCreated(t, rt, once, 1)
		got := runUserCreated(t, rt, sc, deliveries)

		require.Equal(rt, want, got)
	})
}

// orderPaidHandler is one of the two durables that receive ORDER.paid.
type orderPaidHandler int

const (
	sendOrderConfirmation orderPaidHandler = iota
	markPaid
)

// orderState is what the two ORDER.paid durables read and change.
type orderState struct {
	ConfirmationSent bool
	Emails           int
	Journeys         map[string]entity.TicketJourney
	Workflows        map[string]bool
}

// orderScenario is one generated ORDER.paid announcement and the state of
// its records when it arrives.
type orderScenario struct {
	announcementID string
	order          *entityv1.Order
	// source is how the Order was applied for: "lottery", "resale" or
	// "checkout".
	source string
	// confirmationSent is whether the confirmation was sent before.
	confirmationSent bool
	// journey is the fan's journey for the event before, nil when none.
	journey *entity.TicketJourney
}

func genOrderScenario(rt *rapid.T) orderScenario {
	sc := orderScenario{
		announcementID: genID(rt, "announcementID"),
		order: &entityv1.Order{
			Id:            &entityv1.OrderId{Value: genID(rt, "orderID")},
			UserId:        &entityv1.UserId{Value: genID(rt, "userID")},
			TicketStockId: &entityv1.TicketStockId{Value: genID(rt, "stockID")},
			TicketCount:   rapid.Int32Range(1, 4).Draw(rt, "ticketCount"),
			Amount:        rapid.Int64Range(0, 100_000).Draw(rt, "amount"),
			Currency:      "JPY",
		},
		source:           rapid.SampledFrom([]string{"lottery", "resale", "checkout"}).Draw(rt, "source"),
		confirmationSent: rapid.Bool().Draw(rt, "confirmationSent"),
	}
	if rapid.Bool().Draw(rt, "hasJourney") {
		sc.journey = &entity.TicketJourney{
			UserID:  sc.order.GetUserId().GetValue(),
			EventID: "event-1",
			Status: rapid.SampledFrom([]entity.TicketJourneyStatus{
				entity.TicketJourneyStatusTracking, entity.TicketJourneyStatusApplied, entity.TicketJourneyStatusLost,
				entity.TicketJourneyStatusUnpaid, entity.TicketJourneyStatusPaid,
			}).Draw(rt, "journeyStatus"),
			StatusSetBy: rapid.SampledFrom([]entity.TicketJourneySetBy{
				entity.TicketJourneySetByFan, entity.TicketJourneySetByPlatform, entity.TicketJourneySetByInference,
			}).Draw(rt, "journeySetBy"),
		}
	}
	return sc
}

// newOrderPaidWorld wires the ORDER.paid consumer to the real
// NotificationUseCase and TicketJourneyUseCase over the records of sc, and
// returns it with a function that reads the final state. The fan has no
// home, so marking the journey Paid raises no hype.
func newOrderPaidWorld(t *testing.T, sc orderScenario) (*event.OrderPaidConsumer, func() orderState) {
	logger := newTestLogger(t)
	var mu sync.Mutex
	orderID := sc.order.GetId().GetValue()
	userID := sc.order.GetUserId().GetValue()
	order := entity.Order{ID: entity.OrderID(orderID), UserID: entity.UserID(userID), Amount: sc.order.GetAmount(), Currency: "JPY", PayTime: new(time.Now())}
	switch sc.source {
	case "lottery":
		order.LotteryEntryID = "entry-1"
	case "resale":
		order.ResaleEntryID = "resale-entry-1"
	default:
		order.CheckoutID = "checkout-1"
	}
	if sc.confirmationSent {
		order.ConfirmationSendTime = new(time.Now())
	}
	state := orderState{Journeys: map[string]entity.TicketJourney{}}
	if sc.journey != nil {
		state.Journeys[sc.journey.UserID+"/"+sc.journey.EventID] = *sc.journey
	}

	orders := entitymocks.NewMockOrderRepository(t)
	orders.EXPECT().Get(mock.Anything, entity.OrderID(orderID)).RunAndReturn(func(context.Context, entity.OrderID) (*entity.Order, error) {
		mu.Lock()
		defer mu.Unlock()
		copied := order
		return &copied, nil
	}).Maybe()
	orders.EXPECT().MarkConfirmationSent(mock.Anything, entity.OrderID(orderID), mock.Anything).RunAndReturn(
		func(_ context.Context, _ entity.OrderID, at time.Time) error {
			mu.Lock()
			defer mu.Unlock()
			order.ConfirmationSendTime = &at
			return nil
		}).Maybe()
	mailer := entitymocks.NewMockOrderMailer(t)
	mailer.EXPECT().SendConfirmationEmail(mock.Anything, entity.OrderID(orderID), "fan@example.com", mock.Anything).RunAndReturn(
		func(context.Context, entity.OrderID, string, entity.OrderConfirmationEmail) error {
			mu.Lock()
			defer mu.Unlock()
			state.Emails++
			return nil
		}).Maybe()
	users := entitymocks.NewMockUserRepository(t)
	users.EXPECT().Get(mock.Anything, userID).Return(&entity.User{ID: userID, Email: "fan@example.com"}, nil).Maybe()
	organizers := entitymocks.NewMockOrganizerRepository(t)
	organizers.EXPECT().Get(mock.Anything, "org-1").Return(&entity.Organizer{ID: "org-1"}, nil).Maybe()
	journeys := entitymocks.NewMockTicketJourneyRepository(t)
	journeys.EXPECT().Upsert(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, j *entity.TicketJourney) error {
		mu.Lock()
		defer mu.Unlock()
		state.Journeys[j.UserID+"/"+j.EventID] = *j
		return nil
	}).Maybe()

	// The push workflow is only started: what it delivers is the deliver
	// workflow's own concern, and the consumer's part is the id it names.
	workflows := &onceWorkflows{started: map[string]bool{}}
	notifications := usecase.NewNotificationUseCase(entitymocks.NewMockNotificationRepository(t), entitymocks.NewMockPushSubscriptionRepository(t),
		entitymocks.NewMockPushNotificationSender(t), workflows, anyPushMetrics(t), logger, usecase.OrderConfirmationDeps{
			Now:    time.Now,
			Orders: orders, Users: users, Concerts: stubConcertReader{}, EventOrganizer: stubEventOrganizer{},
			Organizers: organizers, Mailer: mailer, TimeZone: time.UTC,
		}, usecase.NewConcertsDeps{}, usecase.SubjectDeps{Now: time.Now})
	journeyUC := usecase.NewTicketJourneyUseCase(journeys, users, entitymocks.NewMockConcertRepository(t),
		entitymocks.NewMockFollowRepository(t), entitymocks.NewMockLotteryEntryRepository(t), logger)
	consumer := event.NewOrderPaidConsumer(notifications, journeyUC, stubStocks{}, logger)

	return consumer, func() orderState {
		mu.Lock()
		defer mu.Unlock()
		got := state
		got.ConfirmationSent = order.ConfirmationSendTime != nil
		got.Journeys = maps.Clone(state.Journeys)
		got.Workflows = workflows.startedIDs()
		return got
	}
}

// runOrderPaid delivers sc's announcement to the durables in the order of
// handlers and returns the final state. It fails rt when a delivery returns
// an error.
func runOrderPaid(t *testing.T, rt *rapid.T, sc orderScenario, handlers []orderPaidHandler) orderState {
	consumer, final := newOrderPaidWorld(t, sc)
	for i, h := range handlers {
		msg := makeAnnouncementMsg(t, sc.announcementID, "", sc.order)
		var err error
		switch h {
		case sendOrderConfirmation:
			err = consumer.HandleSendOrderConfirmation(msg)
		case markPaid:
			err = consumer.HandleMarkPaid(msg)
		}
		require.NoError(rt, err, "delivery %d to durable %d", i+1, h)
	}
	return final()
}

// TestOrderPaidConsumer_RedeliveryIsIdempotent checks the property of this
// file for ORDER.paid: each durable receives the announcement one to three
// times, in any order, whatever the Order's source, whether its confirmation
// was sent before, and whatever journey the fan had for the event.
func TestOrderPaidConsumer_RedeliveryIsIdempotent(t *testing.T) {
	t.Parallel()

	// @spec components/infrastructure/backend/process/event-delivery "Delivered twice"
	rapid.Check(t, func(rt *rapid.T) {
		sc := genOrderScenario(rt)
		var handlers []orderPaidHandler
		for range rapid.IntRange(1, 3).Draw(rt, "confirmations") {
			handlers = append(handlers, sendOrderConfirmation)
		}
		for range rapid.IntRange(1, 3).Draw(rt, "markPaids") {
			handlers = append(handlers, markPaid)
		}
		handlers = rapid.Permutation(handlers).Draw(rt, "order")

		want := runOrderPaid(t, rt, sc, []orderPaidHandler{sendOrderConfirmation, markPaid})
		got := runOrderPaid(t, rt, sc, handlers)

		require.Equal(rt, want, got)
	})
}
