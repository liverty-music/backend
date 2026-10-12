package rpc_test

import (
	"context"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	organizerscannerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/scanner/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	testEventID   = "019a0000-0000-7000-8000-0000000000e1"
	testScannerID = "019a0000-0000-7000-8000-0000000000a1"
)

func newOrganizerScannerHandler(t *testing.T) (*handler.OrganizerScannerHandler, *ucmocks.MockScannerUseCase, *ucmocks.MockOrganizerUseCase) {
	t.Helper()
	scanners := ucmocks.NewMockScannerUseCase(t)
	orgs := ucmocks.NewMockOrganizerUseCase(t)
	logger, err := logging.New()
	require.NoError(t, err)
	return handler.NewOrganizerScannerHandler(scanners, orgs, logger), scanners, orgs
}

func TestOrganizerScannerHandler(t *testing.T) {
	t.Parallel()

	ctx := auth.WithCallerOrgID(context.Background(), "zorg-1")

	t.Run("operator creates a scanner", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/scanner "Operator creates a scanner"
		h, scanners, orgs := newOrganizerScannerHandler(t)
		orgs.EXPECT().ResolveCaller(ctx, "zorg-1").Return(&entity.Organizer{ID: "org-1"}, nil)
		scanners.EXPECT().Create(ctx, "org-1", testEventID).Return(&entity.Scanner{
			ID: testScannerID, EventID: testEventID, Number: 1, LinkToken: "tok_0123456789abcdefghijklmn", Status: entity.ScannerStatusUnused,
		}, nil)

		resp, err := h.Create(ctx, connect.NewRequest(&organizerscannerv1.CreateRequest{EventId: &entityv1.EventId{Value: testEventID}}))
		require.NoError(t, err)
		got := resp.Msg.Scanner
		assert.Equal(t, testScannerID, got.Id.Value)
		assert.Equal(t, int32(1), got.Number.Value)
		assert.Equal(t, "tok_0123456789abcdefghijklmn", got.LinkToken.Value)
		assert.Equal(t, entityv1.ScannerStatus_SCANNER_STATUS_UNUSED, got.Status)
	})

	t.Run("list maps every scanner", func(t *testing.T) {
		t.Parallel()
		h, scanners, orgs := newOrganizerScannerHandler(t)
		bound := time.Date(2026, 11, 20, 5, 10, 0, 0, time.UTC)
		orgs.EXPECT().ResolveCaller(ctx, "zorg-1").Return(&entity.Organizer{ID: "org-1"}, nil)
		scanners.EXPECT().ListByEvent(ctx, "org-1", testEventID).Return([]*entity.Scanner{
			{ID: testScannerID, EventID: testEventID, Number: 1, Status: entity.ScannerStatusInUse, BoundTime: &bound},
		}, nil)

		resp, err := h.ListByEvent(ctx, connect.NewRequest(&organizerscannerv1.ListByEventRequest{EventId: &entityv1.EventId{Value: testEventID}}))
		require.NoError(t, err)
		require.Len(t, resp.Msg.Scanners, 1)
		assert.Nil(t, resp.Msg.Scanners[0].LinkToken)
		assert.True(t, resp.Msg.Scanners[0].BindTime.AsTime().Equal(bound))
	})

	t.Run("deactivated organizer", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/scanner "Deactivated Organizer"
		h, _, orgs := newOrganizerScannerHandler(t)
		deactivated := apperr.New(codes.FailedPrecondition, "organizer deactivated")
		orgs.EXPECT().ResolveCaller(ctx, "zorg-1").Return(nil, deactivated).Times(3)

		_, err := h.Create(ctx, connect.NewRequest(&organizerscannerv1.CreateRequest{EventId: &entityv1.EventId{Value: testEventID}}))
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		_, err = h.ListByEvent(ctx, connect.NewRequest(&organizerscannerv1.ListByEventRequest{EventId: &entityv1.EventId{Value: testEventID}}))
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		_, err = h.Revoke(ctx, connect.NewRequest(&organizerscannerv1.RevokeRequest{ScannerId: &entityv1.ScannerId{Value: testScannerID}}))
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("create without an event", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/scanner "Create without an event"
		h, _, _ := newOrganizerScannerHandler(t)
		_, err := h.Create(ctx, connect.NewRequest(&organizerscannerv1.CreateRequest{}))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("malformed ids are refused before any usecase", func(t *testing.T) {
		t.Parallel()
		h, _, _ := newOrganizerScannerHandler(t)
		_, err := h.ListByEvent(ctx, connect.NewRequest(&organizerscannerv1.ListByEventRequest{EventId: &entityv1.EventId{Value: "not-a-uuid"}}))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		_, err = h.Revoke(ctx, connect.NewRequest(&organizerscannerv1.RevokeRequest{}))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("revoke passes the current time", func(t *testing.T) {
		t.Parallel()
		h, scanners, orgs := newOrganizerScannerHandler(t)
		orgs.EXPECT().ResolveCaller(ctx, "zorg-1").Return(&entity.Organizer{ID: "org-1"}, nil)
		revoked := time.Now()
		scanners.EXPECT().Revoke(ctx, "org-1", entity.ScannerID(testScannerID), mock.AnythingOfType("time.Time")).
			Return(&entity.Scanner{ID: testScannerID, EventID: testEventID, Number: 1, Status: entity.ScannerStatusRevoked, RevokedTime: &revoked}, nil)

		resp, err := h.Revoke(ctx, connect.NewRequest(&organizerscannerv1.RevokeRequest{ScannerId: &entityv1.ScannerId{Value: testScannerID}}))
		require.NoError(t, err)
		assert.Equal(t, entityv1.ScannerStatus_SCANNER_STATUS_REVOKED, resp.Msg.Scanner.Status)
		assert.NotNil(t, resp.Msg.Scanner.RevokeTime)
	})

	t.Run("no caller org id", func(t *testing.T) {
		t.Parallel()
		h, _, _ := newOrganizerScannerHandler(t)
		_, err := h.Create(context.Background(), connect.NewRequest(&organizerscannerv1.CreateRequest{EventId: &entityv1.EventId{Value: testEventID}}))
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})
}
