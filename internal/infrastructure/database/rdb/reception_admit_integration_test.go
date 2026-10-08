package rdb_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/testutil"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

const integrationAdmitProcedure = "/liverty_music.rpc.organizer.reception.v1.ReceptionService/Admit"

// TestTicketUseCase_Admit_Integration runs TicketUseCase.Admit against the
// real store: the same fan code scanned at two entrances at once, and a
// ticket presented again later.
func TestTicketUseCase_Admit_Integration(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	logger, err := logging.New()
	require.NoError(t, err)

	ticketRepo := rdb.NewTicketRepository(testDB)
	linkRepo := rdb.NewReceptionLinkRepository(testDB)
	keyRepo := rdb.NewWalletPublicKeyRepository(testDB)
	uc := usecase.NewTicketUseCase(
		rdb.NewOrderRepository(testDB), ticketRepo, linkRepo, rdb.NewEventRepository(testDB),
		keyRepo, rdb.NewAdmissionRepository(testDB), rdb.NewRejectedScanRepository(testDB), logger,
	)

	// setup seeds an event opening 18:00 / starting 19:00 on 2026-11-20, a
	// fan holding n tickets with a registered wallet key, and two links
	// (受付1, 受付2) bound to two staff devices.
	type stage struct {
		eventID string
		fanID   entity.UserID
		fan     *testutil.DeviceKey
		tickets []entity.TicketID
		links   [2]*entity.ReceptionLink
		staff   [2]*testutil.DeviceKey
	}
	setup := func(t *testing.T, n int) *stage {
		t.Helper()
		cleanDatabase(t)
		s := &stage{eventID: seedReceptionEvent(t), fan: testutil.NewDeviceKey(t)}
		_, err := testDB.Pool.Exec(ctx, `UPDATE events SET open_at = $2, start_at = $3 WHERE id = $1`, s.eventID, nov20(18, 0), nov20(19, 0))
		require.NoError(t, err)
		s.fanID = entity.UserID(seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String()))
		s.tickets = seedHeldTickets(t, s.eventID, string(s.fanID), n)
		_, _, err = keyRepo.Register(ctx, s.fanID, s.fan.PublicKey(t), nov20(9, 0))
		require.NoError(t, err)
		for i := range 2 {
			s.staff[i] = testutil.NewDeviceKey(t)
			link := seedLink(t, s.eventID)
			_, bound, err := linkRepo.BindDevice(ctx, link.ID, s.staff[i].PublicKey(t), nov20(14, 0))
			require.NoError(t, err)
			bound.Token = link.Token
			s.links[i] = bound
		}
		return s
	}
	admit := func(t *testing.T, s *stage, i int, scanned string, now time.Time) (*usecase.AdmitResult, error) {
		t.Helper()
		call := s.staff[i].SignCall(t, integrationAdmitProcedure, s.links[i].Token, scanned, now)
		return uc.Admit(ctx, usecase.AdmitInput{
			Procedure: integrationAdmitProcedure, LinkToken: s.links[i].Token,
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

		assert.Equal(t, 2, results[0].AdmittedTicketCount+results[1].AdmittedTicketCount, "each ticket admitted through exactly one link")
		already := 0
		for _, r := range results {
			for _, rt := range r.RejectedTickets {
				assert.Equal(t, entity.RejectedScanReasonAlreadyAdmitted, rt.Reason)
				already++
			}
		}
		assert.Equal(t, 2, already, "and rejected as AlreadyAdmitted through the other")

		var admissions, rejections int
		require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT count(*) FROM admissions WHERE event_id = $1`, s.eventID).Scan(&admissions))
		require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT count(*) FROM rejected_scans WHERE event_id = $1 AND reason = 6`, s.eventID).Scan(&rejections))
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
		assert.True(t, rt.EarlierAdmittedTime.Equal(first))
		assert.Equal(t, 1, rt.EarlierReceptionLinkNumber)

		var reason int16
		var linkID string
		require.NoError(t, testDB.Pool.QueryRow(ctx,
			`SELECT reason, reception_link_id FROM rejected_scans WHERE ticket_id = $1`, string(s.tickets[0])).Scan(&reason, &linkID))
		assert.Equal(t, int16(entity.RejectedScanReasonAlreadyAdmitted), reason)
		assert.Equal(t, string(s.links[1].ID), linkID)
	})
}
