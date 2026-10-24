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

// seedAdmissionEvent inserts an event on 2026-11-20 and returns its id.
func seedAdmissionEvent(t *testing.T) string {
	t.Helper()
	artistID := seedArtist(t, "admission-artist", uuid.NewV7().String())
	venueID := seedVenue(t, "admission-venue-"+uuid.NewV7().String())
	return seedEvent(t, venueID, artistID, "admission-concert", "2026-11-20")
}

// seedScanner creates a Scanner for eventID.
func seedScanner(t *testing.T, eventID string) *entity.Scanner {
	t.Helper()
	scanner, err := rdb.NewScannerRepository(testDB).Create(context.Background(), entity.NewScanner(eventID))
	require.NoError(t, err)
	return scanner
}

func TestTicketRepository_Admit(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewTicketRepository(testDB)
	admissions := rdb.NewAdmissionRepository(testDB)
	scanners := rdb.NewScannerRepository(testDB)

	t.Run("first admission", func(t *testing.T) {
		// @spec components/entity/ticket/admit "First admission"
		cleanDatabase(t)
		eventID := seedAdmissionEvent(t)
		fan := seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
		ticket := seedIssuedTickets(t, eventID, fan, 1)[0]
		scanner := seedScanner(t, eventID)

		got, err := repo.Admit(ctx, ticket, scanner.ID, nov20(18, 32))
		require.NoError(t, err)
		assert.Equal(t, entity.AdmitOutcomeAdmitted, got.Outcome)
		assert.True(t, got.AdmitTime.Equal(nov20(18, 32)))

		tickets, err := repo.ListByUserAndEvent(ctx, entity.UserID(fan), eventID)
		require.NoError(t, err)
		require.NotNil(t, tickets[0].AdmitTime)
		assert.True(t, tickets[0].AdmitTime.Equal(nov20(18, 32)))

		admission, err := admissions.GetByTicket(ctx, ticket)
		require.NoError(t, err)
		assert.Equal(t, scanner.ID, admission.ScannerID)
		assert.Equal(t, 1, admission.ScannerNumber)
		assert.Equal(t, eventID, admission.EventID)
		assert.True(t, admission.AdmitTime.Equal(nov20(18, 32)))
	})

	t.Run("second admission", func(t *testing.T) {
		// @spec components/entity/ticket/admit "Second admission"
		cleanDatabase(t)
		eventID := seedAdmissionEvent(t)
		fan := seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
		ticket := seedIssuedTickets(t, eventID, fan, 1)[0]
		scanner := seedScanner(t, eventID)

		_, err := repo.Admit(ctx, ticket, scanner.ID, nov20(18, 32))
		require.NoError(t, err)
		got, err := repo.Admit(ctx, ticket, scanner.ID, nov20(18, 40))
		require.NoError(t, err)
		assert.Equal(t, entity.AdmitOutcomeAlreadyAdmitted, got.Outcome)
		assert.True(t, got.AdmitTime.Equal(nov20(18, 32)))

		admission, err := admissions.GetByTicket(ctx, ticket)
		require.NoError(t, err)
		assert.True(t, admission.AdmitTime.Equal(nov20(18, 32)), "the admitted time stays 18:32")
	})

	t.Run("concurrent admissions", func(t *testing.T) {
		// @spec components/entity/ticket/admit "Concurrent admissions"
		cleanDatabase(t)
		eventID := seedAdmissionEvent(t)
		fan := seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
		ticket := seedIssuedTickets(t, eventID, fan, 1)[0]
		scanner1 := seedScanner(t, eventID)
		scanner2 := seedScanner(t, eventID)

		const callers = 8
		var (
			wg       sync.WaitGroup
			mu       sync.Mutex
			outcomes []entity.AdmitOutcome
		)
		start := make(chan struct{})
		for i := range callers {
			scanner := scanner1
			if i%2 == 1 {
				scanner = scanner2
			}
			wg.Go(func() {
				<-start
				got, err := repo.Admit(ctx, ticket, scanner.ID, nov20(18, 32))
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
		eventID := seedAdmissionEvent(t)
		fan := seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
		ticket := seedIssuedTickets(t, eventID, fan, 1)[0]
		scanner := seedScanner(t, eventID)
		_, err := testDB.Pool.Exec(ctx, `UPDATE tickets SET status = 2 WHERE id = $1`, string(ticket))
		require.NoError(t, err)

		got, err := repo.Admit(ctx, ticket, scanner.ID, nov20(18, 32))
		require.NoError(t, err)
		assert.Equal(t, entity.AdmitOutcomeVoided, got.Outcome)

		_, err = admissions.GetByTicket(ctx, ticket)
		assert.ErrorIs(t, err, apperr.ErrNotFound, "nothing is recorded")
	})

	t.Run("unknown ticket", func(t *testing.T) {
		// @spec components/entity/ticket/admit "Unknown ticket"
		cleanDatabase(t)
		eventID := seedAdmissionEvent(t)
		scanner := seedScanner(t, eventID)

		_, err := repo.Admit(ctx, entity.TicketID(entity.NewID()), scanner.ID, nov20(18, 32))
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("scanner revoked before the admission", func(t *testing.T) {
		cleanDatabase(t)
		eventID := seedAdmissionEvent(t)
		fan := seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
		ticket := seedIssuedTickets(t, eventID, fan, 1)[0]
		scanner := seedScanner(t, eventID)
		_, err := scanners.Revoke(ctx, scanner.ID, nov20(18, 30))
		require.NoError(t, err)

		_, err = repo.Admit(ctx, ticket, scanner.ID, nov20(18, 32))
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		assert.ErrorIs(t, err, entity.ErrScannerNotUsable)

		tickets, err := repo.ListByUserAndEvent(ctx, entity.UserID(fan), eventID)
		require.NoError(t, err)
		assert.Nil(t, tickets[0].AdmitTime, "nothing is admitted through a revoked scanner")
	})

	t.Run("voided after entry keeps the admitted time and the admission", func(t *testing.T) {
		// @spec components/entity/ticket "Voided after entry"
		// @spec components/entity/admission "Ticket voided after entry"
		cleanDatabase(t)
		eventID := seedAdmissionEvent(t)
		fan := seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
		ticket := seedIssuedTickets(t, eventID, fan, 1)[0]
		scanner := seedScanner(t, eventID)
		_, err := repo.Admit(ctx, ticket, scanner.ID, nov20(18, 32))
		require.NoError(t, err)

		tickets, err := repo.ListByUserAndEvent(ctx, entity.UserID(fan), eventID)
		require.NoError(t, err)
		_, err = testDB.Pool.Exec(ctx, `UPDATE tickets SET status = $2 WHERE order_id = $1`, string(tickets[0].OrderID), int16(entity.TicketStatusVoided))
		require.NoError(t, err)

		tickets, err = repo.ListByUserAndEvent(ctx, entity.UserID(fan), eventID)
		require.NoError(t, err)
		assert.Equal(t, entity.TicketStatusVoided, tickets[0].Status)
		require.NotNil(t, tickets[0].AdmitTime)
		assert.True(t, tickets[0].AdmitTime.Equal(nov20(18, 32)))

		admission, err := admissions.GetByTicket(ctx, ticket)
		require.NoError(t, err)
		assert.True(t, admission.AdmitTime.Equal(nov20(18, 32)))
		assert.Equal(t, scanner.ID, admission.ScannerID)
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

func TestScannerRepository(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewScannerRepository(testDB)

	t.Run("numbering and creation", func(t *testing.T) {
		cleanDatabase(t)
		eventID := seedAdmissionEvent(t)

		// @spec components/entity/scanner "First scanner"
		first := seedScanner(t, eventID)
		assert.Equal(t, 1, first.Number)
		assert.Equal(t, entity.ScannerStatusUnused, first.Status)
		assert.NotEmpty(t, first.LinkToken)

		second := seedScanner(t, eventID)
		_, err := repo.Revoke(ctx, second.ID, nov20(12, 0))
		require.NoError(t, err)

		// @spec components/entity/scanner "After a revoked scanner"
		// @spec components/entity/scanner/create "New scanner stored"
		third, err := repo.Create(ctx, entity.NewScanner(eventID))
		require.NoError(t, err)
		assert.Equal(t, 3, third.Number)
		assert.Equal(t, entity.ScannerStatusUnused, third.Status)
		assert.NotEmpty(t, third.LinkToken)

		other := seedScanner(t, seedAdmissionEvent(t))
		assert.Equal(t, 1, other.Number, "numbers are per event")
	})

	t.Run("two scanners created at once", func(t *testing.T) {
		// @spec components/entity/scanner/create "Two scanners created at once"
		cleanDatabase(t)
		eventID := seedAdmissionEvent(t)

		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			numbers []int
		)
		start := make(chan struct{})
		for range 2 {
			wg.Go(func() {
				<-start
				scanner, err := repo.Create(ctx, entity.NewScanner(eventID))
				assert.NoError(t, err)
				if scanner != nil {
					mu.Lock()
					numbers = append(numbers, scanner.Number)
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
		_, err := repo.Create(ctx, entity.NewScanner(entity.NewID()))
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("get", func(t *testing.T) {
		cleanDatabase(t)
		scanner := seedScanner(t, seedAdmissionEvent(t))
		_, err := repo.Revoke(ctx, scanner.ID, nov20(12, 0))
		require.NoError(t, err)

		// @spec components/entity/scanner/get "Existing scanner"
		got, err := repo.Get(ctx, scanner.ID)
		require.NoError(t, err)
		assert.Equal(t, entity.ScannerStatusRevoked, got.Status)

		// @spec components/entity/scanner/get "Unknown scanner"
		_, err = repo.Get(ctx, entity.ScannerID(entity.NewID()))
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("get by link token", func(t *testing.T) {
		cleanDatabase(t)
		scanner := seedScanner(t, seedAdmissionEvent(t))

		// @spec components/entity/scanner/get-by-link-token "Known link token"
		got, err := repo.GetByLinkToken(ctx, scanner.LinkToken)
		require.NoError(t, err)
		assert.Equal(t, scanner.ID, got.ID)

		// @spec components/entity/scanner/get-by-link-token "Unknown link token"
		_, err = repo.GetByLinkToken(ctx, entity.NewScanner("x").LinkToken)
		assert.ErrorIs(t, err, apperr.ErrNotFound)

		// The link token keeps finding the scanner after it is bound, but is no longer
		// returned.
		key := testutil.NewDeviceKey(t).PublicKey(t)
		_, _, err = repo.BindDevice(ctx, scanner.ID, key, nov20(14, 10))
		require.NoError(t, err)
		bound, err := repo.GetByLinkToken(ctx, scanner.LinkToken)
		require.NoError(t, err)
		assert.Equal(t, scanner.ID, bound.ID)
		assert.Empty(t, bound.LinkToken)
	})

	t.Run("list by event", func(t *testing.T) {
		cleanDatabase(t)
		eventID := seedAdmissionEvent(t)
		l1 := seedScanner(t, eventID)
		l2 := seedScanner(t, eventID)
		_, _, err := repo.BindDevice(ctx, l1.ID, testutil.NewDeviceKey(t).PublicKey(t), nov20(14, 10))
		require.NoError(t, err)
		_, err = repo.Revoke(ctx, l2.ID, nov20(15, 0))
		require.NoError(t, err)

		// @spec components/entity/scanner/list-by-event "Event with scanners"
		got, err := repo.ListByEvent(ctx, eventID)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, 1, got[0].Number)
		assert.Equal(t, entity.ScannerStatusInUse, got[0].Status)
		assert.Equal(t, 2, got[1].Number)
		assert.Equal(t, entity.ScannerStatusRevoked, got[1].Status)

		// @spec components/entity/scanner/list-by-event "Event without scanners"
		empty, err := repo.ListByEvent(ctx, seedAdmissionEvent(t))
		require.NoError(t, err)
		assert.NotNil(t, empty)
		assert.Empty(t, empty)
	})

	t.Run("bind device", func(t *testing.T) {
		cleanDatabase(t)
		eventID := seedAdmissionEvent(t)
		p1 := testutil.NewDeviceKey(t).PublicKey(t)
		p2 := testutil.NewDeviceKey(t).PublicKey(t)
		scanner := seedScanner(t, eventID)

		// @spec components/entity/scanner/bind-device "First device"
		outcome, got, err := repo.BindDevice(ctx, scanner.ID, p1, nov20(14, 10))
		require.NoError(t, err)
		assert.Equal(t, entity.BindOutcomeBound, outcome)
		assert.Equal(t, entity.ScannerStatusInUse, got.Status)
		assert.Equal(t, p1, got.BoundPublicKey)
		require.NotNil(t, got.BoundTime)
		assert.True(t, got.BoundTime.Equal(nov20(14, 10)))
		assert.Empty(t, got.LinkToken, "the token is not kept once bound")

		// @spec components/entity/scanner/bind-device "Same device again"
		outcome, got, err = repo.BindDevice(ctx, scanner.ID, p1, nov20(15, 0))
		require.NoError(t, err)
		assert.Equal(t, entity.BindOutcomeBound, outcome)
		assert.True(t, got.BoundTime.Equal(nov20(14, 10)), "nothing changes")

		// @spec components/entity/scanner/bind-device "Another device"
		outcome, got, err = repo.BindDevice(ctx, scanner.ID, p2, nov20(15, 0))
		require.NoError(t, err)
		assert.Equal(t, entity.BindOutcomeOtherDevice, outcome)
		assert.Equal(t, p1, got.BoundPublicKey)

		// @spec components/entity/scanner/bind-device "Revoked scanner"
		revoked := seedScanner(t, eventID)
		_, err = repo.Revoke(ctx, revoked.ID, nov20(15, 0))
		require.NoError(t, err)
		outcome, got, err = repo.BindDevice(ctx, revoked.ID, p1, nov20(15, 5))
		require.NoError(t, err)
		assert.Equal(t, entity.BindOutcomeRevoked, outcome)
		assert.Nil(t, got.BoundPublicKey)

		_, _, err = repo.BindDevice(ctx, scanner.ID, entity.PublicKey{0x04, 0x01}, nov20(15, 0))
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		_, _, err = repo.BindDevice(ctx, entity.ScannerID(entity.NewID()), p1, nov20(15, 0))
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("two devices at once", func(t *testing.T) {
		// @spec components/entity/scanner/bind-device "Two devices at once"
		cleanDatabase(t)
		scanner := seedScanner(t, seedAdmissionEvent(t))

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
				outcome, _, err := repo.BindDevice(ctx, scanner.ID, key, nov20(14, 10))
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
		scanner := seedScanner(t, seedAdmissionEvent(t))
		_, _, err := repo.BindDevice(ctx, scanner.ID, testutil.NewDeviceKey(t).PublicKey(t), nov20(14, 10))
		require.NoError(t, err)

		// @spec components/entity/scanner/revoke "Scanner in use"
		got, err := repo.Revoke(ctx, scanner.ID, nov20(18, 5))
		require.NoError(t, err)
		assert.Equal(t, entity.ScannerStatusRevoked, got.Status)
		require.NotNil(t, got.RevokedTime)
		assert.True(t, got.RevokedTime.Equal(nov20(18, 5)))

		// @spec components/entity/scanner/revoke "Already revoked"
		got, err = repo.Revoke(ctx, scanner.ID, nov20(18, 10))
		require.NoError(t, err)
		assert.True(t, got.RevokedTime.Equal(nov20(18, 5)), "the revoked time stays 18:05")

		// @spec components/entity/scanner/revoke "Unknown scanner"
		_, err = repo.Revoke(ctx, entity.ScannerID(entity.NewID()), nov20(18, 10))
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestRejectedScanRepository_Append(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewRejectedScanRepository(testDB)

	count := func(t *testing.T, scannerID entity.ScannerID) int {
		t.Helper()
		var n int
		require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT count(*) FROM rejected_scans WHERE scanner_id = $1`, string(scannerID)).Scan(&n))
		return n
	}

	t.Run("group rejected", func(t *testing.T) {
		// @spec components/entity/rejected-scan/append "Group rejected"
		cleanDatabase(t)
		eventID := seedAdmissionEvent(t)
		scanner := seedScanner(t, eventID)
		scans := []*entity.RejectedScan{
			entity.NewRejectedScan(eventID, scanner.ID, entity.TicketID(entity.NewID()), entity.RejectedScanReasonExpired, nov20(18, 40)),
			entity.NewRejectedScan(eventID, scanner.ID, entity.TicketID(entity.NewID()), entity.RejectedScanReasonExpired, nov20(18, 40)),
			entity.NewRejectedScan(eventID, scanner.ID, entity.TicketID(entity.NewID()), entity.RejectedScanReasonExpired, nov20(18, 40)),
		}
		require.NoError(t, repo.Append(ctx, scans))
		assert.Equal(t, 3, count(t, scanner.ID))
	})

	t.Run("forged scan without a ticket", func(t *testing.T) {
		cleanDatabase(t)
		eventID := seedAdmissionEvent(t)
		scanner := seedScanner(t, eventID)
		require.NoError(t, repo.Append(ctx, []*entity.RejectedScan{
			entity.NewRejectedScan(eventID, scanner.ID, "", entity.RejectedScanReasonForged, nov20(18, 40)),
		}))
		assert.Equal(t, 1, count(t, scanner.ID))
	})

	t.Run("invalid one in the batch", func(t *testing.T) {
		// @spec components/entity/rejected-scan/append "Invalid one in the batch"
		cleanDatabase(t)
		eventID := seedAdmissionEvent(t)
		scanner := seedScanner(t, eventID)
		scans := []*entity.RejectedScan{
			entity.NewRejectedScan(eventID, scanner.ID, entity.TicketID(entity.NewID()), entity.RejectedScanReasonVoided, nov20(18, 40)),
			entity.NewRejectedScan(eventID, scanner.ID, entity.TicketID(entity.NewID()), entity.RejectedScanReasonVoided, nov20(18, 40)),
			entity.NewRejectedScan(eventID, scanner.ID, entity.TicketID(entity.NewID()), entity.RejectedScanReasonForged, nov20(18, 40)),
		}
		err := repo.Append(ctx, scans)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Equal(t, 0, count(t, scanner.ID), "none is stored")
	})

	t.Run("a database failure stores none", func(t *testing.T) {
		cleanDatabase(t)
		eventID := seedAdmissionEvent(t)
		scanner := seedScanner(t, eventID)
		scans := []*entity.RejectedScan{
			entity.NewRejectedScan(eventID, scanner.ID, entity.TicketID(entity.NewID()), entity.RejectedScanReasonVoided, nov20(18, 40)),
			// Unknown scanner: the foreign key fails inside the transaction.
			entity.NewRejectedScan(eventID, entity.ScannerID(entity.NewID()), entity.TicketID(entity.NewID()), entity.RejectedScanReasonVoided, nov20(18, 40)),
		}
		require.Error(t, repo.Append(ctx, scans))
		assert.Equal(t, 0, count(t, scanner.ID))
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
		eventID := seedAdmissionEvent(t)
		fan := seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
		ticket := seedIssuedTickets(t, eventID, fan, 1)[0]
		scanner := seedScanner(t, eventID)
		_, err := tickets.Admit(ctx, ticket, scanner.ID, nov20(18, 32))
		require.NoError(t, err)

		got, err := repo.GetByTicket(ctx, ticket)
		require.NoError(t, err)
		assert.True(t, got.AdmitTime.Equal(nov20(18, 32)))
		assert.Equal(t, 1, got.ScannerNumber)
	})

	t.Run("ticket not admitted", func(t *testing.T) {
		// @spec components/entity/admission/get-by-ticket "Ticket not admitted"
		cleanDatabase(t)
		eventID := seedAdmissionEvent(t)
		fan := seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String())
		ticket := seedIssuedTickets(t, eventID, fan, 1)[0]
		scanner := seedScanner(t, eventID)
		require.NoError(t, rdb.NewRejectedScanRepository(testDB).Append(ctx, []*entity.RejectedScan{
			entity.NewRejectedScan(eventID, scanner.ID, ticket, entity.RejectedScanReasonVoided, nov20(18, 32)),
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
		eventID := seedAdmissionEvent(t)
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
		eventID := seedAdmissionEvent(t)
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

	t.Run("cancelled event", func(t *testing.T) {
		// @spec components/entity/event/get "Cancelled event"
		cleanDatabase(t)
		o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)
		_, err := testDB.Pool.Exec(ctx, `UPDATE events SET publish_state = 'CANCELLED' WHERE id = $1`, o.eventID)
		require.NoError(t, err)

		got, err := repo.Get(ctx, o.eventID)
		require.NoError(t, err)
		assert.True(t, got.HasPublishState(entity.PublishStateCancelled))
	})

	t.Run("discovered event", func(t *testing.T) {
		// @spec components/entity/event/get "Discovered event"
		cleanDatabase(t)
		got, err := repo.Get(ctx, seedAdmissionEvent(t))
		require.NoError(t, err)
		assert.Nil(t, got.PublishState)
	})

	t.Run("unknown event", func(t *testing.T) {
		// @spec components/entity/event/get "Unknown event"
		cleanDatabase(t)
		_, err := repo.Get(ctx, entity.NewID())
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
