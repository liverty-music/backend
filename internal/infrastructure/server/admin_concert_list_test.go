package server_test

import (
	"context"
	"testing"

	adminconcertv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/admin/concert/v1/concertv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	adminconcertv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/concert/v1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	authmocks "github.com/liverty-music/backend/internal/infrastructure/auth/mocks"
	usecasemocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestAdminServer_ConcertListByArtist covers admin ConcertService.List with an
// artist through the admin server's validation and role gate.
func TestAdminServer_ConcertListByArtist(t *testing.T) {
	t.Parallel()

	const (
		adminToken = "concert-admin-token"
		artistID   = "019a0000-0000-7000-8000-0000000000a1"
		seriesID   = "019a0000-0000-7000-8000-0000000000b1"
		eventID    = "019a0000-0000-7000-8000-0000000000c1"
	)
	newServer := func(t *testing.T, concertUC *usecasemocks.MockConcertUseCase) adminconcertv1connect.ConcertServiceClient {
		t.Helper()
		validator := authmocks.NewMockTokenValidator(t)
		validator.EXPECT().ValidateToken(mock.Anything, adminToken).
			Return(&auth.Claims{Sub: "admin-user", Roles: []string{"admin"}}, nil).Maybe()
		ts := newTestAdminServer(t, validator, usecasemocks.NewMockArtistUseCase(t), concertUC)
		return adminconcertv1connect.NewConcertServiceClient(connect.NewClient(connecthttp.NewTransport(ts.Client(), ts.URL)))
	}

	t.Run("admin lists one artist's concerts", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/concert "Admin lists one artist's concerts"
		cancelled := entity.PublishStateCancelled
		artist := &entity.Artist{ID: artistID, Name: "Test Artist"}
		concert := &entity.Concert{
			ID: eventID, SeriesID: seriesID, PublishState: &cancelled,
			Series:  &entity.Series{ID: seriesID, Title: "Cancelled Tour"},
			Artists: []*entity.Artist{artist},
		}
		concertUC := usecasemocks.NewMockConcertUseCase(t)
		concertUC.EXPECT().List(mock.Anything, artistID).Return([]*entity.Concert{concert}, nil).Once()

		resp, err := newServer(t, concertUC).List(withBearer(context.Background(), adminToken),
			&adminconcertv1.ListRequest{ArtistId: &entityv1.ArtistId{Value: artistID}})

		require.NoError(t, err)
		require.Len(t, resp.GetConcerts(), 1)
		assert.Equal(t, eventID, resp.GetConcerts()[0].GetEvent().GetId().GetValue())
		require.Len(t, resp.GetSeries(), 1)
		assert.Equal(t, seriesID, resp.GetSeries()[0].GetId().GetValue())
		require.Len(t, resp.GetArtists(), 1)
		assert.Equal(t, artistID, resp.GetArtists()[0].GetId().GetValue())
	})

	t.Run("malformed artist id", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/concert "Malformed artist id"
		concertUC := usecasemocks.NewMockConcertUseCase(t)

		_, err := newServer(t, concertUC).List(withBearer(context.Background(), adminToken),
			&adminconcertv1.ListRequest{ArtistId: &entityv1.ArtistId{Value: "not-a-uuid"}})

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		concertUC.AssertNotCalled(t, "List", mock.Anything, mock.Anything)
	})
}
