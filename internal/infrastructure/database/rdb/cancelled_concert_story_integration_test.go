package rdb_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/payment"
	"github.com/liverty-music/backend/internal/infrastructure/repository"
	"github.com/liverty-music/backend/internal/infrastructure/workflow"
	"github.com/liverty-music/backend/internal/usecase"
)

// The story tests of stories/remove-a-cancelled-concert run the cancellation
// and the refund notices against a real local Postgres and the real workflow
// runtime: a member of the Event's Organizer cancels it with
// SeriesUseCase.Cancel, the CONCERT.cancelled announcements are handed to
// their consumer, which starts a notice workflow per Order that the worker
// runs; the admin's `Refund all`, which calls OrderService.Refund once per
// Paid Order, refunds each Order with OrderUseCase.Refund, and the
// ORDER.refunded announcements start the refund notices the same way. The payment provider is an in-memory refunder
// and the mail service an in-memory mailer (storyMailer); the fans register
// no browser, so each push is recorded as a Notification without a send.

// storyRefunder refunds a charge in memory once per Order, as the payment
// provider does with the Order's idempotency key.
type storyRefunder struct {
	entity.OrderRepository
	mu      sync.Mutex
	refunds map[entity.OrderID]int64
}

func (r *storyRefunder) GetProviderChargeID(_ context.Context, providerPaymentID string) (string, error) {
	return "ch_" + strings.TrimPrefix(providerPaymentID, "pi_"), nil
}

func (r *storyRefunder) CreateRefund(_ context.Context, params entity.RefundParams) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.refunds[params.OrderID]; !ok {
		r.refunds[params.OrderID] = params.Amount
	}
	return "re_" + string(params.OrderID), nil
}

// noStoryMetrics records nothing.
type noStoryMetrics struct{}

func (noStoryMetrics) RecordPushSend(context.Context, string)                {}
func (noStoryMetrics) RecordDeliveryOutcome(context.Context, string, string) {}

// cancelledConcertStory is one first-party Event with the Paid Orders of two
// fans, the worker running the notice workflows, and the consumer of the
// cancellation and refund announcements.
type cancelledConcertStory struct {
	t           *testing.T
	ctx         context.Context
	eventID     string
	seriesID    string
	organizerID string
	series      usecase.SeriesUseCase
	orders      []entity.OrderID
	fans        []string
	emails      []string

	mailer   *storyMailer
	refunder *storyRefunder
	refunds  usecase.OrderUseCase
	consumer *event.CancellationNoticeConsumer
	client   *workflow.Client
	mark     int64
}

func newCancelledConcertStory(t *testing.T) *cancelledConcertStory {
	t.Helper()
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	_, err := testDB.Pool.Exec(context.Background(), `TRUNCATE dbos.workflow_status, dbos.operation_outputs, dbos.workflow_input,
		dbos.workflow_output, dbos.workflow_events, dbos.workflow_events_history, dbos.notifications, dbos.streams CASCADE`)
	require.NoError(t, err)
	logger, err := logging.New()
	require.NoError(t, err)

	s := &cancelledConcertStory{t: t, ctx: context.Background()}
	f := seedStock(t, entity.TicketSaleMethodLottery, 10, 4)
	s.eventID = f.EventID
	require.NoError(t, testDB.Pool.QueryRow(s.ctx,
		`SELECT s.id, s.organizer_id FROM events e JOIN series s ON s.id = e.series_id WHERE e.id = $1`, s.eventID).
		Scan(&s.seriesID, &s.organizerID))
	organizerID := seedOrganizer(t)
	for range 2 {
		fan := seedFanUser(t)
		tickets := seedIssuedTicketsOf(t, f, fan, 2)
		var orderID, email string
		require.NoError(t, testDB.Pool.QueryRow(s.ctx, `SELECT order_id FROM tickets WHERE id = $1`, string(tickets[0])).Scan(&orderID))
		require.NoError(t, testDB.Pool.QueryRow(s.ctx, `SELECT email FROM users WHERE id = $1`, fan).Scan(&email))
		_, err := testDB.Pool.Exec(s.ctx, `UPDATE orders SET card_brand = 'visa', card_last4 = '4242' WHERE id = $1`, orderID)
		require.NoError(t, err)
		settlementID := entity.NewID()
		_, err = testDB.Pool.Exec(s.ctx, `
			INSERT INTO settlements (id, order_id, organizer_id, event_id, status, created_at) VALUES ($1, $2, $3, $4, 1, $5)`,
			settlementID, orderID, organizerID, s.eventID, issuedTicketsTime)
		require.NoError(t, err)
		insertSettlementSplit(t, entity.SettlementID(settlementID), organizerID, 5400)
		s.orders = append(s.orders, entity.OrderID(orderID))
		s.fans = append(s.fans, fan)
		s.emails = append(s.emails, email)
	}

	orderRepo := orderRepository(t, rdb.NewOrderRepository(testDB))
	s.refunder = &storyRefunder{OrderRepository: orderRepo, refunds: map[entity.OrderID]int64{}}
	ticketRepo := rdb.NewTicketRepository(testDB, testOutbox)
	s.mailer = &storyMailer{orders: orderRepo}
	notifications := func(workflows usecase.Workflows) usecase.NotificationUseCase {
		return usecase.NewNotificationUseCase(rdb.NewNotificationRepository(testDB),
			rdb.NewPushSubscriptionRepository(testDB, testOutbox), nil, workflows, noStoryMetrics{}, logger,
			usecase.OrderConfirmationDeps{
				Orders:   orderRepo,
				Users:    rdb.NewUserRepository(testDB, testOutbox),
				Concerts: rdb.NewConcertRepository(testDB, testOutbox),
				Tickets:  ticketRepo,
				Mailer:   s.mailer,
				TimeZone: time.UTC,
			}, usecase.NewConcertsDeps{}, usecase.SubjectDeps{
				Users:         rdb.NewUserRepository(testDB, testOutbox),
				Follows:       rdb.NewFollowRepository(testDB, testOutbox),
				Artists:       rdb.NewArtistRepository(testDB, testOutbox),
				Concerts:      rdb.NewConcertRepository(testDB, testOutbox),
				Orders:        orderRepo,
				TicketSales:   rdb.NewTicketSaleRepository(testDB, testOutbox),
				Entries:       rdb.NewLotteryEntryRepository(testDB, testOutbox),
				ResaleEntries: rdb.NewResaleEntryRepository(testDB, testOutbox),
			})
	}

	// The worker runs the notices and their pushes, as cmd/worker registers
	// them.
	rt, err := workflow.NewRuntime(s.ctx, s.newPool(), logger)
	require.NoError(t, err)
	worker := notifications(nil)
	workflow.Register(rt, usecase.WorkflowDeliverNotification, worker.RunDeliver)
	workflow.Register(rt, usecase.WorkflowNotifyEventCancelled, worker.RunNotifyEventCancelled)
	workflow.Register(rt, usecase.WorkflowNotifyOrderRefunded, worker.RunNotifyOrderRefunded)
	require.NoError(t, rt.Launch())
	t.Cleanup(func() { _ = rt.Close() })
	s.client, err = workflow.NewClient(s.ctx, s.newPool(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.client.Close() })

	// The event consumer only starts the notices.
	s.consumer = event.NewCancellationNoticeConsumer(notifications(s.client), logger)
	// The admin API refunds through the in-memory refunder.
	s.refunds = usecase.NewOrderUseCase(usecase.OrderDeps{
		OrderRepo:  s.refunder,
		TicketRepo: ticketRepo,
		EventRepo:  rdb.NewEventRepository(testDB),
		RefundRepo: rdb.NewRefundRepository(testDB, testOutbox),
		// Held Settlements are reversed without a payout to claw back.
		SettlementRepo: repository.NewSettlementRepository(rdb.NewSettlementRepository(testDB), payment.NewNoopSettlementPayouts(logger)),
		Clock:          time.Now,
		Logger:         logger,
	})
	// The organizer console's SeriesUseCase: Cancel reads the authored series
	// and cancels through the series repository.
	s.series = usecase.NewSeriesUseCase(rdb.NewSeriesRepository(testDB, testOutbox), rdb.NewSeriesImageRepository(testDB, testOutbox),
		rdb.NewVenueRepository(testDB), nil, rdb.NewOrganizerRepository(testDB, testOutbox), rdb.NewTicketSaleRepository(testDB, testOutbox),
		nil, usecase.StorageBuckets{}, logger, time.Now)
	s.mark = outboxMark(t)
	return s
}

// newPool returns the database pool of one simulated process.
func (s *cancelledConcertStory) newPool() *pgxpool.Pool {
	s.t.Helper()
	pool, err := pgxpool.New(s.ctx, fmt.Sprintf("postgres://test-user@localhost:%d/%s?sslmode=disable", testDBPort(), testDBName()))
	require.NoError(s.t, err)
	s.t.Cleanup(pool.Close)
	return pool
}

// deliver hands the announcements of subject recorded since the last
// hand-over to the consumer, then awaits the workflow named after each Order
// with prefix and its push.
func (s *cancelledConcertStory) deliver(subject string, handle func(*message.Message) error, prefix string, push entity.NotificationType) {
	s.t.Helper()
	msgs := announcedMessages(s.t, s.mark, subject)
	require.NotEmpty(s.t, msgs, "%s announced", subject)
	for _, msg := range msgs {
		require.NoError(s.t, handle(msg))
	}
	for _, o := range s.orders {
		require.NoError(s.t, s.client.Await(s.ctx, prefix+string(o), storyWait, nil), "%s%s", prefix, o)
		require.NoError(s.t, s.client.Await(s.ctx, "deliver:"+string(push)+":"+string(o), storyWait, nil), "push of %s", o)
	}
}

// cancel has a member of the Organizer cancel the Event and the consumer
// start its notices.
func (s *cancelledConcertStory) cancel() {
	s.t.Helper()
	require.NoError(s.t, s.series.Cancel(s.ctx, s.organizerID, s.seriesID, []string{s.eventID}))
	s.deliver(entity.SubjectConcertCancelled, s.consumer.HandleEventCancelled, usecase.WorkflowNotifyEventCancelled+":", entity.NotificationTypeConcertCancelled)
}

// refundAll refunds every Paid Order of the Event for the cancellation, as
// the admin's `Refund all` does, and has the consumer start the notices.
func (s *cancelledConcertStory) refundAll() {
	s.t.Helper()
	for _, o := range s.orders {
		_, err := s.refunds.Refund(s.ctx, o, usecase.RefundReasonCancellation, time.Now())
		require.NoError(s.t, err)
	}
	s.deliver(entity.SubjectOrderRefunded, s.consumer.HandleOrderRefunded, usecase.WorkflowNotifyOrderRefunded+":", entity.NotificationTypeOrderRefunded)
}

// emailsTo returns the subjects and bodies of the emails sent to the fan i.
func (s *cancelledConcertStory) emailsTo(i int) []sentEmail {
	return s.mailer.to(s.emails[i])
}

// notifications counts the fan's recorded notifications of the type.
func (s *cancelledConcertStory) notifications(i int, typ entity.NotificationType) int {
	s.t.Helper()
	var n int
	require.NoError(s.t, testDB.Pool.QueryRow(s.ctx,
		`SELECT count(*) FROM notifications WHERE user_id = $1 AND type = $2`, s.fans[i], string(typ)).Scan(&n))
	return n
}

func TestCancelledConcertStory_CancelledAndRefunded(t *testing.T) {
	// @spec stories/remove-a-cancelled-concert "Cancelled and refunded"
	// @spec components/usecase/order/refund "Tickets voided"
	s := newCancelledConcertStory(t)

	s.cancel()
	for i := range s.fans {
		got := s.emailsTo(i)
		require.Len(t, got, 1, "fan %d gets one cancellation email", i)
		assert.Contains(t, got[0].subject, "公演中止")
		assert.Contains(t, got[0].body, "6,000円（税込）")
		assert.Equal(t, 1, s.notifications(i, entity.NotificationTypeConcertCancelled))
	}

	s.refundAll()
	for i := range s.fans {
		got := s.emailsTo(i)
		require.Len(t, got, 2, "fan %d gets one refund email after the cancellation email", i)
		assert.Contains(t, got[1].subject, "返金完了")
		assert.Contains(t, got[1].body, "6,000円（税込）")
		assert.Contains(t, got[1].body, "VISA 末尾4242")
		assert.Equal(t, 1, s.notifications(i, entity.NotificationTypeOrderRefunded))
	}
	for _, o := range s.orders {
		assert.Equal(t, int64(6000), s.refunder.refunds[o], "the fan's card was refunded the price of both tickets")
		got, err := s.refunder.Get(s.ctx, o)
		require.NoError(t, err)
		assert.Equal(t, entity.OrderStatusRefunded, got.Status)
		var issued int
		require.NoError(t, testDB.Pool.QueryRow(s.ctx, `SELECT count(*) FROM tickets WHERE order_id = $1 AND status = $2`,
			string(o), int16(entity.TicketStatusIssued)).Scan(&issued))
		assert.Zero(t, issued, "both Tickets of the Order are Voided")
	}

	// A repeated delivery of every announcement sends nothing new.
	s.mark = 0
	for _, msg := range announcedMessages(t, s.mark, entity.SubjectConcertCancelled) {
		require.NoError(t, s.consumer.HandleEventCancelled(msg))
	}
	for _, msg := range announcedMessages(t, s.mark, entity.SubjectOrderRefunded) {
		require.NoError(t, s.consumer.HandleOrderRefunded(msg))
	}
	for _, o := range s.orders {
		require.NoError(t, s.client.Await(s.ctx, usecase.WorkflowNotifyOrderRefunded+":"+string(o), storyWait, nil))
	}
	for i := range s.fans {
		assert.Len(t, s.emailsTo(i), 2, "fan %d gets each notice once", i)
	}
}

func TestCancelledConcertStory_DeletedRightAfterTheRefunds(t *testing.T) {
	// @spec stories/remove-a-cancelled-concert "Deleted right after the refunds"
	s := newCancelledConcertStory(t)
	s.cancel()
	s.refundAll()

	require.NoError(t, rdb.NewConcertRepository(testDB, testOutbox).DeleteAndSuppress(s.ctx, s.eventID))

	assert.False(t, rowExists(t, `SELECT 1 FROM events WHERE id = $1`, s.eventID), "the Event no longer exists")
	for i := range s.fans {
		got := s.emailsTo(i)
		require.Len(t, got, 2, "fan %d keeps the cancellation and the refund email", i)
		assert.Contains(t, got[1].subject, "返金完了")
	}
}
