package rpc_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	concertv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/concert/v1/concertv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	concertv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/concert/v1"
	"connectrpc.com/authn"
	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	authmocks "github.com/liverty-music/backend/internal/infrastructure/auth/mocks"
	"github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/date"
)

// newConcertBoundaryClient serves the fan ConcertService behind the same
// authn middleware (with the production public-procedure allowlist) and
// validation interceptor as the consumer server, and returns a client that
// sends no bearer token, i.e. a guest.
func newConcertBoundaryClient(t *testing.T, concertUC *mocks.MockConcertUseCase) concertv1connect.ConcertServiceClient {
	t.Helper()

	logger, err := logging.New()
	require.NoError(t, err)
	h := rpc.NewConcertHandler(concertUC, entitymocks.NewMockUserRepository(t), mapper.NewMediaURLBuilder(""), logger)

	path, svc := concertv1connect.NewConcertServiceHandler(h, connect.WithInterceptors(validate.NewInterceptor()))
	mux := http.NewServeMux()
	mux.Handle(path, svc)
	authFunc := auth.NewAuthFunc(authmocks.NewMockTokenValidator(t), auth.FanPublicProcedures())
	srv := httptest.NewServer(authn.NewMiddleware(authFunc).Wrap(mux))
	t.Cleanup(srv.Close)

	return concertv1connect.NewConcertServiceClient(srv.Client(), srv.URL)
}

func TestConcertHandler_Get_Boundary(t *testing.T) {
	t.Parallel()

	const eventID = "019a0000-0000-7000-8000-0000000000e1"

	t.Run("run Get for a guest", func(t *testing.T) {
		// @spec components/adapter/fan/api/rpc/concert "Guest opens an event page"
		t.Parallel()
		concertUC := mocks.NewMockConcertUseCase(t)
		concertUC.EXPECT().Get(mock.Anything, eventID).
			Return(firstPartyConcert(eventID, time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)), nil).Once()
		client := newConcertBoundaryClient(t, concertUC)

		resp, err := client.Get(context.Background(), connect.NewRequest(&concertv1.GetRequest{
			EventId: &entityv1.EventId{Value: eventID},
		}))

		require.NoError(t, err)
		assert.Equal(t, eventID, resp.Msg.GetConcert().GetId().GetValue())
	})

	tests := []struct {
		name string
		req  *concertv1.GetRequest
	}{
		{
			// @spec components/adapter/fan/api/rpc/concert "Event page without an id"
			name: "return INVALID_ARGUMENT without an event id",
			req:  &concertv1.GetRequest{},
		},
		{
			name: "return INVALID_ARGUMENT for an event id that is not a UUID",
			req:  &concertv1.GetRequest{EventId: &entityv1.EventId{Value: "not-a-uuid"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// The mock fails the test if any usecase method runs.
			client := newConcertBoundaryClient(t, mocks.NewMockConcertUseCase(t))

			resp, err := client.Get(context.Background(), connect.NewRequest(tt.req))

			assert.Nil(t, resp)
			assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		})
	}
}

func TestConcertHandler_ListBySeries_Boundary(t *testing.T) {
	t.Parallel()

	t.Run("run ListBySeries for a guest", func(t *testing.T) {
		// @spec components/adapter/fan/api/rpc/concert "Guest lists the dates of a series"
		t.Parallel()
		concertUC := mocks.NewMockConcertUseCase(t)
		concertUC.EXPECT().ListBySeries(mock.Anything, eventPageSeriesID).Return([]*entity.Concert{
			firstPartyConcert("019a0000-0000-7000-8000-0000000000e2", time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)),
		}, nil).Once()
		client := newConcertBoundaryClient(t, concertUC)

		resp, err := client.ListBySeries(context.Background(), connect.NewRequest(&concertv1.ListBySeriesRequest{
			SeriesId: &entityv1.SeriesId{Value: eventPageSeriesID},
		}))

		require.NoError(t, err)
		assert.Len(t, resp.Msg.GetConcerts(), 1)
	})

	t.Run("return INVALID_ARGUMENT without a series id", func(t *testing.T) {
		t.Parallel()
		client := newConcertBoundaryClient(t, mocks.NewMockConcertUseCase(t))

		resp, err := client.ListBySeries(context.Background(), connect.NewRequest(&concertv1.ListBySeriesRequest{}))

		assert.Nil(t, resp)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("return UNAUTHENTICATED for a guest calling ListByFollower", func(t *testing.T) {
		// @spec components/adapter/fan/api/rpc/concert "Guest asks for followed artists' concerts"
		t.Parallel()
		client := newConcertBoundaryClient(t, mocks.NewMockConcertUseCase(t))

		_, err := client.ListByFollower(context.Background(), connect.NewRequest(&concertv1.ListByFollowerRequest{}))

		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})
}

func TestConcertHandler_PublicLists_Boundary(t *testing.T) {
	t.Parallel()

	artistID := func(i int) *entityv1.ArtistId {
		return &entityv1.ArtistId{Value: fmt.Sprintf("019a0000-0000-7000-8000-%012d", i)}
	}
	home := &entityv1.Home{CountryCode: "JP", Level_1: "JP-13"}

	t.Run("run ListByArtists for a guest", func(t *testing.T) {
		// @spec components/adapter/fan/api/rpc/concert "Guest lists concerts of chosen artists"
		t.Parallel()
		concertUC := mocks.NewMockConcertUseCase(t)
		concertUC.EXPECT().ListByArtists(mock.Anything, []string{artistID(1).GetValue(), artistID(2).GetValue()}, mock.Anything).
			Return([]*entity.ProximityGroup{}, nil).Once()
		client := newConcertBoundaryClient(t, concertUC)

		_, err := client.ListByArtists(context.Background(), connect.NewRequest(&concertv1.ListByArtistsRequest{
			ArtistIds: []*entityv1.ArtistId{artistID(1), artistID(2)},
			Home:      home,
		}))

		require.NoError(t, err)
	})

	t.Run("return INVALID_ARGUMENT for 51 artists", func(t *testing.T) {
		// @spec components/adapter/fan/api/rpc/concert "Too many artists"
		t.Parallel()
		ids := make([]*entityv1.ArtistId, 51)
		for i := range ids {
			ids[i] = artistID(i)
		}
		client := newConcertBoundaryClient(t, mocks.NewMockConcertUseCase(t))

		_, err := client.ListByArtists(context.Background(), connect.NewRequest(&concertv1.ListByArtistsRequest{
			ArtistIds: ids,
			Home:      home,
		}))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("return INVALID_ARGUMENT for a range that ends before it starts", func(t *testing.T) {
		// @spec components/adapter/fan/api/rpc/concert "Range backwards"
		t.Parallel()
		client := newConcertBoundaryClient(t, mocks.NewMockConcertUseCase(t))

		_, err := client.ListByLocation(context.Background(), connect.NewRequest(&concertv1.ListByLocationRequest{
			Location: &entityv1.GeoLocation{Latitude: 35.68, Longitude: 139.76, AdminArea: "JP-13"},
			From:     &entityv1.LocalDate{Value: &date.Date{Year: 2026, Month: 10, Day: 2}},
			To:       &entityv1.LocalDate{Value: &date.Date{Year: 2026, Month: 10, Day: 1}},
		}))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("return INVALID_ARGUMENT for a search without an artist", func(t *testing.T) {
		// @spec components/adapter/fan/api/rpc/concert "Search without an artist"
		t.Parallel()
		// The mock fails the test if SearchNewConcerts runs.
		client := newConcertBoundaryClient(t, mocks.NewMockConcertUseCase(t))

		_, err := client.SearchNewConcerts(context.Background(), connect.NewRequest(&concertv1.SearchNewConcertsRequest{}))

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}
