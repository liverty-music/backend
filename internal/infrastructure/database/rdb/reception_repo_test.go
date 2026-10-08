package rdb_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/testutil"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

var jst = time.FixedZone("JST", 9*60*60)

// nov20 returns 2026-11-20 hh:mm in Japan time.
func nov20(hh, mm int) time.Time {
	return time.Date(2026, 11, 20, hh, mm, 0, 0, jst)
}

// seedReceptionEvent inserts an event on 2026-11-20 and returns its id.
func seedReceptionEvent(t *testing.T) string {
	t.Helper()
	artistID := seedArtist(t, "reception-artist", uuid.NewV7().String())
	venueID := seedVenue(t, "reception-venue-"+uuid.NewV7().String())
	return seedEvent(t, venueID, artistID, "reception-concert", "2026-11-20")
}

// seedHeldTickets issues n Issued tickets of eventID to holderID (through a
// lottery phase, a Won application and a Paid order) and returns their ids
// in issue order.
func seedHeldTickets(t *testing.T, eventID, holderID string, n int) []entity.TicketID {
	t.Helper()
	ctx := context.Background()
	phaseRepo := rdb.NewLotteryPhaseRepository(testDB)
	appRepo := rdb.NewTicketApplicationRepository(testDB)

	open := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	phase, err := phaseRepo.Create(ctx, &entity.LotterySalesPhase{
		ID:                       entity.LotteryPhaseID(entity.NewID()),
		EventID:                  eventID,
		OpenTime:                 open,
		CloseTime:                open.Add(7 * 24 * time.Hour),
		TicketCapacity:           100,
		MaxTicketsPerApplication: 10,
		TicketPrice:              5000,
	})
	require.NoError(t, err)
	app := seedApplication(t, appRepo, phase.ID, holderID, entity.TicketApplicationStateWon)

	orderID := entity.NewID()
	_, err = testDB.Pool.Exec(ctx, `
		INSERT INTO orders (id, buyer_id, application_id, provider, payment_intent_ref, status, amount, currency, paid_at)
		VALUES ($1, $2, $3, 1, $4, 1, $5, 'JPY', $6)`,
		orderID, holderID, string(app.ID), "pi_"+orderID, 5000*n, open)
	require.NoError(t, err)

	ids := make([]entity.TicketID, 0, n)
	for i := range n {
		id := entity.NewID()
		_, err := testDB.Pool.Exec(ctx, `
			INSERT INTO tickets (id, order_id, holder_id, event_id, holder_full_name, holder_phone_number, status, issued_at)
			VALUES ($1, $2, $3, $4, '山田太郎', '+819012345678', 1, $5)`,
			id, orderID, holderID, eventID, open.Add(time.Duration(i)*time.Second))
		require.NoError(t, err)
		ids = append(ids, entity.TicketID(id))
	}
	return ids
}

// seedLink creates a reception link for eventID.
func seedLink(t *testing.T, eventID string) *entity.ReceptionLink {
	t.Helper()
	link, err := rdb.NewReceptionLinkRepository(testDB).Create(context.Background(), entity.NewReceptionLink(eventID))
	require.NoError(t, err)
	return link
}

func TestTicketRepository_Admit(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewTicketRepository(testDB)
	admissions := rdb.NewAdmissionRepository(testDB)
	links := rdb.NewReceptionLinkRepository(testDB)

	t.Run("first admission", func(t *testing.T) {
		// @spec components/entity/ticket/admit "First admission"
		cleanDatabase(t)
		eventID := seedReceptionEvent(t)
		holder := seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
		ticket := seedHeldTickets(t, eventID, holder, 1)[0]
		link := seedLink(t, eventID)

		got, err := repo.Admit(ctx, ticket, link.ID, nov20(18, 32))
		require.NoError(t, err)
		assert.Equal(t, entity.AdmitOutcomeAdmitted, got.Outcome)
		assert.True(t, got.AdmittedTime.Equal(nov20(18, 32)))

		held, err := repo.ListByHolderAndEvent(ctx, entity.UserID(holder), eventID)
		require.NoError(t, err)
		require.NotNil(t, held[0].AdmittedTime)
		assert.True(t, held[0].AdmittedTime.Equal(nov20(18, 32)))

		admission, err := admissions.GetByTicket(ctx, ticket)
		require.NoError(t, err)
		assert.Equal(t, link.ID, admission.ReceptionLinkID)
		assert.Equal(t, 1, admission.ReceptionLinkNumber)
		assert.Equal(t, eventID, admission.EventID)
		assert.True(t, admission.AdmittedTime.Equal(nov20(18, 32)))
	})

	t.Run("second admission", func(t *testing.T) {
		// @spec components/entity/ticket/admit "Second admission"
		cleanDatabase(t)
		eventID := seedReceptionEvent(t)
		holder := seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
		ticket := seedHeldTickets(t, eventID, holder, 1)[0]
		link := seedLink(t, eventID)

		_, err := repo.Admit(ctx, ticket, link.ID, nov20(18, 32))
		require.NoError(t, err)
		got, err := repo.Admit(ctx, ticket, link.ID, nov20(18, 40))
		require.NoError(t, err)
		assert.Equal(t, entity.AdmitOutcomeAlreadyAdmitted, got.Outcome)
		assert.True(t, got.AdmittedTime.Equal(nov20(18, 32)))

		admission, err := admissions.GetByTicket(ctx, ticket)
		require.NoError(t, err)
		assert.True(t, admission.AdmittedTime.Equal(nov20(18, 32)), "the admitted time stays 18:32")
	})

	t.Run("concurrent admissions", func(t *testing.T) {
		// @spec components/entity/ticket/admit "Concurrent admissions"
		cleanDatabase(t)
		eventID := seedReceptionEvent(t)
		holder := seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
		ticket := seedHeldTickets(t, eventID, holder, 1)[0]
		link1 := seedLink(t, eventID)
		link2 := seedLink(t, eventID)

		const callers = 8
		var (
			wg       sync.WaitGroup
			mu       sync.Mutex
			outcomes []entity.AdmitOutcome
		)
		start := make(chan struct{})
		for i := range callers {
			link := link1
			if i%2 == 1 {
				link = link2
			}
			wg.Go(func() {
				<-start
				got, err := repo.Admit(ctx, ticket, link.ID, nov20(18, 32))
				assert.NoError(t, err)
				mu.Lock()
				outcomes = append(outcomes, got.Outcome)
				mu.Unlock()
			})
		}
		close(start)
		wg.Wait()

		admitted := 0
		for _, o := range outcomes {
			if o == entity.AdmitOutcomeAdmitted {
				admitted++
			} else {
				assert.Equal(t, entity.AdmitOutcomeAlreadyAdmitted, o)
			}
		}
		assert.Equal(t, 1, admitted, "exactly one concurrent Admit reports Admitted")

		var count int
		require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT count(*) FROM admissions WHERE ticket_id = $1`, string(ticket)).Scan(&count))
		assert.Equal(t, 1, count, "exactly one Admission is stored")
	})

	t.Run("voided ticket", func(t *testing.T) {
		// @spec components/entity/ticket/admit "Voided ticket"
		cleanDatabase(t)
		eventID := seedReceptionEvent(t)
		holder := seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
		ticket := seedHeldTickets(t, eventID, holder, 1)[0]
		link := seedLink(t, eventID)
		_, err := testDB.Pool.Exec(ctx, `UPDATE tickets SET status = 2 WHERE id = $1`, string(ticket))
		require.NoError(t, err)

		got, err := repo.Admit(ctx, ticket, link.ID, nov20(18, 32))
		require.NoError(t, err)
		assert.Equal(t, entity.AdmitOutcomeVoided, got.Outcome)

		_, err = admissions.GetByTicket(ctx, ticket)
		assert.ErrorIs(t, err, apperr.ErrNotFound, "nothing is recorded")
	})

	t.Run("unknown ticket", func(t *testing.T) {
		// @spec components/entity/ticket/admit "Unknown ticket"
		cleanDatabase(t)
		eventID := seedReceptionEvent(t)
		link := seedLink(t, eventID)

		_, err := repo.Admit(ctx, entity.TicketID(entity.NewID()), link.ID, nov20(18, 32))
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("link revoked before the admission", func(t *testing.T) {
		cleanDatabase(t)
		eventID := seedReceptionEvent(t)
		holder := seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
		ticket := seedHeldTickets(t, eventID, holder, 1)[0]
		link := seedLink(t, eventID)
		_, err := links.Revoke(ctx, link.ID, nov20(18, 30))
		require.NoError(t, err)

		_, err = repo.Admit(ctx, ticket, link.ID, nov20(18, 32))
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		assert.ErrorIs(t, err, entity.ErrReceptionLinkNotUsable)

		held, err := repo.ListByHolderAndEvent(ctx, entity.UserID(holder), eventID)
		require.NoError(t, err)
		assert.Nil(t, held[0].AdmittedTime, "nothing is admitted through a revoked link")
	})

	t.Run("voided after entry keeps the admitted time and the admission", func(t *testing.T) {
		// @spec components/entity/ticket "Voided after entry"
		// @spec components/entity/admission "Ticket voided after entry"
		cleanDatabase(t)
		eventID := seedReceptionEvent(t)
		holder := seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
		ticket := seedHeldTickets(t, eventID, holder, 1)[0]
		link := seedLink(t, eventID)
		_, err := repo.Admit(ctx, ticket, link.ID, nov20(18, 32))
		require.NoError(t, err)

		held, err := repo.ListByHolderAndEvent(ctx, entity.UserID(holder), eventID)
		require.NoError(t, err)
		require.NoError(t, repo.VoidByOrder(ctx, held[0].OrderID))

		held, err = repo.ListByHolderAndEvent(ctx, entity.UserID(holder), eventID)
		require.NoError(t, err)
		assert.Equal(t, entity.TicketStatusVoided, held[0].Status)
		require.NotNil(t, held[0].AdmittedTime)
		assert.True(t, held[0].AdmittedTime.Equal(nov20(18, 32)))

		admission, err := admissions.GetByTicket(ctx, ticket)
		require.NoError(t, err)
		assert.True(t, admission.AdmittedTime.Equal(nov20(18, 32)))
		assert.Equal(t, link.ID, admission.ReceptionLinkID)
	})
}

func TestTicketRepository_ListByHolderAndEvent(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewTicketRepository(testDB)

	t.Run("group of tickets", func(t *testing.T) {
		// @spec components/entity/ticket/list-by-holder-and-event "Group of tickets"
		cleanDatabase(t)
		eventID := seedReceptionEvent(t)
		otherEvent := seedReceptionEvent(t)
		holder := seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
		group := seedHeldTickets(t, eventID, holder, 3)
		seedHeldTickets(t, otherEvent, holder, 1)
		_, err := testDB.Pool.Exec(ctx, `UPDATE tickets SET status = 2 WHERE id = $1`, string(group[2]))
		require.NoError(t, err)

		got, err := repo.ListByHolderAndEvent(ctx, entity.UserID(holder), eventID)
		require.NoError(t, err)
		require.Len(t, got, 3)
		for i, tk := range got {
			assert.Equal(t, group[i], tk.ID, "issue order")
			assert.Equal(t, eventID, tk.EventID)
		}
		assert.Equal(t, entity.TicketStatusVoided, got[2].Status, "Voided tickets are listed too")
	})

	t.Run("no tickets for the event", func(t *testing.T) {
		// @spec components/entity/ticket/list-by-holder-and-event "No tickets for the event"
		cleanDatabase(t)
		eventID := seedReceptionEvent(t)
		holder := seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())

		got, err := repo.ListByHolderAndEvent(ctx, entity.UserID(holder), eventID)
		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})
}

func TestWalletPublicKeyRepository(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewWalletPublicKeyRepository(testDB)

	newUser := func(t *testing.T) entity.UserID {
		t.Helper()
		return entity.UserID(seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String()))
	}

	t.Run("first device", func(t *testing.T) {
		// @spec components/entity/wallet-public-key/register "First device"
		cleanDatabase(t)
		user := newUser(t)
		k1 := testutil.NewDeviceKey(t).PublicKey(t)

		got, replaced, err := repo.Register(ctx, user, k1, nov20(9, 0))
		require.NoError(t, err)
		assert.False(t, replaced)
		assert.Equal(t, k1, got.PublicKey)

		stored, err := repo.GetByUser(ctx, user)
		require.NoError(t, err)
		assert.Equal(t, k1, stored.PublicKey)
	})

	t.Run("new phone", func(t *testing.T) {
		// @spec components/entity/wallet-public-key/register "New phone"
		// @spec components/entity/wallet-public-key/get-by-user "User with a key"
		cleanDatabase(t)
		user := newUser(t)
		k1 := testutil.NewDeviceKey(t).PublicKey(t)
		k2 := testutil.NewDeviceKey(t).PublicKey(t)
		_, _, err := repo.Register(ctx, user, k1, nov20(9, 0))
		require.NoError(t, err)

		got, replaced, err := repo.Register(ctx, user, k2, nov20(10, 0))
		require.NoError(t, err)
		assert.True(t, replaced)
		assert.Equal(t, k2, got.PublicKey)
		assert.True(t, got.RegisteredTime.Equal(nov20(10, 0)))

		stored, err := repo.GetByUser(ctx, user)
		require.NoError(t, err)
		assert.Equal(t, k2, stored.PublicKey)
		assert.True(t, stored.RegisteredTime.Equal(nov20(10, 0)))
	})

	t.Run("same key again", func(t *testing.T) {
		// @spec components/entity/wallet-public-key/register "Same key again"
		cleanDatabase(t)
		user := newUser(t)
		k1 := testutil.NewDeviceKey(t).PublicKey(t)
		_, _, err := repo.Register(ctx, user, k1, nov20(9, 0))
		require.NoError(t, err)

		got, replaced, err := repo.Register(ctx, user, k1, nov20(10, 0))
		require.NoError(t, err)
		assert.False(t, replaced)
		assert.True(t, got.RegisteredTime.Equal(nov20(9, 0)), "nothing changes")
	})

	t.Run("invalid key", func(t *testing.T) {
		// @spec components/entity/wallet-public-key/register "Invalid key"
		cleanDatabase(t)
		user := newUser(t)
		k1 := testutil.NewDeviceKey(t).PublicKey(t)
		_, _, err := repo.Register(ctx, user, k1, nov20(9, 0))
		require.NoError(t, err)

		notAPoint := append(entity.PublicKey{0x04}, make([]byte, 64)...)
		_, _, err = repo.Register(ctx, user, notAPoint, nov20(10, 0))
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)

		stored, err := repo.GetByUser(ctx, user)
		require.NoError(t, err)
		assert.Equal(t, k1, stored.PublicKey, "the user's key is unchanged")
	})

	t.Run("user without a key", func(t *testing.T) {
		// @spec components/entity/wallet-public-key/get-by-user "User without a key"
		cleanDatabase(t)
		_, err := repo.GetByUser(ctx, newUser(t))
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("concurrent registrations keep one key and report one replacement each", func(t *testing.T) {
		cleanDatabase(t)
		user := newUser(t)
		var wg sync.WaitGroup
		for range 4 {
			key := testutil.NewDeviceKey(t).PublicKey(t)
			wg.Go(func() {
				_, _, err := repo.Register(ctx, user, key, nov20(9, 0))
				assert.NoError(t, err)
			})
		}
		wg.Wait()
		var count int
		require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT count(*) FROM wallet_public_keys WHERE user_id = $1`, string(user)).Scan(&count))
		assert.Equal(t, 1, count)
	})
}

func TestReceptionLinkRepository(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewReceptionLinkRepository(testDB)

	t.Run("numbering and creation", func(t *testing.T) {
		cleanDatabase(t)
		eventID := seedReceptionEvent(t)

		// @spec components/entity/reception-link "First link"
		first := seedLink(t, eventID)
		assert.Equal(t, 1, first.Number)
		assert.Equal(t, entity.ReceptionLinkStatusUnused, first.Status)
		assert.NotEmpty(t, first.Token)

		second := seedLink(t, eventID)
		_, err := repo.Revoke(ctx, second.ID, nov20(12, 0))
		require.NoError(t, err)

		// @spec components/entity/reception-link "After a revoked link"
		// @spec components/entity/reception-link/create "New link stored"
		third, err := repo.Create(ctx, entity.NewReceptionLink(eventID))
		require.NoError(t, err)
		assert.Equal(t, 3, third.Number)
		assert.Equal(t, entity.ReceptionLinkStatusUnused, third.Status)
		assert.NotEmpty(t, third.Token)

		other := seedLink(t, seedReceptionEvent(t))
		assert.Equal(t, 1, other.Number, "numbers are per event")
	})

	t.Run("two links created at once", func(t *testing.T) {
		// @spec components/entity/reception-link/create "Two links created at once"
		cleanDatabase(t)
		eventID := seedReceptionEvent(t)

		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			numbers []int
		)
		start := make(chan struct{})
		for range 2 {
			wg.Go(func() {
				<-start
				link, err := repo.Create(ctx, entity.NewReceptionLink(eventID))
				assert.NoError(t, err)
				if link != nil {
					mu.Lock()
					numbers = append(numbers, link.Number)
					mu.Unlock()
				}
			})
		}
		close(start)
		wg.Wait()
		assert.ElementsMatch(t, []int{1, 2}, numbers)
	})

	t.Run("create for an unknown event", func(t *testing.T) {
		cleanDatabase(t)
		_, err := repo.Create(ctx, entity.NewReceptionLink(entity.NewID()))
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("get", func(t *testing.T) {
		cleanDatabase(t)
		link := seedLink(t, seedReceptionEvent(t))
		_, err := repo.Revoke(ctx, link.ID, nov20(12, 0))
		require.NoError(t, err)

		// @spec components/entity/reception-link/get "Existing link"
		got, err := repo.Get(ctx, link.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ReceptionLinkStatusRevoked, got.Status)

		// @spec components/entity/reception-link/get "Unknown link"
		_, err = repo.Get(ctx, entity.ReceptionLinkID(entity.NewID()))
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("get by token", func(t *testing.T) {
		cleanDatabase(t)
		link := seedLink(t, seedReceptionEvent(t))

		// @spec components/entity/reception-link/get-by-token "Known token"
		got, err := repo.GetByToken(ctx, link.Token)
		require.NoError(t, err)
		assert.Equal(t, link.ID, got.ID)

		// @spec components/entity/reception-link/get-by-token "Unknown token"
		_, err = repo.GetByToken(ctx, entity.NewReceptionLink("x").Token)
		assert.ErrorIs(t, err, apperr.ErrNotFound)

		// The token keeps finding the link after it is bound, but is no longer
		// returned.
		key := testutil.NewDeviceKey(t).PublicKey(t)
		_, _, err = repo.BindDevice(ctx, link.ID, key, nov20(14, 10))
		require.NoError(t, err)
		bound, err := repo.GetByToken(ctx, link.Token)
		require.NoError(t, err)
		assert.Equal(t, link.ID, bound.ID)
		assert.Empty(t, bound.Token)
	})

	t.Run("list by event", func(t *testing.T) {
		cleanDatabase(t)
		eventID := seedReceptionEvent(t)
		l1 := seedLink(t, eventID)
		l2 := seedLink(t, eventID)
		_, _, err := repo.BindDevice(ctx, l1.ID, testutil.NewDeviceKey(t).PublicKey(t), nov20(14, 10))
		require.NoError(t, err)
		_, err = repo.Revoke(ctx, l2.ID, nov20(15, 0))
		require.NoError(t, err)

		// @spec components/entity/reception-link/list-by-event "Event with links"
		got, err := repo.ListByEvent(ctx, eventID)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, 1, got[0].Number)
		assert.Equal(t, entity.ReceptionLinkStatusInUse, got[0].Status)
		assert.Equal(t, 2, got[1].Number)
		assert.Equal(t, entity.ReceptionLinkStatusRevoked, got[1].Status)

		// @spec components/entity/reception-link/list-by-event "Event without links"
		empty, err := repo.ListByEvent(ctx, seedReceptionEvent(t))
		require.NoError(t, err)
		assert.NotNil(t, empty)
		assert.Empty(t, empty)
	})

	t.Run("bind device", func(t *testing.T) {
		cleanDatabase(t)
		eventID := seedReceptionEvent(t)
		p1 := testutil.NewDeviceKey(t).PublicKey(t)
		p2 := testutil.NewDeviceKey(t).PublicKey(t)
		link := seedLink(t, eventID)

		// @spec components/entity/reception-link/bind-device "First device"
		outcome, got, err := repo.BindDevice(ctx, link.ID, p1, nov20(14, 10))
		require.NoError(t, err)
		assert.Equal(t, entity.BindOutcomeBound, outcome)
		assert.Equal(t, entity.ReceptionLinkStatusInUse, got.Status)
		assert.Equal(t, p1, got.BoundPublicKey)
		require.NotNil(t, got.BoundTime)
		assert.True(t, got.BoundTime.Equal(nov20(14, 10)))
		assert.Empty(t, got.Token, "the token is not kept once bound")

		// @spec components/entity/reception-link/bind-device "Same device again"
		outcome, got, err = repo.BindDevice(ctx, link.ID, p1, nov20(15, 0))
		require.NoError(t, err)
		assert.Equal(t, entity.BindOutcomeBound, outcome)
		assert.True(t, got.BoundTime.Equal(nov20(14, 10)), "nothing changes")

		// @spec components/entity/reception-link/bind-device "Another device"
		outcome, got, err = repo.BindDevice(ctx, link.ID, p2, nov20(15, 0))
		require.NoError(t, err)
		assert.Equal(t, entity.BindOutcomeOtherDevice, outcome)
		assert.Equal(t, p1, got.BoundPublicKey)

		// @spec components/entity/reception-link/bind-device "Revoked link"
		revoked := seedLink(t, eventID)
		_, err = repo.Revoke(ctx, revoked.ID, nov20(15, 0))
		require.NoError(t, err)
		outcome, got, err = repo.BindDevice(ctx, revoked.ID, p1, nov20(15, 5))
		require.NoError(t, err)
		assert.Equal(t, entity.BindOutcomeRevoked, outcome)
		assert.Nil(t, got.BoundPublicKey)

		_, _, err = repo.BindDevice(ctx, link.ID, entity.PublicKey{0x04, 0x01}, nov20(15, 0))
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		_, _, err = repo.BindDevice(ctx, entity.ReceptionLinkID(entity.NewID()), p1, nov20(15, 0))
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("two devices at once", func(t *testing.T) {
		// @spec components/entity/reception-link/bind-device "Two devices at once"
		cleanDatabase(t)
		link := seedLink(t, seedReceptionEvent(t))

		var (
			wg       sync.WaitGroup
			mu       sync.Mutex
			outcomes []entity.BindOutcome
		)
		start := make(chan struct{})
		for range 2 {
			key := testutil.NewDeviceKey(t).PublicKey(t)
			wg.Go(func() {
				<-start
				outcome, _, err := repo.BindDevice(ctx, link.ID, key, nov20(14, 10))
				assert.NoError(t, err)
				mu.Lock()
				outcomes = append(outcomes, outcome)
				mu.Unlock()
			})
		}
		close(start)
		wg.Wait()
		assert.ElementsMatch(t, []entity.BindOutcome{entity.BindOutcomeBound, entity.BindOutcomeOtherDevice}, outcomes)
	})

	t.Run("revoke", func(t *testing.T) {
		cleanDatabase(t)
		link := seedLink(t, seedReceptionEvent(t))
		_, _, err := repo.BindDevice(ctx, link.ID, testutil.NewDeviceKey(t).PublicKey(t), nov20(14, 10))
		require.NoError(t, err)

		// @spec components/entity/reception-link/revoke "Link in use"
		got, err := repo.Revoke(ctx, link.ID, nov20(18, 5))
		require.NoError(t, err)
		assert.Equal(t, entity.ReceptionLinkStatusRevoked, got.Status)
		require.NotNil(t, got.RevokedTime)
		assert.True(t, got.RevokedTime.Equal(nov20(18, 5)))

		// @spec components/entity/reception-link/revoke "Already revoked"
		got, err = repo.Revoke(ctx, link.ID, nov20(18, 10))
		require.NoError(t, err)
		assert.True(t, got.RevokedTime.Equal(nov20(18, 5)), "the revoked time stays 18:05")

		// @spec components/entity/reception-link/revoke "Unknown link"
		_, err = repo.Revoke(ctx, entity.ReceptionLinkID(entity.NewID()), nov20(18, 10))
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestRejectedScanRepository_Append(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewRejectedScanRepository(testDB)

	count := func(t *testing.T, linkID entity.ReceptionLinkID) int {
		t.Helper()
		var n int
		require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT count(*) FROM rejected_scans WHERE reception_link_id = $1`, string(linkID)).Scan(&n))
		return n
	}

	t.Run("group rejected", func(t *testing.T) {
		// @spec components/entity/rejected-scan/append "Group rejected"
		cleanDatabase(t)
		eventID := seedReceptionEvent(t)
		link := seedLink(t, eventID)
		scans := []*entity.RejectedScan{
			entity.NewRejectedScan(eventID, link.ID, entity.TicketID(entity.NewID()), entity.RejectedScanReasonExpired, nov20(18, 40)),
			entity.NewRejectedScan(eventID, link.ID, entity.TicketID(entity.NewID()), entity.RejectedScanReasonExpired, nov20(18, 40)),
			entity.NewRejectedScan(eventID, link.ID, entity.TicketID(entity.NewID()), entity.RejectedScanReasonExpired, nov20(18, 40)),
		}
		require.NoError(t, repo.Append(ctx, scans))
		assert.Equal(t, 3, count(t, link.ID))
	})

	t.Run("forged scan without a ticket", func(t *testing.T) {
		cleanDatabase(t)
		eventID := seedReceptionEvent(t)
		link := seedLink(t, eventID)
		require.NoError(t, repo.Append(ctx, []*entity.RejectedScan{
			entity.NewRejectedScan(eventID, link.ID, "", entity.RejectedScanReasonForged, nov20(18, 40)),
		}))
		assert.Equal(t, 1, count(t, link.ID))
	})

	t.Run("invalid one in the batch", func(t *testing.T) {
		// @spec components/entity/rejected-scan/append "Invalid one in the batch"
		cleanDatabase(t)
		eventID := seedReceptionEvent(t)
		link := seedLink(t, eventID)
		scans := []*entity.RejectedScan{
			entity.NewRejectedScan(eventID, link.ID, entity.TicketID(entity.NewID()), entity.RejectedScanReasonVoided, nov20(18, 40)),
			entity.NewRejectedScan(eventID, link.ID, entity.TicketID(entity.NewID()), entity.RejectedScanReasonVoided, nov20(18, 40)),
			entity.NewRejectedScan(eventID, link.ID, entity.TicketID(entity.NewID()), entity.RejectedScanReasonForged, nov20(18, 40)),
		}
		err := repo.Append(ctx, scans)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Equal(t, 0, count(t, link.ID), "none is stored")
	})

	t.Run("a database failure stores none", func(t *testing.T) {
		cleanDatabase(t)
		eventID := seedReceptionEvent(t)
		link := seedLink(t, eventID)
		scans := []*entity.RejectedScan{
			entity.NewRejectedScan(eventID, link.ID, entity.TicketID(entity.NewID()), entity.RejectedScanReasonVoided, nov20(18, 40)),
			// Unknown link: the foreign key fails inside the transaction.
			entity.NewRejectedScan(eventID, entity.ReceptionLinkID(entity.NewID()), entity.TicketID(entity.NewID()), entity.RejectedScanReasonVoided, nov20(18, 40)),
		}
		require.Error(t, repo.Append(ctx, scans))
		assert.Equal(t, 0, count(t, link.ID))
	})
}

func TestAdmissionRepository_GetByTicket(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	tickets := rdb.NewTicketRepository(testDB)
	repo := rdb.NewAdmissionRepository(testDB)

	t.Run("admitted ticket", func(t *testing.T) {
		// @spec components/entity/admission/get-by-ticket "Admitted ticket"
		cleanDatabase(t)
		eventID := seedReceptionEvent(t)
		holder := seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
		ticket := seedHeldTickets(t, eventID, holder, 1)[0]
		link := seedLink(t, eventID)
		_, err := tickets.Admit(ctx, ticket, link.ID, nov20(18, 32))
		require.NoError(t, err)

		got, err := repo.GetByTicket(ctx, ticket)
		require.NoError(t, err)
		assert.True(t, got.AdmittedTime.Equal(nov20(18, 32)))
		assert.Equal(t, 1, got.ReceptionLinkNumber)
	})

	t.Run("ticket not admitted", func(t *testing.T) {
		// @spec components/entity/admission/get-by-ticket "Ticket not admitted"
		cleanDatabase(t)
		eventID := seedReceptionEvent(t)
		holder := seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
		ticket := seedHeldTickets(t, eventID, holder, 1)[0]
		link := seedLink(t, eventID)
		require.NoError(t, rdb.NewRejectedScanRepository(testDB).Append(ctx, []*entity.RejectedScan{
			entity.NewRejectedScan(eventID, link.ID, ticket, entity.RejectedScanReasonNotHolder, nov20(18, 32)),
		}))

		_, err := repo.GetByTicket(ctx, ticket)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestEventRepository_Get(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewEventRepository(testDB)

	t.Run("event with both times", func(t *testing.T) {
		// @spec components/entity/event/get "Event with both times"
		cleanDatabase(t)
		eventID := seedReceptionEvent(t)
		_, err := testDB.Pool.Exec(ctx, `UPDATE events SET open_at = $2, start_at = $3 WHERE id = $1`, eventID, nov20(18, 0), nov20(19, 0))
		require.NoError(t, err)

		got, err := repo.Get(ctx, eventID)
		require.NoError(t, err)
		assert.Equal(t, "2026-11-20", got.LocalDate.Format(time.DateOnly))
		require.NotNil(t, got.OpenTime)
		require.NotNil(t, got.StartTime)
		assert.True(t, got.OpenTime.Equal(nov20(18, 0)))
		assert.True(t, got.StartTime.Equal(nov20(19, 0)))
	})

	t.Run("start time filled in later", func(t *testing.T) {
		// @spec components/entity/event/get "Start time filled in later"
		cleanDatabase(t)
		eventID := seedReceptionEvent(t)
		before, err := repo.Get(ctx, eventID)
		require.NoError(t, err)
		assert.Nil(t, before.StartTime)
		assert.Nil(t, before.OpenTime)

		_, err = testDB.Pool.Exec(ctx, `UPDATE events SET start_at = $2 WHERE id = $1`, eventID, nov20(19, 0))
		require.NoError(t, err)
		after, err := repo.Get(ctx, eventID)
		require.NoError(t, err)
		require.NotNil(t, after.StartTime)
		assert.True(t, after.StartTime.Equal(nov20(19, 0)))
	})

	t.Run("unknown event", func(t *testing.T) {
		// @spec components/entity/event/get "Unknown event"
		cleanDatabase(t)
		_, err := repo.Get(ctx, entity.NewID())
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
