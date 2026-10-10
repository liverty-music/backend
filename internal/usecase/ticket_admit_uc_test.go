package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/testutil"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const admitProcedure = "/liverty_music.rpc.organizer.reception.v1.ReceptionService/Admit"

// evening returns 2026-11-20 hh:mm:ss in Japan time; the test event opens at
// 18:00 and starts at 19:00, so its reception window is 15:00 to 04:00 the
// next day.
func evening(hh, mm, ss int) time.Time {
	return time.Date(2026, 11, 20, hh, mm, ss, 0, jst)
}

// admitFixture is one reception link (受付1, InUse on the staff device) for
// one event, a fan with a registered wallet key, and the mocks behind
// TicketUseCase.Admit.
type admitFixture struct {
	t        *testing.T
	uc       usecase.TicketUseCase
	tickets  *entitymocks.MockTicketRepository
	links    *entitymocks.MockReceptionLinkRepository
	events   *entitymocks.MockEventRepository
	keys     *entitymocks.MockWalletPublicKeyRepository
	admits   *entitymocks.MockAdmissionRepository
	rejects  *entitymocks.MockRejectedScanRepository
	staff    *testutil.DeviceKey
	fan      *testutil.DeviceKey
	link     *entity.ReceptionLink
	event    *entity.Event
	fanID    entity.UserID
	appended []*entity.RejectedScan
}

func newAdmitFixture(t *testing.T) *admitFixture {
	t.Helper()
	f := &admitFixture{
		t:       t,
		tickets: entitymocks.NewMockTicketRepository(t),
		links:   entitymocks.NewMockReceptionLinkRepository(t),
		events:  entitymocks.NewMockEventRepository(t),
		keys:    entitymocks.NewMockWalletPublicKeyRepository(t),
		admits:  entitymocks.NewMockAdmissionRepository(t),
		rejects: entitymocks.NewMockRejectedScanRepository(t),
		staff:   testutil.NewDeviceKey(t),
		fan:     testutil.NewDeviceKey(t),
		fanID:   entity.UserID(entity.NewID()),
	}
	open, start := evening(18, 0, 0), evening(19, 0, 0)
	f.event = &entity.Event{
		ID:        entity.NewID(),
		LocalDate: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC),
		OpenTime:  &open,
		StartTime: &start,
	}
	f.link = &entity.ReceptionLink{
		ID:             entity.ReceptionLinkID(entity.NewID()),
		EventID:        f.event.ID,
		Number:         1,
		Status:         entity.ReceptionLinkStatusInUse,
		BoundPublicKey: f.staff.PublicKey(t),
	}
	f.uc = usecase.NewTicketUseCase(nil, f.tickets, f.links, f.events, f.keys, f.admits, f.rejects, newTestLogger(t))
	return f
}

// expectLinkAndEvent sets up the token lookup and the event read.
func (f *admitFixture) expectLinkAndEvent() {
	f.links.EXPECT().GetByToken(mock.Anything, "tok-1").Return(f.link, nil)
	f.events.EXPECT().Get(mock.Anything, f.event.ID).Return(f.event, nil)
}

// expectFanKey makes the fan's registered key the current one.
func (f *admitFixture) expectFanKey() {
	f.keys.EXPECT().GetByUser(mock.Anything, f.fanID).
		Return(&entity.WalletPublicKey{UserID: f.fanID, PublicKey: f.fan.PublicKey(f.t)}, nil)
}

// expectAppend captures the RejectedScans appended.
func (f *admitFixture) expectAppend() {
	f.rejects.EXPECT().Append(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, scans []*entity.RejectedScan) error {
			f.appended = append(f.appended, scans...)
			return nil
		})
}

// heldTickets returns n tickets of the event held by the fan.
func (f *admitFixture) heldTickets(n int) []*entity.Ticket {
	out := make([]*entity.Ticket, 0, n)
	for range n {
		out = append(out, &entity.Ticket{ID: entity.TicketID(entity.NewID()), HolderID: f.fanID, EventID: f.event.ID, Status: entity.TicketStatusIssued})
	}
	return out
}

// code signs, on the fan's device at signed, a code for eventID presenting ids.
func (f *admitFixture) code(eventID string, signed time.Time, ids ...entity.TicketID) string {
	f.t.Helper()
	return f.fan.SignCode(f.t, &entity.AdmissionCode{UserID: f.fanID, EventID: eventID, TicketIDs: ids, SignedTime: signed})
}

// admit sends scanned through 受付1 at now, signed by signer.
func (f *admitFixture) admit(signer *testutil.DeviceKey, scanned string, now time.Time) (*usecase.AdmitResult, error) {
	f.t.Helper()
	call := signer.SignCall(f.t, admitProcedure, "tok-1", scanned, now)
	return f.uc.Admit(context.Background(), usecase.AdmitInput{
		Procedure:   admitProcedure,
		LinkToken:   "tok-1",
		SignTime:    call.SignTime,
		Signature:   call.Signature,
		ScannedText: scanned,
		Now:         now,
	})
}

func ids(tickets []*entity.Ticket) []entity.TicketID {
	out := make([]entity.TicketID, 0, len(tickets))
	for _, t := range tickets {
		out = append(out, t.ID)
	}
	return out
}

func TestTicketUseCase_Admit_Caller(t *testing.T) {
	t.Parallel()

	t.Run("revoked link", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket/admit "Revoked link"
		f := newAdmitFixture(t)
		f.link.Status = entity.ReceptionLinkStatusRevoked
		f.links.EXPECT().GetByToken(mock.Anything, "tok-1").Return(f.link, nil)

		_, err := f.admit(f.staff, "anything", evening(18, 32, 0))
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		assert.NotErrorIs(t, err, usecase.ErrUnknownReceptionLinkToken)
		f.tickets.AssertNotCalled(t, "Admit", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("call from another device", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket/admit "Call from another device"
		f := newAdmitFixture(t)
		f.links.EXPECT().GetByToken(mock.Anything, "tok-1").Return(f.link, nil)

		_, err := f.admit(testutil.NewDeviceKey(t), "anything", evening(18, 32, 0))
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		f.tickets.AssertNotCalled(t, "Admit", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("unknown token", func(t *testing.T) {
		t.Parallel()
		f := newAdmitFixture(t)
		f.links.EXPECT().GetByToken(mock.Anything, "tok-1").Return(nil, apperr.New(codes.NotFound, "no link"))

		_, err := f.admit(f.staff, "anything", evening(18, 32, 0))
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		assert.ErrorIs(t, err, usecase.ErrUnknownReceptionLinkToken)
	})

	t.Run("unused link is never proven", func(t *testing.T) {
		t.Parallel()
		f := newAdmitFixture(t)
		f.link.Status = entity.ReceptionLinkStatusUnused
		f.link.BoundPublicKey = nil
		f.links.EXPECT().GetByToken(mock.Anything, "tok-1").Return(f.link, nil)

		_, err := f.admit(f.staff, "anything", evening(18, 32, 0))
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("after the window", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket/admit "After the window"
		f := newAdmitFixture(t)
		f.expectLinkAndEvent()
		now := time.Date(2026, 11, 21, 4, 30, 0, 0, jst)
		tickets := f.heldTickets(1)

		_, err := f.admit(f.staff, f.code(f.event.ID, now, ids(tickets)...), now)
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		f.rejects.AssertNotCalled(t, "Append", mock.Anything, mock.Anything)
		f.tickets.AssertNotCalled(t, "Admit", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("event lost its start time", func(t *testing.T) {
		t.Parallel()
		f := newAdmitFixture(t)
		f.event.StartTime = nil
		f.expectLinkAndEvent()

		_, err := f.admit(f.staff, "anything", evening(18, 32, 0))
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})
}

func TestTicketUseCase_Admit_Code(t *testing.T) {
	t.Parallel()

	now := evening(18, 32, 0)

	t.Run("screenshot shown later", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket/admit "Screenshot shown later"
		f := newAdmitFixture(t)
		f.expectLinkAndEvent()
		f.expectFanKey()
		f.expectAppend()
		tickets := f.heldTickets(2)

		got, err := f.admit(f.staff, f.code(f.event.ID, now.Add(-time.Minute), ids(tickets)...), now)
		require.NoError(t, err)
		assert.Equal(t, entity.RejectedScanReasonExpired, got.RejectedScanReason)
		assert.Zero(t, got.AdmittedTicketCount)
		assert.Empty(t, got.RejectedTickets)
		require.Len(t, f.appended, 2, "one RejectedScan per presented ticket")
		for i, s := range f.appended {
			assert.Equal(t, tickets[i].ID, s.TicketID)
			assert.Equal(t, entity.RejectedScanReasonExpired, s.Reason)
			assert.Equal(t, f.link.ID, s.ReceptionLinkID)
			assert.Equal(t, f.event.ID, s.EventID)
			assert.True(t, s.ScannedTime.Equal(now))
		}
		f.tickets.AssertNotCalled(t, "Admit", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("code from a replaced phone", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket/admit "Code from a replaced phone"
		f := newAdmitFixture(t)
		f.expectLinkAndEvent()
		f.expectAppend()
		f.keys.EXPECT().GetByUser(mock.Anything, f.fanID).
			Return(&entity.WalletPublicKey{UserID: f.fanID, PublicKey: testutil.NewDeviceKey(t).PublicKey(t)}, nil)

		got, err := f.admit(f.staff, f.code(f.event.ID, now, ids(f.heldTickets(1))...), now)
		require.NoError(t, err)
		assert.Equal(t, entity.RejectedScanReasonForged, got.RejectedScanReason)
		require.Len(t, f.appended, 1)
		assert.Empty(t, f.appended[0].TicketID, "a forged scan names no ticket")
		assert.Equal(t, entity.RejectedScanReasonForged, f.appended[0].Reason)
	})

	t.Run("not an entry code", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket/admit "Not an entry code"
		f := newAdmitFixture(t)
		f.expectLinkAndEvent()
		f.expectAppend()

		got, err := f.admit(f.staff, "https://example.com/", now)
		require.NoError(t, err)
		assert.Equal(t, entity.RejectedScanReasonForged, got.RejectedScanReason)
		require.Len(t, f.appended, 1)
		assert.Empty(t, f.appended[0].TicketID)
	})

	t.Run("user without a wallet key", func(t *testing.T) {
		t.Parallel()
		f := newAdmitFixture(t)
		f.expectLinkAndEvent()
		f.expectAppend()
		f.keys.EXPECT().GetByUser(mock.Anything, f.fanID).Return(nil, apperr.New(codes.NotFound, "no key"))

		got, err := f.admit(f.staff, f.code(f.event.ID, now, ids(f.heldTickets(1))...), now)
		require.NoError(t, err)
		assert.Equal(t, entity.RejectedScanReasonForged, got.RejectedScanReason)
	})

	t.Run("ticket for another event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket/admit "Ticket for another event"
		f := newAdmitFixture(t)
		f.expectLinkAndEvent()
		f.expectFanKey()
		f.expectAppend()
		other := []entity.TicketID{entity.TicketID(entity.NewID()), entity.TicketID(entity.NewID())}

		got, err := f.admit(f.staff, f.code(entity.NewID(), now, other...), now)
		require.NoError(t, err)
		assert.Equal(t, entity.RejectedScanReasonOtherEvent, got.RejectedScanReason)
		require.Len(t, f.appended, 2, "one RejectedScan per presented ticket")
		assert.Equal(t, other[0], f.appended[0].TicketID)
		assert.Equal(t, f.event.ID, f.appended[0].EventID, "the event of the link that scanned")
		f.tickets.AssertNotCalled(t, "ListByHolderAndEvent", mock.Anything, mock.Anything, mock.Anything)
	})

	// A genuine, fresh code for this event that also presents a ticket the
	// user does not hold for it is rejected as a whole: ListByHolderAndEvent
	// does not return that ticket, whoever holds it.
	foreignTicketTests := []struct {
		name string
		// why describes the ticket ListByHolderAndEvent does not return.
		why string
	}{
		// @spec components/usecase/ticket/admit "Someone else's ticket"
		{name: "someone else's ticket", why: "held by another account"},
		// @spec components/usecase/ticket/admit "Ticket of another event in the code"
		{name: "ticket of another event in the code", why: "the fan's own ticket for another event"},
	}
	for _, tt := range foreignTicketTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newAdmitFixture(t)
			f.expectLinkAndEvent()
			f.expectFanKey()
			f.expectAppend()
			own := f.heldTickets(2)
			foreign := entity.TicketID(entity.NewID())
			f.tickets.EXPECT().ListByHolderAndEvent(mock.Anything, f.fanID, f.event.ID).Return(own, nil)

			got, err := f.admit(f.staff, f.code(f.event.ID, now, own[0].ID, foreign, own[1].ID), now)
			require.NoError(t, err)
			assert.Equal(t, entity.RejectedScanReasonForged, got.RejectedScanReason, tt.why)
			assert.Zero(t, got.AdmittedTicketCount)
			assert.Empty(t, got.RejectedTickets)
			require.Len(t, f.appended, 1, "one RejectedScan for the whole scan")
			assert.Equal(t, entity.RejectedScanReasonForged, f.appended[0].Reason)
			assert.Empty(t, f.appended[0].TicketID, "a forged scan names no ticket")
			assert.Equal(t, f.link.ID, f.appended[0].ReceptionLinkID)
			assert.True(t, f.appended[0].ScannedTime.Equal(now))
			f.tickets.AssertNotCalled(t, "Admit", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		})
	}

	t.Run("ticket list failure fails the scan", func(t *testing.T) {
		t.Parallel()
		f := newAdmitFixture(t)
		f.expectLinkAndEvent()
		f.expectFanKey()
		tickets := f.heldTickets(1)
		f.tickets.EXPECT().ListByHolderAndEvent(mock.Anything, f.fanID, f.event.ID).
			Return(nil, apperr.New(codes.Internal, "db down"))

		_, err := f.admit(f.staff, f.code(f.event.ID, now, ids(tickets)...), now)
		assert.ErrorIs(t, err, apperr.ErrInternal)
		f.tickets.AssertNotCalled(t, "Admit", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		f.rejects.AssertNotCalled(t, "Append", mock.Anything, mock.Anything)
	})
}

func TestTicketUseCase_Admit_Tickets(t *testing.T) {
	t.Parallel()

	now := evening(18, 32, 0)

	t.Run("group of three admitted", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket/admit "Group of three admitted"
		// @spec components/usecase/ticket/admit "Admission recorded"
		f := newAdmitFixture(t)
		f.expectLinkAndEvent()
		f.expectFanKey()
		tickets := f.heldTickets(3)
		f.tickets.EXPECT().ListByHolderAndEvent(mock.Anything, f.fanID, f.event.ID).Return(tickets, nil)
		for _, tk := range tickets {
			f.tickets.EXPECT().Admit(mock.Anything, tk.ID, f.link.ID, now).
				Return(entity.AdmitResult{Outcome: entity.AdmitOutcomeAdmitted, AdmittedTime: now}, nil).Once()
		}

		got, err := f.admit(f.staff, f.code(f.event.ID, now, ids(tickets)...), now)
		require.NoError(t, err)
		assert.Equal(t, 3, got.AdmittedTicketCount)
		assert.Empty(t, got.RejectedTickets)
		assert.Equal(t, entity.RejectedScanReasonUnspecified, got.RejectedScanReason)
		f.rejects.AssertNotCalled(t, "Append", mock.Anything, mock.Anything)
	})

	t.Run("already used earlier", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket/admit "Already used earlier"
		// @spec components/usecase/ticket/admit "Rejection recorded"
		f := newAdmitFixture(t)
		f.expectLinkAndEvent()
		f.expectFanKey()
		f.expectAppend()
		later := evening(18, 40, 0)
		tickets := f.heldTickets(1)
		f.tickets.EXPECT().ListByHolderAndEvent(mock.Anything, f.fanID, f.event.ID).Return(tickets, nil)
		f.tickets.EXPECT().Admit(mock.Anything, tickets[0].ID, f.link.ID, later).
			Return(entity.AdmitResult{Outcome: entity.AdmitOutcomeAlreadyAdmitted, AdmittedTime: now}, nil)
		f.admits.EXPECT().GetByTicket(mock.Anything, tickets[0].ID).
			Return(&entity.Admission{TicketID: tickets[0].ID, ReceptionLinkID: f.link.ID, ReceptionLinkNumber: 1, AdmittedTime: now}, nil)

		got, err := f.admit(f.staff, f.code(f.event.ID, later, ids(tickets)...), later)
		require.NoError(t, err)
		assert.Zero(t, got.AdmittedTicketCount)
		require.Len(t, got.RejectedTickets, 1)
		rt := got.RejectedTickets[0]
		assert.Equal(t, entity.RejectedScanReasonAlreadyAdmitted, rt.Reason)
		require.NotNil(t, rt.EarlierAdmittedTime)
		assert.True(t, rt.EarlierAdmittedTime.Equal(now))
		assert.Equal(t, 1, rt.EarlierReceptionLinkNumber)

		require.Len(t, f.appended, 1)
		assert.Equal(t, entity.RejectedScanReasonAlreadyAdmitted, f.appended[0].Reason)
		assert.Equal(t, tickets[0].ID, f.appended[0].TicketID)
		assert.Equal(t, f.link.ID, f.appended[0].ReceptionLinkID)
		assert.True(t, f.appended[0].ScannedTime.Equal(later))
	})

	t.Run("refunded ticket", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket/admit "Refunded ticket"
		f := newAdmitFixture(t)
		f.expectLinkAndEvent()
		f.expectFanKey()
		f.expectAppend()
		tickets := f.heldTickets(1)
		tickets[0].Status = entity.TicketStatusVoided
		f.tickets.EXPECT().ListByHolderAndEvent(mock.Anything, f.fanID, f.event.ID).Return(tickets, nil)
		f.tickets.EXPECT().Admit(mock.Anything, tickets[0].ID, f.link.ID, now).
			Return(entity.AdmitResult{Outcome: entity.AdmitOutcomeVoided}, nil)

		got, err := f.admit(f.staff, f.code(f.event.ID, now, ids(tickets)...), now)
		require.NoError(t, err)
		require.Len(t, got.RejectedTickets, 1)
		assert.Equal(t, entity.RejectedScanReasonVoided, got.RejectedTickets[0].Reason)
		assert.Nil(t, got.RejectedTickets[0].EarlierAdmittedTime)
	})

	t.Run("result of a group scan", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket/admit "Result of a group scan"
		f := newAdmitFixture(t)
		f.expectLinkAndEvent()
		f.expectFanKey()
		f.expectAppend()
		later := evening(18, 40, 0)
		tickets := f.heldTickets(3)
		f.tickets.EXPECT().ListByHolderAndEvent(mock.Anything, f.fanID, f.event.ID).Return(tickets, nil)
		f.tickets.EXPECT().Admit(mock.Anything, tickets[0].ID, f.link.ID, later).
			Return(entity.AdmitResult{Outcome: entity.AdmitOutcomeAdmitted, AdmittedTime: later}, nil)
		f.tickets.EXPECT().Admit(mock.Anything, tickets[1].ID, f.link.ID, later).
			Return(entity.AdmitResult{Outcome: entity.AdmitOutcomeAlreadyAdmitted, AdmittedTime: now}, nil)
		f.tickets.EXPECT().Admit(mock.Anything, tickets[2].ID, f.link.ID, later).
			Return(entity.AdmitResult{Outcome: entity.AdmitOutcomeAdmitted, AdmittedTime: later}, nil)
		f.admits.EXPECT().GetByTicket(mock.Anything, tickets[1].ID).
			Return(&entity.Admission{TicketID: tickets[1].ID, ReceptionLinkNumber: 1, AdmittedTime: now}, nil)

		got, err := f.admit(f.staff, f.code(f.event.ID, later, ids(tickets)...), later)
		require.NoError(t, err)
		// The result is a head count and reasons only; usecase.AdmitResult and
		// RejectedTicket have no field that could carry a name, phone number
		// or account (the wire shape is asserted in the handler test).
		assert.Equal(t, &usecase.AdmitResult{
			AdmittedTicketCount: 2,
			RejectedTickets: []usecase.RejectedTicket{{
				Reason:                     entity.RejectedScanReasonAlreadyAdmitted,
				EarlierAdmittedTime:        &now,
				EarlierReceptionLinkNumber: 1,
			}},
		}, got)
	})

	t.Run("rejection append failure still returns the result", func(t *testing.T) {
		t.Parallel()
		f := newAdmitFixture(t)
		f.expectLinkAndEvent()
		f.expectFanKey()
		tickets := f.heldTickets(1)
		f.tickets.EXPECT().ListByHolderAndEvent(mock.Anything, f.fanID, f.event.ID).Return(tickets, nil)
		f.tickets.EXPECT().Admit(mock.Anything, tickets[0].ID, f.link.ID, now).
			Return(entity.AdmitResult{Outcome: entity.AdmitOutcomeVoided}, nil)
		f.rejects.EXPECT().Append(mock.Anything, mock.Anything).Return(apperr.New(codes.Internal, "db down"))

		got, err := f.admit(f.staff, f.code(f.event.ID, now, ids(tickets)...), now)
		require.NoError(t, err)
		require.Len(t, got.RejectedTickets, 1)
	})

	t.Run("earlier admission unreadable still reports the admitted time", func(t *testing.T) {
		t.Parallel()
		// AlreadyAdmitted carries the admitted time alone when
		// Admission.GetByTicket fails (Each presented ticket admitted exactly
		// once).
		f := newAdmitFixture(t)
		f.expectLinkAndEvent()
		f.expectFanKey()
		f.expectAppend()
		tickets := f.heldTickets(1)
		f.tickets.EXPECT().ListByHolderAndEvent(mock.Anything, f.fanID, f.event.ID).Return(tickets, nil)
		f.tickets.EXPECT().Admit(mock.Anything, tickets[0].ID, f.link.ID, now).
			Return(entity.AdmitResult{Outcome: entity.AdmitOutcomeAlreadyAdmitted, AdmittedTime: evening(18, 1, 0)}, nil)
		f.admits.EXPECT().GetByTicket(mock.Anything, tickets[0].ID).Return(nil, apperr.New(codes.Internal, "db down"))

		got, err := f.admit(f.staff, f.code(f.event.ID, now, ids(tickets)...), now)
		require.NoError(t, err)
		require.Len(t, got.RejectedTickets, 1)
		assert.True(t, got.RejectedTickets[0].EarlierAdmittedTime.Equal(evening(18, 1, 0)))
		assert.Zero(t, got.RejectedTickets[0].EarlierReceptionLinkNumber)
	})

	t.Run("ticket admit failure fails with Unavailable", func(t *testing.T) {
		t.Parallel()
		f := newAdmitFixture(t)
		f.expectLinkAndEvent()
		f.expectFanKey()
		tickets := f.heldTickets(2)
		f.tickets.EXPECT().ListByHolderAndEvent(mock.Anything, f.fanID, f.event.ID).Return(tickets, nil)
		f.tickets.EXPECT().Admit(mock.Anything, tickets[0].ID, f.link.ID, now).
			Return(entity.AdmitResult{Outcome: entity.AdmitOutcomeAdmitted, AdmittedTime: now}, nil)
		f.tickets.EXPECT().Admit(mock.Anything, tickets[1].ID, f.link.ID, now).
			Return(entity.AdmitResult{}, errors.New("connection reset"))

		_, err := f.admit(f.staff, f.code(f.event.ID, now, ids(tickets)...), now)
		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})

	t.Run("revoked during a group scan", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket/admit "Revoked during a group scan"
		f := newAdmitFixture(t)
		f.expectLinkAndEvent()
		f.expectFanKey()
		tickets := f.heldTickets(3)
		f.tickets.EXPECT().ListByHolderAndEvent(mock.Anything, f.fanID, f.event.ID).Return(tickets, nil)
		// The first ticket is admitted, then the link is revoked: the store
		// refuses the second (see the "link revoked before the admission"
		// contract test of Ticket.Admit).
		f.tickets.EXPECT().Admit(mock.Anything, tickets[0].ID, f.link.ID, now).
			Return(entity.AdmitResult{Outcome: entity.AdmitOutcomeAdmitted, AdmittedTime: now}, nil).Once()
		f.tickets.EXPECT().Admit(mock.Anything, tickets[1].ID, f.link.ID, now).
			Return(entity.AdmitResult{}, apperr.Wrap(entity.ErrReceptionLinkNotUsable, codes.PermissionDenied, "revoked")).Once()

		_, err := f.admit(f.staff, f.code(f.event.ID, now, ids(tickets)...), now)
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		assert.NotErrorIs(t, err, usecase.ErrUnknownReceptionLinkToken)
		f.tickets.AssertNotCalled(t, "Admit", mock.Anything, tickets[2].ID, mock.Anything, mock.Anything)

		// A later scan through a new link reports the first ticket
		// AlreadyAdmitted (the store kept its admission).
		later := evening(18, 40, 0)
		next := newAdmitFixture(t)
		next.fan, next.fanID, next.event = f.fan, f.fanID, f.event
		next.link.EventID = f.event.ID
		next.link.Number = 2
		next.expectLinkAndEvent()
		next.expectFanKey()
		next.expectAppend()
		next.tickets.EXPECT().ListByHolderAndEvent(mock.Anything, f.fanID, f.event.ID).Return(tickets[:1], nil)
		next.tickets.EXPECT().Admit(mock.Anything, tickets[0].ID, next.link.ID, later).
			Return(entity.AdmitResult{Outcome: entity.AdmitOutcomeAlreadyAdmitted, AdmittedTime: now}, nil)
		next.admits.EXPECT().GetByTicket(mock.Anything, tickets[0].ID).
			Return(&entity.Admission{TicketID: tickets[0].ID, ReceptionLinkNumber: 1, AdmittedTime: now}, nil)

		got, err := next.admit(next.staff, next.code(f.event.ID, later, tickets[0].ID), later)
		require.NoError(t, err)
		require.Len(t, got.RejectedTickets, 1)
		assert.Equal(t, entity.RejectedScanReasonAlreadyAdmitted, got.RejectedTickets[0].Reason)
		assert.Equal(t, 1, got.RejectedTickets[0].EarlierReceptionLinkNumber)
	})
}
