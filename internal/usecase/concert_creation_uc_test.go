package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- test doubles ---

type fakeVenueRepo struct {
	venues  map[string]*entity.Venue
	created []*entity.Venue
}

func newFakeVenueRepo() *fakeVenueRepo {
	return &fakeVenueRepo{venues: make(map[string]*entity.Venue)}
}

func (r *fakeVenueRepo) Create(_ context.Context, v *entity.Venue) error {
	r.venues[v.Name] = v
	r.created = append(r.created, v)
	return nil
}

func (r *fakeVenueRepo) Get(_ context.Context, id string) (*entity.Venue, error) {
	for _, v := range r.venues {
		if v.ID == id {
			return v, nil
		}
	}
	return nil, apperr.New(codes.NotFound, "venue not found")
}

func (r *fakeVenueRepo) GetByName(_ context.Context, name string) (*entity.Venue, error) {
	if v, ok := r.venues[name]; ok {
		return v, nil
	}
	return nil, apperr.New(codes.NotFound, "venue not found")
}

type fakeConcertRepo struct {
	created []*entity.Concert
}

func (r *fakeConcertRepo) ListByArtist(_ context.Context, _ string, _ bool) ([]*entity.Concert, error) {
	return nil, nil
}

func (r *fakeConcertRepo) ListByFollower(_ context.Context, _ string) ([]*entity.Concert, error) {
	return nil, nil
}

func (r *fakeConcertRepo) Create(_ context.Context, concerts ...*entity.Concert) error {
	r.created = append(r.created, concerts...)
	return nil
}

// --- helpers ---

func newGoChannelPub(t *testing.T) *gochannel.GoChannel {
	t.Helper()
	return gochannel.NewGoChannel(gochannel.Config{OutputChannelBuffer: 256}, watermill.NopLogger{})
}

// --- tests ---

func TestConcertCreationUseCase_CreateFromDiscovered(t *testing.T) {
	localDate := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	startTime := time.Date(2026, 3, 15, 19, 0, 0, 0, time.UTC)

	t.Run("creates venues and concerts", func(t *testing.T) {
		venueRepo := newFakeVenueRepo()
		concertRepo := &fakeConcertRepo{}
		pub := newGoChannelPub(t)
		uc := usecase.NewConcertCreationUseCase(venueRepo, concertRepo, pub, newTestLogger(t))

		data := messaging.ConcertDiscoveredData{
			ArtistID:   "artist-1",
			ArtistName: "Test Artist",
			Concerts: []messaging.ScrapedConcertData{
				{
					Title:           "Concert A",
					ListedVenueName: "Venue X",
					LocalDate:       localDate,
					StartTime:       &startTime,
					SourceURL:       "https://example.com/a",
				},
				{
					Title:           "Concert B",
					ListedVenueName: "Venue Y",
					LocalDate:       localDate,
					SourceURL:       "https://example.com/b",
				},
			},
		}

		err := uc.CreateFromDiscovered(context.Background(), data)
		require.NoError(t, err)

		// Two new venues should be created.
		assert.Len(t, venueRepo.created, 2)

		// Two concerts should be created.
		assert.Len(t, concertRepo.created, 2)
		assert.Equal(t, "artist-1", concertRepo.created[0].ArtistID)
		assert.Equal(t, "Concert A", concertRepo.created[0].Title)
	})

	t.Run("reuses existing venue", func(t *testing.T) {
		venueRepo := newFakeVenueRepo()
		venueRepo.venues["Existing Venue"] = &entity.Venue{ID: "existing-venue-id", Name: "Existing Venue"}
		concertRepo := &fakeConcertRepo{}
		pub := newGoChannelPub(t)
		uc := usecase.NewConcertCreationUseCase(venueRepo, concertRepo, pub, newTestLogger(t))

		data := messaging.ConcertDiscoveredData{
			ArtistID:   "artist-2",
			ArtistName: "Another Artist",
			Concerts: []messaging.ScrapedConcertData{
				{
					Title:           "Concert C",
					ListedVenueName: "Existing Venue",
					LocalDate:       localDate,
					SourceURL:       "https://example.com/c",
				},
			},
		}

		err := uc.CreateFromDiscovered(context.Background(), data)
		require.NoError(t, err)

		// No new venues should be created.
		assert.Empty(t, venueRepo.created)

		// Concert should reference the existing venue.
		require.Len(t, concertRepo.created, 1)
		assert.Equal(t, "existing-venue-id", concertRepo.created[0].VenueID)
	})

	t.Run("deduplicates venues within batch", func(t *testing.T) {
		venueRepo := newFakeVenueRepo()
		concertRepo := &fakeConcertRepo{}
		pub := newGoChannelPub(t)
		uc := usecase.NewConcertCreationUseCase(venueRepo, concertRepo, pub, newTestLogger(t))

		data := messaging.ConcertDiscoveredData{
			ArtistID:   "artist-3",
			ArtistName: "Third Artist",
			Concerts: []messaging.ScrapedConcertData{
				{
					Title:           "Show 1",
					ListedVenueName: "Same Venue",
					LocalDate:       localDate,
					SourceURL:       "https://example.com/1",
				},
				{
					Title:           "Show 2",
					ListedVenueName: "Same Venue",
					LocalDate:       localDate.AddDate(0, 0, 1),
					SourceURL:       "https://example.com/2",
				},
			},
		}

		err := uc.CreateFromDiscovered(context.Background(), data)
		require.NoError(t, err)

		// Only one venue should be created for "Same Venue".
		assert.Len(t, venueRepo.created, 1)

		// Both concerts should reference the same venue ID.
		require.Len(t, concertRepo.created, 2)
		assert.Equal(t, concertRepo.created[0].VenueID, concertRepo.created[1].VenueID)
	})
}
