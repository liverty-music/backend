package rpc_test

import (
	"testing"

	organizervenuev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/venue/v1"
	"connectrpc.com/connect/v2"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// organizerVenueHandlerDeps bundles the mocked collaborators of
// OrganizerVenueHandler. None has an expectation unless a test sets one, so
// an unexpected call fails the test.
type organizerVenueHandlerDeps struct {
	venueUC     *ucmocks.MockVenueUseCase
	organizerUC *ucmocks.MockOrganizerUseCase
	handler     *rpc.OrganizerVenueHandler
}

func newOrganizerVenueHandlerDeps(t *testing.T) *organizerVenueHandlerDeps {
	t.Helper()
	d := &organizerVenueHandlerDeps{
		venueUC:     ucmocks.NewMockVenueUseCase(t),
		organizerUC: ucmocks.NewMockOrganizerUseCase(t),
	}
	d.handler = rpc.NewOrganizerVenueHandler(d.venueUC, d.organizerUC)
	return d
}

func TestOrganizerVenueHandler_Search(t *testing.T) {
	t.Parallel()

	// @spec components/adapter/organizer/api/rpc/venue "Venue search for an active Organizer"
	t.Run("return the candidate places for a member of an active organizer", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerVenueHandlerDeps(t)

		d.organizerUC.EXPECT().
			ResolveCaller(mock.Anything, testProviderOrganizerID).
			Return(activeOrganizer(), nil).
			Once()
		d.venueUC.EXPECT().
			Search(mock.Anything, testOrganizerID, "Zepp Haneda").
			Return([]*entity.MapPlaceCandidate{
				{PlaceID: "ChIJeVmAOuphGGARoWY4-GPmhhA", Name: "Zepp Haneda (TOKYO)", Address: "〒144-0041 東京都大田区羽田空港１丁目１−４"},
				{PlaceID: "ChIJother", Name: "Zepp DiverCity (TOKYO)", Address: "〒135-0064 東京都江東区青海１丁目１−１０"},
			}, nil).
			Once()

		resp, err := d.handler.Search(orgCtx(testProviderOrganizerID), &organizervenuev1.SearchRequest{Text: "Zepp Haneda"})

		require.NoError(t, err)
		require.Len(t, resp.GetCandidates(), 2)
		first := resp.GetCandidates()[0]
		assert.Equal(t, "ChIJeVmAOuphGGARoWY4-GPmhhA", first.GetPlaceId().GetValue())
		assert.Equal(t, "Zepp Haneda (TOKYO)", first.GetName().GetValue())
		assert.Equal(t, "〒144-0041 東京都大田区羽田空港１丁目１−４", first.GetAddress())
		assert.Equal(t, "ChIJother", resp.GetCandidates()[1].GetPlaceId().GetValue())
	})

	// @spec components/adapter/organizer/api/rpc/venue "Venue search without an Organizer"
	t.Run("return PERMISSION_DENIED without calling the map catalog when no organizer is linked to the Organization", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerVenueHandlerDeps(t)

		// The authoring usecase (the only path to the map catalog) has no
		// expectation: reaching it fails the test.
		resolveErr := apperr.New(apperr.ErrPermissionDenied.Code, "permission denied")
		d.organizerUC.EXPECT().
			ResolveCaller(mock.Anything, testProviderOrganizerID).
			Return(nil, resolveErr).
			Once()

		_, err := d.handler.Search(orgCtx(testProviderOrganizerID), &organizervenuev1.SearchRequest{Text: "Zepp Haneda"})

		require.Error(t, err)
		assert.ErrorIs(t, err, resolveErr)
		assert.Equal(t, connect.CodePermissionDenied, connectCodeOf(err))
	})
}
