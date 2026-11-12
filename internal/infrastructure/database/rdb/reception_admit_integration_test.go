package rdb_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/testutil"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

// receptionTestRole is a login role named like the reception-api Cloud SQL
// IAM user, so the reception grant migration applies to it. Roles belong to
// the whole server: the project part is new on every run, so a role another
// run left behind never blocks this one.
var receptionTestRole = "reception-api@admit-test-" + uuid.NewV7().String()[24:] + ".iam"

const (
	integrationAdmitProcedure = "/liverty_music.rpc.scanner.v1.ScannerService/Admit"

	// receptionGrantMigration grants the reception-api role its reads and
	// writes.
	receptionGrantMigration = "../../../../k8s/atlas/base/migrations/20261009120000_grant_reception_api_and_restrict_organizer_console_api.sql"

	// outboxGrantMigration grants the reception-api role INSERT on the
	// outbox, where Ticket.Admit records its announcement.
	outboxGrantMigration = "../../../../k8s/atlas/base/migrations/20261030130100_grant_events_outbox_inserts.sql"
)

// connectAsReceptionRole returns a connection to the test database as a role
// holding only what the reception grant migrations give reception-api, so a
// reception path that needs a missing grant fails here rather than at the
// door. Seeding and assertions keep using db.
func connectAsReceptionRole(t *testing.T, db *rdb.Database) *rdb.Database {
	t.Helper()
	ctx := context.Background()
	role := pgx.Identifier{receptionTestRole}.Sanitize()
	// Held until the role is dropped (cleanups run last first): a migration
	// applied elsewhere meanwhile would grant to the role, and it could then
	// not be dropped.
	testutil.LockMigrations(t)

	var exists bool
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, receptionTestRole).Scan(&exists))
	if !exists {
		_, err := db.Pool.Exec(ctx, "CREATE ROLE "+role+" LOGIN")
		require.NoError(t, err)
	}
	migration, err := os.ReadFile(receptionGrantMigration)
	require.NoError(t, err)
	// Later migrations rename what this grant names:
	// 20261012000000_one_name_per_concept.sql renames tickets.admitted_at to
	// admit_at, and 20261012220000_rename_reception_links_to_scanners.sql
	// renames reception_links to scanners and its token to link_token. In a
	// migrated database the grants follow the renames; replayed here on the
	// current schema, the grant names them by their current names.
	grants := strings.NewReplacer(
		"(admitted_at) ON tickets", "(admit_at) ON tickets",
		"(status, bound_public_key, bound_at, token) ON reception_links", "(status, bound_public_key, bound_at, link_token) ON scanners",
		"reception_links", "scanners",
	).Replace(string(migration))
	_, err = db.Pool.Exec(ctx, grants)
	require.NoError(t, err)
	outboxGrant, err := os.ReadFile(outboxGrantMigration)
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, string(outboxGrant))
	require.NoError(t, err)

	logger, err := logging.New()
	require.NoError(t, err)
	roleDB, err := rdb.New(ctx, config.DatabaseConfig{
		Host:              "localhost",
		Port:              testutil.DatabasePort(),
		Name:              databaseOf(t, db).Name,
		User:              receptionTestRole,
		SSLMode:           "disable",
		Schema:            "app",
		MaxOpenConns:      4,
		MaxIdleConns:      1,
		ConnMaxLifetime:   1800,
		MaxConnIdleTime:   600,
		HealthCheckPeriod: 60,
	}, true, logger)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, roleDB.Close())
		ctx := context.Background()
		_, err := db.Pool.Exec(ctx, "DROP OWNED BY "+role)
		assert.NoError(t, err)
		_, err = db.Pool.Exec(ctx, "DROP ROLE "+role)
		assert.NoError(t, err)
	})
	return roleDB
}

// TestTicketUseCase_Admit_Integration runs TicketUseCase.Admit against the
// real store, connected as the reception-api role: the same fan code scanned
// at two entrances at once, and a ticket presented again later.
func TestTicketUseCase_Admit_Integration(t *testing.T) {
	// Not parallel: the role connectAsReceptionRole creates belongs to the
	// whole server and has one name in this run, which
	// TestScannerUseCase_Open_Integration creates and drops too.
	db := newTestDB(t)
	ctx := context.Background()
	logger, err := logging.New()
	require.NoError(t, err)

	receptionDB := connectAsReceptionRole(t, db)
	uc := usecase.NewTicketUseCase(
		orderRepository(t, rdb.NewOrderRepository(receptionDB)), rdb.NewTicketRepository(receptionDB, testOutbox), rdb.NewScannerRepository(receptionDB),
		rdb.NewEventRepository(receptionDB), rdb.NewWalletPublicKeyRepository(receptionDB), rdb.NewAdmissionRepository(receptionDB),
		rdb.NewRejectedScanRepository(receptionDB), logger,
	)
	// Seeding runs as the test superuser.
	scannerRepo := rdb.NewScannerRepository(db)
	keyRepo := rdb.NewWalletPublicKeyRepository(db)

	// setup seeds an event opening 18:00 / starting 19:00 on 2026-11-20, a
	// fan holding n tickets with a registered wallet key, and two scanners
	// (受付1, 受付2) bound to two staff devices.
	type stage struct {
		eventID  string
		fanID    entity.UserID
		fan      *testutil.DeviceKey
		tickets  []entity.TicketID
		scanners [2]*entity.Scanner
		staff    [2]*testutil.DeviceKey
	}
	setup := func(t *testing.T, n int) *stage {
		t.Helper()
		cleanTables(t, db)
		s := &stage{eventID: seedAdmissionEvent(t, db), fan: testutil.NewDeviceKey(t)}
		_, err := db.Pool.Exec(ctx, `UPDATE events SET open_at = $2, start_at = $3 WHERE id = $1`, s.eventID, nov20(18, 0), nov20(19, 0))
		require.NoError(t, err)
		s.fanID = entity.UserID(seedUser(t, db, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String()))
		s.tickets = seedIssuedTickets(t, db, s.eventID, string(s.fanID), n)
		_, _, err = keyRepo.Register(ctx, s.fanID, s.fan.PublicKey(t), nov20(9, 0))
		require.NoError(t, err)
		for i := range 2 {
			s.staff[i] = testutil.NewDeviceKey(t)
			scanner := seedScanner(t, db, s.eventID)
			_, bound, err := scannerRepo.BindDevice(ctx, scanner.ID, s.staff[i].PublicKey(t), nov20(14, 0))
			require.NoError(t, err)
			bound.LinkToken = scanner.LinkToken
			s.scanners[i] = bound
		}
		return s
	}
	admit := func(t *testing.T, s *stage, i int, scanned string, now time.Time) (*usecase.AdmitResult, error) {
		t.Helper()
		call := s.staff[i].SignCall(t, integrationAdmitProcedure, s.scanners[i].LinkToken, scanned, now)
		return uc.Admit(ctx, usecase.AdmitInput{
			Procedure: integrationAdmitProcedure, LinkToken: s.scanners[i].LinkToken,
			SignTime: call.SignTime, Signature: call.Signature, ScannedText: scanned, Now: now,
		})
	}

	t.Run("same code at two entrances", func(t *testing.T) {
		// @spec components/usecase/ticket/admit "Same code at two entrances"
		s := setup(t, 2)
		now := nov20(18, 32)
		code := s.fan.SignCode(t, &entity.AdmissionCode{UserID: s.fanID, EventID: s.eventID, TicketIDs: s.tickets, SignedTime: now})

		var (
			wg      sync.WaitGroup
			results [2]*usecase.AdmitResult
		)
		start := make(chan struct{})
		for i := range 2 {
			wg.Go(func() {
				<-start
				got, err := admit(t, s, i, code, now)
				assert.NoError(t, err)
				results[i] = got
			})
		}
		close(start)
		wg.Wait()
		require.NotNil(t, results[0])
		require.NotNil(t, results[1])

		assert.Equal(t, 2, results[0].AdmittedTicketCount+results[1].AdmittedTicketCount, "each ticket admitted through exactly one scanner")
		already := 0
		for _, r := range results {
			for _, rt := range r.RejectedTickets {
				assert.Equal(t, entity.RejectedScanReasonAlreadyAdmitted, rt.Reason)
				already++
			}
		}
		assert.Equal(t, 2, already, "and rejected as AlreadyAdmitted through the other")

		var admissions, rejections int
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM admissions WHERE event_id = $1`, s.eventID).Scan(&admissions))
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM rejected_scans WHERE event_id = $1 AND reason = 6`, s.eventID).Scan(&rejections))
		assert.Equal(t, 2, admissions)
		assert.Equal(t, 2, rejections)
	})

	t.Run("presented again later", func(t *testing.T) {
		s := setup(t, 1)
		first := nov20(18, 32)
		got, err := admit(t, s, 0, s.fan.SignCode(t, &entity.AdmissionCode{UserID: s.fanID, EventID: s.eventID, TicketIDs: s.tickets, SignedTime: first}), first)
		require.NoError(t, err)
		assert.Equal(t, 1, got.AdmittedTicketCount)

		later := nov20(18, 40)
		got, err = admit(t, s, 1, s.fan.SignCode(t, &entity.AdmissionCode{UserID: s.fanID, EventID: s.eventID, TicketIDs: s.tickets, SignedTime: later}), later)
		require.NoError(t, err)
		require.Len(t, got.RejectedTickets, 1)
		rt := got.RejectedTickets[0]
		assert.Equal(t, entity.RejectedScanReasonAlreadyAdmitted, rt.Reason)
		assert.True(t, rt.EarlierAdmitTime.Equal(first))
		assert.Equal(t, 1, rt.EarlierScannerNumber)

		var reason int16
		var scannerID string
		require.NoError(t, db.Pool.QueryRow(ctx,
			`SELECT reason, scanner_id FROM rejected_scans WHERE ticket_id = $1`, string(s.tickets[0])).Scan(&reason, &scannerID))
		assert.Equal(t, int16(entity.RejectedScanReasonAlreadyAdmitted), reason)
		assert.Equal(t, string(s.scanners[1].ID), scannerID)
	})
}

// TestScannerUseCase_Open_Integration opens one Unused scanner from two
// devices at once against the real store, connected as the reception-api
// role: exactly one is bound, and the other
// is told the scanner is in use on another device (FailedPrecondition), never
// PermissionDenied.
func TestScannerUseCase_Open_Integration(t *testing.T) {
	// Not parallel: see TestTicketUseCase_Admit_Integration.
	db := newTestDB(t)
	ctx := context.Background()
	logger, err := logging.New()
	require.NoError(t, err)
	const openProcedure = "/liverty_music.rpc.scanner.v1.ScannerService/Open"

	eventID := seedAdmissionEvent(t, db)
	_, err = db.Pool.Exec(ctx, `UPDATE events SET start_at = $2 WHERE id = $1`, eventID, nov20(19, 0))
	require.NoError(t, err)
	scanner := seedScanner(t, db, eventID)
	receptionDB := connectAsReceptionRole(t, db)
	uc := usecase.NewScannerUseCase(rdb.NewScannerRepository(receptionDB), rdb.NewEventRepository(receptionDB),
		rdb.NewEventOrganizerRepository(receptionDB), logger)

	now := nov20(14, 10)
	var (
		wg   sync.WaitGroup
		errs [2]error
	)
	start := make(chan struct{})
	for i := range 2 {
		device := testutil.NewDeviceKey(t)
		key := device.PublicKey(t)
		call := device.SignCall(t, openProcedure, scanner.LinkToken, key.Base64URL(), now)
		wg.Go(func() {
			<-start
			_, errs[i] = uc.Open(ctx, usecase.OpenScannerInput{
				Procedure: openProcedure, LinkToken: scanner.LinkToken, PublicKey: key,
				SignTime: call.SignTime, Signature: call.Signature, Now: now,
			})
		})
	}
	close(start)
	wg.Wait()

	succeeded, otherDevice := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, apperr.ErrFailedPrecondition):
			otherDevice++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	assert.Equal(t, 1, succeeded)
	assert.Equal(t, 1, otherDevice)
}
