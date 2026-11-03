package rdb_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/jackc/pgx/v5"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// fakeCards is an in-memory payment provider for the lottery stories: card
// authorizations are opened Pending, the fan's card authentication makes
// them Authorized, and each is captured or released at most once, as a
// PaymentIntent is. It counts the charges of each card authorization.
type fakeCards struct {
	mu      sync.Mutex
	byID    map[string]*entity.CardAuthorization
	charges map[string]int
	closed  map[string]bool
	// afterCharge, when set, runs after a capture charged the card and before
	// Capture returns, so a story can stop the worker there.
	afterCharge func(ctx context.Context, id string) error
}

var _ entity.CardAuthorizationRepository = (*fakeCards)(nil)

func newFakeCards() *fakeCards {
	return &fakeCards{byID: map[string]*entity.CardAuthorization{}, charges: map[string]int{}, closed: map[string]bool{}}
}

// Create opens a Pending card authorization for the purchase.
func (c *fakeCards) Create(_ context.Context, purchase entity.CardAuthorizationPurchase, amountJPY int64) (*entity.CardAuthorization, error) {
	if amountJPY <= 0 {
		return nil, apperr.New(codes.InvalidArgument, "amount must be positive")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	a := &entity.CardAuthorization{
		ID: "pi_fake_" + entity.NewID(), Amount: amountJPY, Status: entity.CardAuthorizationStatusPending, Purchase: purchase,
	}
	c.byID[a.ID] = a
	copied := *a
	copied.Secret = a.ID + "_secret"
	return &copied, nil
}

// authenticate completes the fan's card authentication with a Visa card.
func (c *fakeCards) authenticate(t *testing.T, id string) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	a, ok := c.byID[id]
	require.True(t, ok, "card authorization %s", id)
	require.Equal(t, entity.CardAuthorizationStatusPending, a.Status)
	a.Status, a.CardBrand = entity.CardAuthorizationStatusAuthorized, "visa"
}

// closeCard closes the card behind the card authorization: it can no longer
// be charged.
func (c *fakeCards) closeCard(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed[id] = true
}

// Get returns the card authorization.
func (c *fakeCards) Get(_ context.Context, id string) (*entity.CardAuthorization, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	a, ok := c.byID[id]
	if !ok {
		return nil, apperr.New(codes.NotFound, "no card authorization has the id")
	}
	copied := *a
	return &copied, nil
}

// Capture charges an Authorized card authorization once.
func (c *fakeCards) Capture(ctx context.Context, id string) error {
	c.mu.Lock()
	a, ok := c.byID[id]
	switch {
	case !ok:
		c.mu.Unlock()
		return apperr.New(codes.NotFound, "no card authorization has the id")
	case a.Status == entity.CardAuthorizationStatusCaptured:
		c.mu.Unlock()
		return nil
	case a.Status != entity.CardAuthorizationStatusAuthorized || c.closed[id]:
		c.mu.Unlock()
		return apperr.New(codes.FailedPrecondition, "the card authorization can no longer be charged")
	}
	a.Status = entity.CardAuthorizationStatusCaptured
	a.CapturedPayment = &entity.CapturedPayment{
		Provider: entity.PaymentProviderStripe, ProviderPaymentID: id, AmountJPY: a.Amount, Currency: "JPY",
		CardBrand: string(a.CardBrand), CardLast4: "4242",
	}
	c.charges[id]++
	hook := c.afterCharge
	c.mu.Unlock()
	if hook != nil {
		return hook(ctx, id)
	}
	return nil
}

// Release gives back a Pending or Authorized card authorization.
func (c *fakeCards) Release(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	a, ok := c.byID[id]
	switch {
	case !ok:
		return apperr.New(codes.NotFound, "no card authorization has the id")
	case a.Status == entity.CardAuthorizationStatusCaptured:
		return apperr.New(codes.FailedPrecondition, "the card authorization was captured")
	}
	a.Status = entity.CardAuthorizationStatusReleased
	return nil
}

// status returns the card authorization's status.
func (c *fakeCards) status(id string) entity.CardAuthorizationStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byID[id].Status
}

// charged returns how many times the card authorization was charged.
func (c *fakeCards) charged(id string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.charges[id]
}

// sentEmail is one email a storyMailer sent.
type sentEmail struct {
	to, subject, body string
}

// storyMailer sends the stories' confirmation and loss emails into memory,
// keeping each to one per Order or entry with the send times the
// repositories record, as the Postmark senders do.
type storyMailer struct {
	mu      sync.Mutex
	orders  entity.OrderRepository
	entries entity.LotteryEntryRepository
	sent    []sentEmail
}

var (
	_ entity.OrderConfirmationSender = (*storyMailer)(nil)
	_ entity.LotteryLossEmailSender  = (*storyMailer)(nil)
)

func (m *storyMailer) SendConfirmationEmail(ctx context.Context, orderID entity.OrderID, to string, msg entity.OrderConfirmationEmail) error {
	order, err := m.orders.Get(ctx, orderID)
	if err != nil {
		return err
	}
	if order.ConfirmationSendTime != nil {
		return nil
	}
	m.record(to, msg.Subject, msg.TextBody)
	return m.orders.MarkConfirmationSent(ctx, orderID, time.Now())
}

func (m *storyMailer) SendLossEmail(ctx context.Context, id entity.LotteryEntryID, to string, msg entity.LotteryLossEmail) error {
	entry, err := m.entries.Get(ctx, id)
	if err != nil {
		return err
	}
	if entry.LossEmailSendTime != nil {
		return nil
	}
	m.record(to, msg.Subject, msg.TextBody)
	return m.entries.MarkLossEmailSent(ctx, id, time.Now())
}

func (m *storyMailer) record(to, subject, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, sentEmail{to: to, subject: subject, body: body})
}

// to returns the emails sent to the address.
func (m *storyMailer) to(address string) []sentEmail {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []sentEmail
	for _, e := range m.sent {
		if e.to == address {
			out = append(out, e)
		}
	}
	return out
}

// pushStarts records the deliver-notification workflows the stories' push
// notifications start; the stories' fans register no browser.
type pushStarts struct {
	mu  sync.Mutex
	ids map[string]bool
}

var _ usecase.Workflows = (*pushStarts)(nil)

func (p *pushStarts) Start(_ context.Context, _, id string, _ any, _ ...usecase.StartOption) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ids == nil {
		p.ids = map[string]bool{}
	}
	p.ids[id] = true
	return nil
}

func (p *pushStarts) Send(context.Context, string, string, any) error { return nil }

func (p *pushStarts) Await(context.Context, string, time.Duration, any) error { return nil }

func (p *pushStarts) Cancel(context.Context, string) (bool, error) { return false, nil }

// announcedMessages returns the announcements of subject recorded after
// mark as the messages a durable consumer receives: the protobuf payload
// with the CloudEvents metadata.
func announcedMessages(t *testing.T, mark int64, subject string) []*message.Message {
	t.Helper()
	rows, err := testDB.Pool.Query(context.Background(),
		`SELECT payload FROM watermill_events_outbox WHERE "offset" > $1 AND payload->>'destination_topic' = $2 ORDER BY "offset"`,
		mark, subject)
	require.NoError(t, err)
	raws, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	require.NoError(t, err)
	out := make([]*message.Message, 0, len(raws))
	for _, raw := range raws {
		var a recordedAnnouncement
		require.NoError(t, json.Unmarshal(raw, &a))
		msg := message.NewMessage(a.ID, a.Payload)
		for k, v := range a.Metadata {
			msg.Metadata.Set(k, v)
		}
		out = append(out, msg)
	}
	return out
}

// routedCards captures, releases and reads a card authorization with the
// in-memory provider when it opened it, and with other otherwise: the worker
// of a first-come story charges both a presale's and a checkout's Orders.
type routedCards struct {
	fake  *fakeCards
	other entity.CardAuthorizationRepository
}

var _ entity.CardAuthorizationRepository = (*routedCards)(nil)

// pick returns the provider that holds the card authorization id.
func (r *routedCards) pick(id string) entity.CardAuthorizationRepository {
	r.fake.mu.Lock()
	_, ok := r.fake.byID[id]
	r.fake.mu.Unlock()
	if ok || r.other == nil {
		return r.fake
	}
	return r.other
}

// Create opens a card authorization with the in-memory provider.
func (r *routedCards) Create(ctx context.Context, purchase entity.CardAuthorizationPurchase, amountJPY int64) (*entity.CardAuthorization, error) {
	return r.fake.Create(ctx, purchase, amountJPY)
}

// Get reads the card authorization from the provider that holds it.
func (r *routedCards) Get(ctx context.Context, id string) (*entity.CardAuthorization, error) {
	return r.pick(id).Get(ctx, id)
}

// Capture charges the card authorization at the provider that holds it.
func (r *routedCards) Capture(ctx context.Context, id string) error {
	return r.pick(id).Capture(ctx, id)
}

// Release gives the card authorization back at the provider that holds it.
func (r *routedCards) Release(ctx context.Context, id string) error {
	return r.pick(id).Release(ctx, id)
}
