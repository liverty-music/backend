package rpc_test

import (
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	organizervenuev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/venue/v1"
	"connectrpc.com/connect/v2"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
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

func TestOrganizerVenueHandler_ResolvePlace(t *testing.T) {
	t.Parallel()

	const gocheok = "ChIJgocheok"
	request := func(placeID string) *organizervenuev1.ResolvePlaceRequest {
		return &organizervenuev1.ResolvePlaceRequest{PlaceId: &entityv1.PlaceId{Value: placeID}}
	}

	// @spec components/adapter/organizer/api/rpc/venue "New place for an active Organizer"
	t.Run("return the place draft of Gocheok Sky Dome with KR, KR-11 and Asia/Seoul", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerVenueHandlerDeps(t)
		d.organizerUC.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).Return(activeOrganizer(), nil).Once()
		d.venueUC.EXPECT().ResolvePlace(mock.Anything, testOrganizerID, gocheok).Return(&usecase.ResolvedPlace{
			Draft: &usecase.PlaceDraft{Name: "Gocheok Sky Dome", CountryCode: "KR", SuggestedAdminArea: new("KR-11"), TimeZone: new("Asia/Seoul")},
		}, nil).Once()

		resp, err := d.handler.ResolvePlace(orgCtx(testProviderOrganizerID), request(gocheok))

		require.NoError(t, err)
		draft := resp.GetDraft()
		require.NotNil(t, draft)
		assert.Equal(t, "Gocheok Sky Dome", draft.GetName().GetValue())
		assert.Equal(t, "KR", draft.GetCountryCode().GetValue())
		assert.Equal(t, "KR-11", draft.GetSuggestedAdminArea().GetValue())
		assert.Equal(t, "Asia/Seoul", draft.GetTimeZone().GetValue())
	})

	t.Run("leave the suggested area and zone out of a draft that has none", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerVenueHandlerDeps(t)
		d.organizerUC.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).Return(activeOrganizer(), nil).Once()
		d.venueUC.EXPECT().ResolvePlace(mock.Anything, testOrganizerID, gocheok).Return(&usecase.ResolvedPlace{
			Draft: &usecase.PlaceDraft{Name: "Somewhere", CountryCode: "US"},
		}, nil).Once()

		resp, err := d.handler.ResolvePlace(orgCtx(testProviderOrganizerID), request(gocheok))

		require.NoError(t, err)
		assert.False(t, resp.GetDraft().HasSuggestedAdminArea())
		assert.False(t, resp.GetDraft().HasTimeZone())
	})

	// @spec components/adapter/organizer/api/rpc/venue "Place already a Venue"
	t.Run("return the Venue holding the place with its name, area and zone", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerVenueHandlerDeps(t)
		d.organizerUC.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).Return(activeOrganizer(), nil).Once()
		d.venueUC.EXPECT().ResolvePlace(mock.Anything, testOrganizerID, "ChIJzepp").Return(&usecase.ResolvedPlace{
			Venue: &entity.Venue{ID: "019a0000-0000-7000-8000-0000000000v1", Name: "Zepp Haneda", AdminArea: new("JP-13"), TimeZone: new("Asia/Tokyo"), GooglePlaceID: new("ChIJzepp")},
		}, nil).Once()

		resp, err := d.handler.ResolvePlace(orgCtx(testProviderOrganizerID), request("ChIJzepp"))

		require.NoError(t, err)
		venue := resp.GetVenue()
		require.NotNil(t, venue)
		assert.Equal(t, "Zepp Haneda", venue.GetName().GetValue())
		assert.Equal(t, "JP-13", venue.GetAdminArea().GetValue())
		assert.Equal(t, "Asia/Tokyo", venue.GetTimeZone().GetValue())
	})

	// @spec components/adapter/organizer/api/rpc/venue "Picked place without an Organizer"
	t.Run("return PERMISSION_DENIED without resolving the place when no organizer is linked", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerVenueHandlerDeps(t)
		// VenueUseCase (the only path to the map catalog) has no expectation.
		resolveErr := apperr.New(apperr.ErrPermissionDenied.Code, "permission denied")
		d.organizerUC.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).Return(nil, resolveErr).Once()

		_, err := d.handler.ResolvePlace(orgCtx(testProviderOrganizerID), request(gocheok))

		assert.ErrorIs(t, err, resolveErr)
		assert.Equal(t, connect.CodePermissionDenied, connectCodeOf(err))
	})
}
