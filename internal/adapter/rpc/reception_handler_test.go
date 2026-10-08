package rpc_test

import (
	"context"
	"testing"
	"time"

	receptionv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/organizer/reception/v1/receptionv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	receptionv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/reception/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/testutil"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const testLinkToken = "tok_0123456789abcdefghijklmn"

type receptionHandlerFixture struct {
	h       *handler.ReceptionHandler
	links   *ucmocks.MockReceptionLinkUseCase
	tickets *ucmocks.MockTicketUseCase
}

func newReceptionHandler(t *testing.T) *receptionHandlerFixture {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	f := &receptionHandlerFixture{
		links:   ucmocks.NewMockReceptionLinkUseCase(t),
		tickets: ucmocks.NewMockTicketUseCase(t),
	}
	f.h = handler.NewReceptionHandler(f.links, f.tickets, logger)
	return f
}

// openRequest is a well-formed Open from client IP ip.
func openRequest(t *testing.T, token, ip string) *connect.Request[receptionv1.OpenRequest] {
	t.Helper()
	req := connect.NewRequest(&receptionv1.OpenRequest{
		LinkToken: &entityv1.ReceptionLinkToken{Value: token},
		SignTime:  timestamppb.Now(),
		Signature: &entityv1.Signature{Value: make([]byte, entity.SignatureLen)},
		PublicKey: &entityv1.PublicKey{Value: testutil.NewDeviceKey(t).PublicKey(t)},
	})
	req.Header().Set("X-Forwarded-For", ip)
	return req
}

// admitRequest is a well-formed Admit from client IP ip.
func admitRequest(token, ip string) *connect.Request[receptionv1.AdmitRequest] {
	req := connect.NewRequest(&receptionv1.AdmitRequest{
		LinkToken:   &entityv1.ReceptionLinkToken{Value: token},
		SignTime:    timestamppb.Now(),
		Signature:   &entityv1.Signature{Value: make([]byte, entity.SignatureLen)},
		ScannedText: &entityv1.ScannedText{Value: "6BFOXN*TS0BI$ZD"},
	})
	req.Header().Set("X-Forwarded-For", ip)
	return req
}

func TestReceptionHandler_Open(t *testing.T) {
	t.Parallel()

	t.Run("passes the call to ReceptionLinkUseCase.Open", func(t *testing.T) {
		t.Parallel()
		f := newReceptionHandler(t)
		req := openRequest(t, testLinkToken, "203.0.113.1")
		start := time.Date(2026, 11, 20, 6, 0, 0, 0, time.UTC)
		f.links.EXPECT().Open(mock.Anything, mock.MatchedBy(func(in usecase.OpenReceptionLinkInput) bool {
			return in.Procedure == receptionv1connect.ReceptionServiceOpenProcedure &&
				in.LinkToken == testLinkToken &&
				string(in.PublicKey) == string(req.Msg.PublicKey.Value) &&
				!in.Now.IsZero()
		})).Return(&usecase.OpenReceptionLinkResult{
			Link:         &entity.ReceptionLink{ID: testLinkID, EventID: testEventID, Number: 2, Status: entity.ReceptionLinkStatusInUse},
			Window:       &entity.ReceptionWindow{OpenTime: start, CloseTime: start.Add(13 * time.Hour)},
			InsideWindow: true,
		}, nil)

		resp, err := f.h.Open(context.Background(), req)
		require.NoError(t, err)
		assert.Equal(t, int32(2), resp.Msg.ReceptionLink.Number.Value)
		assert.Nil(t, resp.Msg.ReceptionLink.Token)
		assert.True(t, resp.Msg.ReceptionWindow.OpenTime.AsTime().Equal(start))
		assert.True(t, resp.Msg.InsideWindow)
	})

	t.Run("missing public key", func(t *testing.T) {
		t.Parallel()
		f := newReceptionHandler(t)
		req := openRequest(t, testLinkToken, "203.0.113.1")
		req.Msg.PublicKey = nil
		_, err := f.h.Open(context.Background(), req)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}

func TestReceptionHandler_Admit(t *testing.T) {
	t.Parallel()

	t.Run("missing signature", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/reception "Missing signature"
		f := newReceptionHandler(t)
		req := admitRequest(testLinkToken, "203.0.113.1")
		req.Msg.Signature = nil
		_, err := f.h.Admit(context.Background(), req)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		f.tickets.AssertNotCalled(t, "Admit", mock.Anything, mock.Anything)
	})

	t.Run("missing token, sign time or scanned text", func(t *testing.T) {
		t.Parallel()
		f := newReceptionHandler(t)
		for _, strip := range []func(*receptionv1.AdmitRequest){
			func(m *receptionv1.AdmitRequest) { m.LinkToken = nil },
			func(m *receptionv1.AdmitRequest) { m.SignTime = nil },
			func(m *receptionv1.AdmitRequest) { m.ScannedText = nil },
		} {
			req := admitRequest(testLinkToken, "203.0.113.1")
			strip(req.Msg)
			_, err := f.h.Admit(context.Background(), req)
			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		}
	})

	t.Run("result carries a head count and reasons, no personal data", func(t *testing.T) {
		t.Parallel()
		f := newReceptionHandler(t)
		earlier := time.Date(2026, 11, 20, 9, 32, 0, 0, time.UTC)
		f.tickets.EXPECT().Admit(mock.Anything, mock.MatchedBy(func(in usecase.AdmitInput) bool {
			return in.Procedure == receptionv1connect.ReceptionServiceAdmitProcedure &&
				in.LinkToken == testLinkToken && in.ScannedText == "6BFOXN*TS0BI$ZD"
		})).Return(&usecase.AdmitResult{
			AdmittedTicketCount: 2,
			RejectedTickets: []usecase.RejectedTicket{
				{Reason: entity.RejectedScanReasonAlreadyAdmitted, EarlierAdmittedTime: &earlier, EarlierReceptionLinkNumber: 1},
			},
		}, nil)

		resp, err := f.h.Admit(context.Background(), admitRequest(testLinkToken, "203.0.113.1"))
		require.NoError(t, err)
		assert.Equal(t, int32(2), resp.Msg.AdmittedTicketCount)
		require.Len(t, resp.Msg.RejectedTickets, 1)
		rt := resp.Msg.RejectedTickets[0]
		assert.Equal(t, entityv1.RejectedScanReason_REJECTED_SCAN_REASON_ALREADY_ADMITTED, rt.Reason)
		assert.True(t, rt.EarlierAdmitTime.AsTime().Equal(earlier))
		assert.Equal(t, int32(1), rt.EarlierReceptionLinkNumber.Value)

		// Task 6.3: the response shape itself can carry no personal data —
		// only a count, reasons, a time and a link number. Any new field
		// (such as a holder name, phone number or account) fails this test.
		assertFields(t, resp.Msg, "admitted_ticket_count", "rejected_scan_reason", "rejected_tickets")
		assertFields(t, rt, "reason", "earlier_admit_time", "earlier_reception_link_number")
		assertFields(t, rt.EarlierReceptionLinkNumber, "value")
	})

	t.Run("whole-scan rejection", func(t *testing.T) {
		t.Parallel()
		f := newReceptionHandler(t)
		f.tickets.EXPECT().Admit(mock.Anything, mock.Anything).
			Return(&usecase.AdmitResult{RejectedScanReason: entity.RejectedScanReasonExpired}, nil)

		resp, err := f.h.Admit(context.Background(), admitRequest(testLinkToken, "203.0.113.1"))
		require.NoError(t, err)
		assert.Equal(t, entityv1.RejectedScanReason_REJECTED_SCAN_REASON_EXPIRED, resp.Msg.RejectedScanReason)
		assert.Zero(t, resp.Msg.AdmittedTicketCount)
		assert.Empty(t, resp.Msg.RejectedTickets)
	})
}

// assertFields asserts that msg's message type declares exactly the given
// fields.
func assertFields(t *testing.T, msg proto.Message, want ...string) {
	t.Helper()
	fields := msg.ProtoReflect().Descriptor().Fields()
	got := make([]string, 0, fields.Len())
	for i := range fields.Len() {
		got = append(got, string(fields.Get(i).Name()))
	}
	assert.ElementsMatch(t, want, got, "fields of %s", msg.ProtoReflect().Descriptor().FullName())
}
