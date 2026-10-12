package rpc_test

import (
	"context"
	"testing"
	"time"

	scannerv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/scanner/v1/scannerv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	scannerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/scanner/v1"
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

type scannerHandlerFixture struct {
	h        *handler.ScannerHandler
	scanners *ucmocks.MockScannerUseCase
	tickets  *ucmocks.MockTicketUseCase
}

func newScannerHandler(t *testing.T) *scannerHandlerFixture {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	f := &scannerHandlerFixture{
		scanners: ucmocks.NewMockScannerUseCase(t),
		tickets:  ucmocks.NewMockTicketUseCase(t),
	}
	f.h = handler.NewScannerHandler(f.scanners, f.tickets, logger)
	return f
}

// openRequest is a well-formed Open from client IP ip.
func openRequest(t *testing.T, token, ip string) *connect.Request[scannerv1.OpenRequest] {
	t.Helper()
	req := connect.NewRequest(&scannerv1.OpenRequest{
		LinkToken: &entityv1.ScannerLinkToken{Value: token},
		SignTime:  timestamppb.Now(),
		Signature: &entityv1.Signature{Value: make([]byte, entity.SignatureLen)},
		PublicKey: &entityv1.PublicKey{Value: testutil.NewDeviceKey(t).PublicKey(t)},
	})
	req.Header().Set("X-Forwarded-For", ip)
	return req
}

// admitRequest is a well-formed Admit from client IP ip.
func admitRequest(token, ip string) *connect.Request[scannerv1.AdmitRequest] {
	req := connect.NewRequest(&scannerv1.AdmitRequest{
		LinkToken:   &entityv1.ScannerLinkToken{Value: token},
		SignTime:    timestamppb.Now(),
		Signature:   &entityv1.Signature{Value: make([]byte, entity.SignatureLen)},
		ScannedText: &entityv1.ScannedText{Value: "6BFOXN*TS0BI$ZD"},
	})
	req.Header().Set("X-Forwarded-For", ip)
	return req
}

func TestScannerHandler_Open(t *testing.T) {
	t.Parallel()

	t.Run("passes the call to ScannerUseCase.Open", func(t *testing.T) {
		t.Parallel()
		f := newScannerHandler(t)
		req := openRequest(t, testLinkToken, "203.0.113.1")
		start := time.Date(2026, 11, 20, 6, 0, 0, 0, time.UTC)
		f.scanners.EXPECT().Open(mock.Anything, mock.MatchedBy(func(in usecase.OpenScannerInput) bool {
			return in.Procedure == scannerv1connect.ScannerServiceOpenProcedure &&
				in.LinkToken == testLinkToken &&
				string(in.PublicKey) == string(req.Msg.PublicKey.Value) &&
				!in.Now.IsZero()
		})).Return(&usecase.OpenScannerResult{
			Scanner:      &entity.Scanner{ID: testScannerID, EventID: testEventID, Number: 2, Status: entity.ScannerStatusInUse},
			Window:       &entity.AdmissionWindow{OpenTime: start, CloseTime: start.Add(13 * time.Hour)},
			InsideWindow: true,
		}, nil)

		resp, err := f.h.Open(context.Background(), req)
		require.NoError(t, err)
		assert.Equal(t, int32(2), resp.Msg.Scanner.Number.Value)
		assert.Nil(t, resp.Msg.Scanner.LinkToken)
		assert.True(t, resp.Msg.AdmissionWindow.OpenTime.AsTime().Equal(start))
		assert.True(t, resp.Msg.InsideWindow)
	})

	t.Run("missing public key", func(t *testing.T) {
		t.Parallel()
		f := newScannerHandler(t)
		req := openRequest(t, testLinkToken, "203.0.113.1")
		req.Msg.PublicKey = nil
		_, err := f.h.Open(context.Background(), req)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}

func TestScannerHandler_Admit(t *testing.T) {
	t.Parallel()

	t.Run("missing signature", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/scanner-device "Missing signature"
		f := newScannerHandler(t)
		req := admitRequest(testLinkToken, "203.0.113.1")
		req.Msg.Signature = nil
		_, err := f.h.Admit(context.Background(), req)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		f.tickets.AssertNotCalled(t, "Admit", mock.Anything, mock.Anything)
	})

	t.Run("missing link token, sign time or scanned text", func(t *testing.T) {
		t.Parallel()
		f := newScannerHandler(t)
		for _, strip := range []func(*scannerv1.AdmitRequest){
			func(m *scannerv1.AdmitRequest) { m.LinkToken = nil },
			func(m *scannerv1.AdmitRequest) { m.SignTime = nil },
			func(m *scannerv1.AdmitRequest) { m.ScannedText = nil },
		} {
			req := admitRequest(testLinkToken, "203.0.113.1")
			strip(req.Msg)
			_, err := f.h.Admit(context.Background(), req)
			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		}
	})

	t.Run("result carries a head count and reasons, no personal data", func(t *testing.T) {
		t.Parallel()
		f := newScannerHandler(t)
		earlier := time.Date(2026, 11, 20, 9, 32, 0, 0, time.UTC)
		f.tickets.EXPECT().Admit(mock.Anything, mock.MatchedBy(func(in usecase.AdmitInput) bool {
			return in.Procedure == scannerv1connect.ScannerServiceAdmitProcedure &&
				in.LinkToken == testLinkToken && in.ScannedText == "6BFOXN*TS0BI$ZD"
		})).Return(&usecase.AdmitResult{
			AdmittedTicketCount: 2,
			RejectedTickets: []usecase.RejectedTicket{
				{Reason: entity.RejectedScanReasonAlreadyAdmitted, EarlierAdmitTime: &earlier, EarlierScannerNumber: 1},
			},
		}, nil)

		resp, err := f.h.Admit(context.Background(), admitRequest(testLinkToken, "203.0.113.1"))
		require.NoError(t, err)
		assert.Equal(t, int32(2), resp.Msg.AdmittedTicketCount)
		require.Len(t, resp.Msg.RejectedTickets, 1)
		rt := resp.Msg.RejectedTickets[0]
		assert.Equal(t, entityv1.RejectedScanReason_REJECTED_SCAN_REASON_ALREADY_ADMITTED, rt.Reason)
		assert.True(t, rt.EarlierAdmitTime.AsTime().Equal(earlier))
		assert.Equal(t, int32(1), rt.EarlierScannerNumber.Value)

		// Task 6.3: the response shape itself can carry no personal data —
		// only a count, reasons, a time and a scanner number. Any new field
		// (such as a full name, phone number or account) fails this test.
		assertFields(t, resp.Msg, "admitted_ticket_count", "rejected_scan_reason", "rejected_tickets")
		assertFields(t, rt, "reason", "earlier_admit_time", "earlier_scanner_number")
		assertFields(t, rt.EarlierScannerNumber, "value")
	})

	t.Run("whole-scan rejection", func(t *testing.T) {
		t.Parallel()
		f := newScannerHandler(t)
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
