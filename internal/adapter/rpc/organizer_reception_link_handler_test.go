package rpc_test

import (
	"context"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	receptionlinkv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/reception_link/v1"
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
	testEventID = "019a0000-0000-7000-8000-0000000000e1"
	testLinkID  = "019a0000-0000-7000-8000-0000000000a1"
)

func newOrganizerReceptionLinkHandler(t *testing.T) (*handler.OrganizerReceptionLinkHandler, *ucmocks.MockReceptionLinkUseCase, *ucmocks.MockOrganizerUseCase) {
	t.Helper()
	links := ucmocks.NewMockReceptionLinkUseCase(t)
	orgs := ucmocks.NewMockOrganizerUseCase(t)
	logger, err := logging.New()
	require.NoError(t, err)
	return handler.NewOrganizerReceptionLinkHandler(links, orgs, logger), links, orgs
}

func TestOrganizerReceptionLinkHandler(t *testing.T) {
	t.Parallel()

	ctx := auth.WithCallerOrgID(context.Background(), "zorg-1")

	t.Run("operator issues a link", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/reception-link "Operator issues a link"
		h, links, orgs := newOrganizerReceptionLinkHandler(t)
		orgs.EXPECT().ResolveCaller(ctx, "zorg-1").Return(&entity.Organizer{ID: "org-1"}, nil)
		links.EXPECT().Issue(ctx, "org-1", testEventID).Return(&entity.ReceptionLink{
			ID: testLinkID, EventID: testEventID, Number: 1, Token: "tok_0123456789abcdefghijklmn", Status: entity.ReceptionLinkStatusUnused,
		}, nil)

		resp, err := h.Issue(ctx, connect.NewRequest(&receptionlinkv1.IssueRequest{EventId: &entityv1.EventId{Value: testEventID}}))
		require.NoError(t, err)
		got := resp.Msg.ReceptionLink
		assert.Equal(t, testLinkID, got.Id.Value)
		assert.Equal(t, int32(1), got.Number.Value)
		assert.Equal(t, "tok_0123456789abcdefghijklmn", got.Token.Value)
		assert.Equal(t, entityv1.ReceptionLinkStatus_RECEPTION_LINK_STATUS_UNUSED, got.Status)
	})

	t.Run("list maps every link", func(t *testing.T) {
		t.Parallel()
		h, links, orgs := newOrganizerReceptionLinkHandler(t)
		bound := time.Date(2026, 11, 20, 5, 10, 0, 0, time.UTC)
		orgs.EXPECT().ResolveCaller(ctx, "zorg-1").Return(&entity.Organizer{ID: "org-1"}, nil)
		links.EXPECT().ListByEvent(ctx, "org-1", testEventID).Return([]*entity.ReceptionLink{
			{ID: testLinkID, EventID: testEventID, Number: 1, Status: entity.ReceptionLinkStatusInUse, BoundTime: &bound},
		}, nil)

		resp, err := h.List(ctx, connect.NewRequest(&receptionlinkv1.ListRequest{EventId: &entityv1.EventId{Value: testEventID}}))
		require.NoError(t, err)
		require.Len(t, resp.Msg.ReceptionLinks, 1)
		assert.Nil(t, resp.Msg.ReceptionLinks[0].Token)
		assert.True(t, resp.Msg.ReceptionLinks[0].BindTime.AsTime().Equal(bound))
	})

	t.Run("deactivated organizer", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/reception-link "Deactivated Organizer"
		h, _, orgs := newOrganizerReceptionLinkHandler(t)
		deactivated := apperr.New(codes.FailedPrecondition, "organizer deactivated")
		orgs.EXPECT().ResolveCaller(ctx, "zorg-1").Return(nil, deactivated).Times(3)

		_, err := h.Issue(ctx, connect.NewRequest(&receptionlinkv1.IssueRequest{EventId: &entityv1.EventId{Value: testEventID}}))
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		_, err = h.List(ctx, connect.NewRequest(&receptionlinkv1.ListRequest{EventId: &entityv1.EventId{Value: testEventID}}))
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		_, err = h.Revoke(ctx, connect.NewRequest(&receptionlinkv1.RevokeRequest{ReceptionLinkId: &entityv1.ReceptionLinkId{Value: testLinkID}}))
		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("issue without an event", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/reception-link "Issue without an event"
		h, _, _ := newOrganizerReceptionLinkHandler(t)
		_, err := h.Issue(ctx, connect.NewRequest(&receptionlinkv1.IssueRequest{}))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("malformed ids are refused before any usecase", func(t *testing.T) {
		t.Parallel()
		h, _, _ := newOrganizerReceptionLinkHandler(t)
		_, err := h.List(ctx, connect.NewRequest(&receptionlinkv1.ListRequest{EventId: &entityv1.EventId{Value: "not-a-uuid"}}))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		_, err = h.Revoke(ctx, connect.NewRequest(&receptionlinkv1.RevokeRequest{}))
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("revoke passes the current time", func(t *testing.T) {
		t.Parallel()
		h, links, orgs := newOrganizerReceptionLinkHandler(t)
		orgs.EXPECT().ResolveCaller(ctx, "zorg-1").Return(&entity.Organizer{ID: "org-1"}, nil)
		revoked := time.Now()
		links.EXPECT().Revoke(ctx, "org-1", entity.ReceptionLinkID(testLinkID), mock.AnythingOfType("time.Time")).
			Return(&entity.ReceptionLink{ID: testLinkID, EventID: testEventID, Number: 1, Status: entity.ReceptionLinkStatusRevoked, RevokedTime: &revoked}, nil)

		resp, err := h.Revoke(ctx, connect.NewRequest(&receptionlinkv1.RevokeRequest{ReceptionLinkId: &entityv1.ReceptionLinkId{Value: testLinkID}}))
		require.NoError(t, err)
		assert.Equal(t, entityv1.ReceptionLinkStatus_RECEPTION_LINK_STATUS_REVOKED, resp.Msg.ReceptionLink.Status)
		assert.NotNil(t, resp.Msg.ReceptionLink.RevokeTime)
	})

	t.Run("no caller org id", func(t *testing.T) {
		t.Parallel()
		h, _, _ := newOrganizerReceptionLinkHandler(t)
		_, err := h.Issue(context.Background(), connect.NewRequest(&receptionlinkv1.IssueRequest{EventId: &entityv1.EventId{Value: testEventID}}))
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})
}
