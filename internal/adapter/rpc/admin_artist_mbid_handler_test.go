package rpc_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	adminartistv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/artist/v1"
	admincatalogartistv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/catalog_artist/v1"
	"connectrpc.com/connect/v2"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	mbidTestArtistID = "019a0000-0000-7000-8000-0000000000b1"
	ukBandMBID       = "c94eaf81-baae-465d-8197-9951c2ef52cd"
	jpBandMBID       = "9604bb00-65c5-4b67-a174-c46c063ba794"
)

func newAdminArtistHandler(t *testing.T, artistUC *mocks.MockArtistUseCase) *handler.AdminArtistHandler {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return handler.NewAdminArtistHandler(artistUC, logger)
}

func listByMbidStatusRequest(status entityv1.ArtistMbidStatus) *adminartistv1.ListByMbidStatusRequest {
	return &adminartistv1.ListByMbidStatusRequest{MbidStatus: status}
}

func correctMbidRequest(artistID, mbid string) *adminartistv1.CorrectMbidRequest {
	return &adminartistv1.CorrectMbidRequest{
		ArtistId: &entityv1.ArtistId{Value: artistID},
		Mbid:     &entityv1.Mbid{Value: mbid},
	}
}

func listByArtistRequest(artistID string) *admincatalogartistv1.ListByArtistRequest {
	return &admincatalogartistv1.ListByArtistRequest{ArtistId: &entityv1.ArtistId{Value: artistID}}
}

func TestAdminArtistHandler_ListByMbidStatus(t *testing.T) {
	t.Parallel()

	t.Run("admin lists the ambiguous artists", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/artist "Admin lists the ambiguous artists"
		artistUC := mocks.NewMockArtistUseCase(t)
		artistUC.EXPECT().ListByMBIDStatus(mock.Anything, entity.MBIDStatusAmbiguous).Return([]*entity.Artist{
			{ID: mbidTestArtistID, Name: "Penthouse", MBID: ukBandMBID},
			{ID: "019a0000-0000-7000-8000-0000000000b2", Name: "Yama", MBID: jpBandMBID},
		}, nil).Once()
		h := newAdminArtistHandler(t, artistUC)

		resp, err := callThroughAdminPipeline(t, adminCtx(), listByMbidStatusRequest(entityv1.ArtistMbidStatus_ARTIST_MBID_STATUS_AMBIGUOUS), h.ListByMbidStatus)

		require.NoError(t, err)
		require.Len(t, resp.GetArtists(), 2)
		assert.Equal(t, "Penthouse", resp.GetArtists()[0].GetName().GetValue())
		assert.Equal(t, ukBandMBID, resp.GetArtists()[0].GetMbid().GetValue())
		assert.Equal(t, "Yama", resp.GetArtists()[1].GetName().GetValue())
		assert.Equal(t, jpBandMBID, resp.GetArtists()[1].GetMbid().GetValue())
	})

	t.Run("missing status", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/artist "Missing status"
		h := newAdminArtistHandler(t, mocks.NewMockArtistUseCase(t))

		_, err := callThroughAdminPipeline(t, adminCtx(), listByMbidStatusRequest(entityv1.ArtistMbidStatus_ARTIST_MBID_STATUS_UNSPECIFIED), h.ListByMbidStatus)

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}

func TestAdminArtistHandler_CorrectMbid(t *testing.T) {
	t.Parallel()

	t.Run("admin chooses a namesake", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/artist "Admin chooses a namesake"
		artistUC := mocks.NewMockArtistUseCase(t)
		artistUC.EXPECT().CorrectMBID(mock.Anything, mbidTestArtistID, jpBandMBID).Return(&entity.Artist{
			ID: mbidTestArtistID, Name: "PENTHOUSE", MBID: jpBandMBID, ListeningMBID: ukBandMBID, MBIDStatus: entity.MBIDStatusVerified,
		}, nil).Once()
		h := newAdminArtistHandler(t, artistUC)

		resp, err := callThroughAdminPipeline(t, adminCtx(), correctMbidRequest(mbidTestArtistID, jpBandMBID), h.CorrectMbid)

		require.NoError(t, err)
		assert.Equal(t, jpBandMBID, resp.GetArtist().GetMbid().GetValue())
		assert.Equal(t, "PENTHOUSE", resp.GetArtist().GetName().GetValue(), "the catalog's name")
	})

	t.Run("MBID of another artist", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/artist "MBID of another artist"
		artistUC := mocks.NewMockArtistUseCase(t)
		artistUC.EXPECT().CorrectMBID(mock.Anything, mbidTestArtistID, jpBandMBID).
			Return(nil, apperr.New(codes.AlreadyExists, "another artist has the mbid")).Once()
		h := newAdminArtistHandler(t, artistUC)

		_, err := callThroughAdminPipeline(t, adminCtx(), correctMbidRequest(mbidTestArtistID, jpBandMBID), h.CorrectMbid)

		assert.Equal(t, connect.CodeAlreadyExists, connect.CodeOf(err))
	})

	t.Run("malformed MBID", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/artist "Malformed MBID"
		h := newAdminArtistHandler(t, mocks.NewMockArtistUseCase(t))

		_, err := callThroughAdminPipeline(t, adminCtx(), correctMbidRequest(mbidTestArtistID, "too-short"), h.CorrectMbid)

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}

func TestAdminArtistHandler_MBIDRole(t *testing.T) {
	t.Parallel()

	// No usecase has an expectation: reaching one fails the test.
	h := newAdminArtistHandler(t, mocks.NewMockArtistUseCase(t))

	// @spec components/adapter/admin/api/rpc/artist "Non-admin lists artists by status"
	_, err := callThroughAdminPipeline(t, nonAdminCtx(), listByMbidStatusRequest(entityv1.ArtistMbidStatus_ARTIST_MBID_STATUS_AMBIGUOUS), h.ListByMbidStatus)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "ListByMbidStatus")

	// @spec components/adapter/admin/api/rpc/artist "Non-admin corrects an MBID"
	_, err = callThroughAdminPipeline(t, nonAdminCtx(), correctMbidRequest(mbidTestArtistID, jpBandMBID), h.CorrectMbid)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "CorrectMbid")
}

func TestAdminCatalogArtistHandler_ListByArtist(t *testing.T) {
	t.Parallel()

	t.Run("admin reads the candidates", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/catalog-artist "Admin reads the candidates"
		gb, ukNote, jpNote, site := "GB", "UK indie rock band", "Japanese city soul band", "https://penthouse.example/"
		catalogUC := mocks.NewMockCatalogArtistUseCase(t)
		catalogUC.EXPECT().ListByArtist(mock.Anything, mbidTestArtistID).Return([]*entity.CatalogArtist{
			{
				MBID: ukBandMBID, Name: "Penthouse", Country: &gb,
				Begin: &entity.ActivityDate{Year: 1995, Month: 10}, End: &entity.ActivityDate{Year: 2002, Month: 11},
				ActivityEnded: true, Disambiguation: &ukNote,
			},
			{
				MBID: jpBandMBID, Name: "Penthouse",
				Begin: &entity.ActivityDate{Year: 2018}, Disambiguation: &jpNote, OfficialSiteURL: &site,
			},
		}, nil).Once()
		h := handler.NewAdminCatalogArtistHandler(catalogUC)

		resp, err := callThroughAdminPipeline(t, adminCtx(), listByArtistRequest(mbidTestArtistID), h.ListByArtist)

		require.NoError(t, err)
		require.Len(t, resp.GetCatalogArtists(), 2)
		uk, jp := resp.GetCatalogArtists()[0], resp.GetCatalogArtists()[1]
		assert.Equal(t, ukBandMBID, uk.GetMbid().GetValue())
		assert.Equal(t, "Penthouse", uk.GetName().GetValue())
		assert.Equal(t, "GB", uk.GetCountry().GetValue())
		assert.Equal(t, int32(1995), uk.GetBegin().GetValue().GetYear())
		assert.Equal(t, int32(10), uk.GetBegin().GetValue().GetMonth())
		assert.Equal(t, int32(2002), uk.GetEnd().GetValue().GetYear())
		assert.True(t, uk.GetActivityEnded())
		assert.Equal(t, ukNote, uk.GetDisambiguation().GetValue())
		assert.Nil(t, uk.GetOfficialSiteUrl())
		assert.Nil(t, jp.GetCountry())
		assert.Equal(t, int32(2018), jp.GetBegin().GetValue().GetYear())
		assert.Nil(t, jp.GetEnd())
		assert.Equal(t, site, jp.GetOfficialSiteUrl().GetValue())
	})

	t.Run("unknown artist", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/catalog-artist "Unknown artist"
		catalogUC := mocks.NewMockCatalogArtistUseCase(t)
		catalogUC.EXPECT().ListByArtist(mock.Anything, mbidTestArtistID).Return(nil, apperr.New(codes.NotFound, "artist not found")).Once()
		h := handler.NewAdminCatalogArtistHandler(catalogUC)

		_, err := callThroughAdminPipeline(t, adminCtx(), listByArtistRequest(mbidTestArtistID), h.ListByArtist)

		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})
}

func TestAdminCatalogArtistHandler_Role(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ctx      context.Context
		artistID string
		want     connect.Code
	}{
		// @spec components/adapter/admin/api/rpc/catalog-artist "Guest reads candidates"
		{"guest", context.Background(), mbidTestArtistID, connect.CodeUnauthenticated},
		// @spec components/adapter/admin/api/rpc/catalog-artist "Non-admin reads candidates"
		{"non-admin", nonAdminCtx(), mbidTestArtistID, connect.CodePermissionDenied},
		// @spec components/adapter/admin/api/rpc/catalog-artist "Guest with a malformed id"
		{"guest with a malformed id", context.Background(), "not-a-uuid", connect.CodeUnauthenticated},
		// @spec components/adapter/admin/api/rpc/catalog-artist "Non-admin with a malformed id"
		{"non-admin with a malformed id", nonAdminCtx(), "not-a-uuid", connect.CodePermissionDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// No usecase has an expectation: reaching one fails the test.
			h := handler.NewAdminCatalogArtistHandler(mocks.NewMockCatalogArtistUseCase(t))

			_, err := callThroughAdminPipeline(t, tt.ctx, listByArtistRequest(tt.artistID), h.ListByArtist)

			assert.Equal(t, tt.want, connect.CodeOf(err))
		})
	}
}

func createRequest(name, mbid string) *adminartistv1.CreateRequest {
	return &adminartistv1.CreateRequest{
		Name: &entityv1.ArtistName{Value: name},
		Mbid: &entityv1.Mbid{Value: mbid},
	}
}

func TestAdminArtistHandler_Create(t *testing.T) {
	t.Parallel()
	const generatedMBID = "6f1d2e3c-4b5a-4c7d-8e9f-0a1b2c3d4e5f"

	t.Run("admin registers a test artist", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/artist "Admin registers a test artist"
		artistUC := mocks.NewMockArtistUseCase(t)
		artistUC.EXPECT().Create(mock.Anything, mock.MatchedBy(func(a *entity.Artist) bool {
			return a.Name == "E2E Test Artist" && a.MBID == generatedMBID
		})).Return(&entity.Artist{ID: mbidTestArtistID, Name: "E2E Test Artist", MBID: generatedMBID, MBIDStatus: entity.MBIDStatusVerified}, nil).Once()
		h := newAdminArtistHandler(t, artistUC)

		resp, err := callThroughAdminPipeline(t, adminCtx(), createRequest("E2E Test Artist", generatedMBID), h.Create)

		require.NoError(t, err)
		assert.Equal(t, mbidTestArtistID, resp.GetArtist().GetId().GetValue())
		assert.Equal(t, "E2E Test Artist", resp.GetArtist().GetName().GetValue())
	})

	t.Run("malformed MBID", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/artist "Malformed MBID"
		// The mock fails the test if the usecase runs, so no artist is created.
		h := newAdminArtistHandler(t, mocks.NewMockArtistUseCase(t))

		_, err := callThroughAdminPipeline(t, adminCtx(), createRequest("E2E Test Artist", "not-a-uuid"), h.Create)

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("non-admin registers an artist", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/artist "Non-admin registers an artist"
		h := newAdminArtistHandler(t, mocks.NewMockArtistUseCase(t))

		_, err := callThroughAdminPipeline(t, nonAdminCtx(), createRequest("E2E Test Artist", generatedMBID), h.Create)

		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})
}
