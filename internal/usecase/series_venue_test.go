package usecase_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	authoringOrgID       = "org-1"
	authoringArtistID    = "artist-1"
	zeppHanedaPlaceID    = "ChIJeVmAOuphGGARoWY4-GPmhhA"
	quattroShibuyaPlace  = "ChIJquattro-shibuya"
	quattroUmedaPlace    = "ChIJquattro-umeda"
	existingVenueID      = "019a0000-0000-7000-8000-0000000000v1"
	createdVenueID       = "019a0000-0000-7000-8000-0000000000v2"
	createdSecondVenueID = "019a0000-0000-7000-8000-0000000000v3"
)

// draftInput builds one authored event at a future date for the given picked
// place id; a nil placeID is an event whose venue was typed but not picked.
func draftInput(venueName string, placeID *string) *usecase.DraftEventInput {
	return &usecase.DraftEventInput{VenueName: venueName, PlaceID: placeID, LocalDate: futureDate()}
}

// newDraftSeries returns a minimal valid series to author.
func newDraftSeries() *entity.Series {
	vis := entity.SeriesVisibilityPublic
	return &entity.Series{Title: "ONE MAN LIVE", Type: entity.SeriesTypeSingle, Visibility: &vis}
}

func TestConcertAuthoringUseCase_CreateDraft_PickedPlace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		inputs []*usecase.DraftEventInput
		// setup sets the expectations beyond the performer check. A
		// repository or catalog method without an expectation fails the test
		// when called, which is how "nothing is stored" and "the catalog is
		// not read" are asserted.
		setup       func(d *authoringDeps, created *[]*entity.Venue)
		wantVenues  []string
		wantErr     error
		wantStored  bool
		checkCreate func(t *testing.T, created []*entity.Venue)
	}{
		{
			// @spec components/usecase/series/create "Known place id"
			name:   "use the Venue holding the place id without reading the catalog",
			inputs: []*usecase.DraftEventInput{draftInput("Zepp Haneda", new(zeppHanedaPlaceID))},
			setup: func(d *authoringDeps, created *[]*entity.Venue) {
				d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, zeppHanedaPlaceID).
					Return(&entity.Venue{ID: existingVenueID, Name: "Zepp Haneda (TOKYO)", GooglePlaceID: new(zeppHanedaPlaceID)}, nil).Once()
			},
			wantVenues: []string{existingVenueID},
			wantStored: true,
		},
		{
			// @spec components/usecase/series/create "New place"
			name:   "create a Venue for Zepp Haneda with its place id, coordinates and admin area JP-13",
			inputs: []*usecase.DraftEventInput{draftInput("Zepp Haneda", new(zeppHanedaPlaceID))},
			setup: func(d *authoringDeps, created *[]*entity.Venue) {
				d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, zeppHanedaPlaceID).
					Return(nil, apperr.New(codes.NotFound, "venue not found")).Once()
				d.mapCatalog.EXPECT().GetPlace(mock.Anything, zeppHanedaPlaceID).Return(&entity.MapPlace{
					PlaceID:     zeppHanedaPlaceID,
					Name:        "Zepp Haneda (TOKYO)",
					Coordinates: &entity.Coordinates{Latitude: 35.5473958, Longitude: 139.7565213},
					AdminArea:   new("JP-13"),
				}, nil).Once()
				d.venueRepo.EXPECT().Create(mock.Anything, mock.Anything).
					Run(func(_ context.Context, v *entity.Venue) { *created = append(*created, v) }).
					Return(createdVenueID, nil).Once()
			},
			wantVenues: []string{createdVenueID},
			wantStored: true,
			checkCreate: func(t *testing.T, created []*entity.Venue) {
				t.Helper()
				require.Len(t, created, 1)
				v := created[0]
				assert.NotEmpty(t, v.ID)
				assert.Equal(t, "Zepp Haneda (TOKYO)", v.Name)
				assert.Equal(t, new(zeppHanedaPlaceID), v.GooglePlaceID)
				assert.Equal(t, &entity.Coordinates{Latitude: 35.5473958, Longitude: 139.7565213}, v.Coordinates)
				assert.Equal(t, new("JP-13"), v.AdminArea)
				// The typed name stays on the event, not on the Venue, so the
				// listed-name dedupe cannot merge this place into another Venue.
				assert.Nil(t, v.ListedVenueName)
			},
		},
		{
			// @spec components/usecase/series/create "Same name in two cities"
			name: "give CLUB QUATTRO in Shibuya and in Umeda two different Venues in JP-13 and JP-27",
			inputs: []*usecase.DraftEventInput{
				draftInput("CLUB QUATTRO", new(quattroShibuyaPlace)),
				draftInput("CLUB QUATTRO", new(quattroUmedaPlace)),
			},
			setup: func(d *authoringDeps, created *[]*entity.Venue) {
				d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, quattroShibuyaPlace).
					Return(nil, apperr.New(codes.NotFound, "venue not found")).Once()
				d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, quattroUmedaPlace).
					Return(nil, apperr.New(codes.NotFound, "venue not found")).Once()
				d.mapCatalog.EXPECT().GetPlace(mock.Anything, quattroShibuyaPlace).Return(&entity.MapPlace{
					PlaceID: quattroShibuyaPlace, Name: "渋谷CLUB QUATTRO",
					Coordinates: &entity.Coordinates{Latitude: 35.6614, Longitude: 139.6976}, AdminArea: new("JP-13"),
				}, nil).Once()
				d.mapCatalog.EXPECT().GetPlace(mock.Anything, quattroUmedaPlace).Return(&entity.MapPlace{
					PlaceID: quattroUmedaPlace, Name: "梅田CLUB QUATTRO",
					Coordinates: &entity.Coordinates{Latitude: 34.7036, Longitude: 135.5003}, AdminArea: new("JP-27"),
				}, nil).Once()
				d.venueRepo.EXPECT().Create(mock.Anything, mock.MatchedBy(func(v *entity.Venue) bool {
					return *v.GooglePlaceID == quattroShibuyaPlace
				})).Run(func(_ context.Context, v *entity.Venue) { *created = append(*created, v) }).Return(createdVenueID, nil).Once()
				d.venueRepo.EXPECT().Create(mock.Anything, mock.MatchedBy(func(v *entity.Venue) bool {
					return *v.GooglePlaceID == quattroUmedaPlace
				})).Run(func(_ context.Context, v *entity.Venue) { *created = append(*created, v) }).Return(createdSecondVenueID, nil).Once()
			},
			wantVenues: []string{createdVenueID, createdSecondVenueID},
			wantStored: true,
			checkCreate: func(t *testing.T, created []*entity.Venue) {
				t.Helper()
				require.Len(t, created, 2)
				assert.Equal(t, new("JP-13"), created[0].AdminArea)
				assert.Equal(t, new("JP-27"), created[1].AdminArea)
			},
		},
		{
			// @spec components/usecase/series/create "Venue typed but not picked"
			name:    "fail with INVALID_ARGUMENT and store nothing when an event gives a venue name but no place id",
			inputs:  []*usecase.DraftEventInput{draftInput("Zepp Haneda", nil)},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "fail with INVALID_ARGUMENT and store nothing when an event gives an empty place id",
			inputs:  []*usecase.DraftEventInput{draftInput("Zepp Haneda", new(" "))},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "fail with INVALID_ARGUMENT and store nothing when only a later event lacks a place id",
			inputs: []*usecase.DraftEventInput{
				draftInput("Zepp Haneda", new(zeppHanedaPlaceID)),
				draftInput("CLUB QUATTRO", nil),
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			// @spec components/usecase/series/create "Catalog down for a new place"
			name:   "fail with UNAVAILABLE and store nothing when no Venue holds the place id and the catalog cannot be reached",
			inputs: []*usecase.DraftEventInput{draftInput("Zepp Haneda", new(zeppHanedaPlaceID))},
			setup: func(d *authoringDeps, created *[]*entity.Venue) {
				d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, zeppHanedaPlaceID).
					Return(nil, apperr.New(codes.NotFound, "venue not found")).Once()
				d.mapCatalog.EXPECT().GetPlace(mock.Anything, zeppHanedaPlaceID).
					Return(nil, apperr.New(codes.Unavailable, "google maps place details failed")).Once()
			},
			wantErr: apperr.ErrUnavailable,
		},
		{
			name:   "fail with INVALID_ARGUMENT and store nothing when the catalog has no place with the id",
			inputs: []*usecase.DraftEventInput{draftInput("Zepp Haneda", new("ChIJunknown"))},
			setup: func(d *authoringDeps, created *[]*entity.Venue) {
				d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, "ChIJunknown").
					Return(nil, apperr.New(codes.NotFound, "venue not found")).Once()
				d.mapCatalog.EXPECT().GetPlace(mock.Anything, "ChIJunknown").
					Return(nil, apperr.New(codes.NotFound, "no place with this id in google maps")).Once()
			},
			wantErr: apperr.ErrInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			d := newAuthoringDeps(t)
			d.organizerRepo.EXPECT().ListArtists(mock.Anything, authoringOrgID).
				Return([]*entity.Artist{{ID: authoringArtistID}}, nil).Once()

			var created []*entity.Venue
			if tt.setup != nil {
				tt.setup(d, &created)
			}
			var stored []*entity.DraftEvent
			if tt.wantStored {
				d.seriesRepo.EXPECT().CreateDraft(mock.Anything, mock.Anything, mock.Anything, []string{authoringArtistID}).
					Run(func(_ context.Context, _ *entity.Series, events []*entity.DraftEvent, _ []string) { stored = events }).
					Return(nil).Once()
				d.seriesRepo.EXPECT().GetAuthored(mock.Anything, mock.Anything).
					Return(&entity.Series{ID: "series-1"}, nil, nil, nil).Once()
			}

			_, _, _, err := d.uc.Create(ctx, authoringOrgID, newDraftSeries(), tt.inputs, []string{authoringArtistID})

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Len(t, stored, len(tt.wantVenues))
			for i, ev := range stored {
				assert.Equal(t, tt.wantVenues[i], ev.VenueID)
				assert.Equal(t, new(tt.inputs[i].VenueName), ev.ListedVenueName)
			}
			if tt.checkCreate != nil {
				tt.checkCreate(t, created)
			}
		})
	}
}

// TestConcertAuthoringUseCase_UpdateDraft_PickedPlace verifies that UpdateDraft
// applies the picked-place rule of CreateDraft ("find or create each event's
// Venue as CreateDraft does").
func TestConcertAuthoringUseCase_UpdateDraft_PickedPlace(t *testing.T) {
	t.Parallel()

	const seriesID = "series-1"
	draftState := entity.SeriesPublishStateDraft
	existing := &entity.Series{ID: seriesID, OrganizerID: new(authoringOrgID), PublishState: &draftState}

	t.Run("fail with INVALID_ARGUMENT and store nothing when an event gives no place id", func(t *testing.T) {
		t.Parallel()
		d := newAuthoringDeps(t)
		d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(existing, nil).Once()
		d.organizerRepo.EXPECT().ListArtists(mock.Anything, authoringOrgID).
			Return([]*entity.Artist{{ID: authoringArtistID}}, nil).Once()

		_, _, _, err := d.uc.Update(context.Background(), authoringOrgID, seriesID, newDraftSeries(),
			[]*usecase.DraftEventInput{draftInput("Zepp Haneda", nil)}, []string{authoringArtistID})

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("create a Venue from the catalog's place when no Venue holds the place id", func(t *testing.T) {
		t.Parallel()
		d := newAuthoringDeps(t)
		d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(existing, nil).Once()
		d.organizerRepo.EXPECT().ListArtists(mock.Anything, authoringOrgID).
			Return([]*entity.Artist{{ID: authoringArtistID}}, nil).Once()
		d.venueRepo.EXPECT().GetByPlaceID(mock.Anything, zeppHanedaPlaceID).
			Return(nil, apperr.New(codes.NotFound, "venue not found")).Once()
		d.mapCatalog.EXPECT().GetPlace(mock.Anything, zeppHanedaPlaceID).Return(&entity.MapPlace{
			PlaceID: zeppHanedaPlaceID, Name: "Zepp Haneda (TOKYO)", AdminArea: new("JP-13"),
		}, nil).Once()
		d.venueRepo.EXPECT().Create(mock.Anything, mock.MatchedBy(func(v *entity.Venue) bool {
			return *v.GooglePlaceID == zeppHanedaPlaceID && *v.AdminArea == "JP-13" && v.Name == "Zepp Haneda (TOKYO)"
		})).Return(createdVenueID, nil).Once()
		var stored []*entity.DraftEvent
		d.seriesRepo.EXPECT().UpdateDraft(mock.Anything, mock.Anything, mock.Anything, []string{authoringArtistID}).
			Run(func(_ context.Context, _ *entity.Series, events []*entity.DraftEvent, _ []string) { stored = events }).
			Return(nil).Once()
		d.seriesRepo.EXPECT().GetAuthored(mock.Anything, seriesID).Return(existing, nil, nil, nil).Once()

		_, _, _, err := d.uc.Update(context.Background(), authoringOrgID, seriesID, newDraftSeries(),
			[]*usecase.DraftEventInput{draftInput("Zepp Haneda", new(zeppHanedaPlaceID))}, []string{authoringArtistID})

		require.NoError(t, err)
		require.Len(t, stored, 1)
		assert.Equal(t, createdVenueID, stored[0].VenueID)
	})
}
