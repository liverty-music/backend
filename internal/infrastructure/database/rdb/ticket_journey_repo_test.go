package rdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedJourneyDeps inserts the FK dependencies required by the ticket_journeys table
// and returns (userID, eventID).
func seedJourneyDeps(t *testing.T) (userID, eventID string) {
	t.Helper()
	userID = seedUser(t, "journey-user", "journey@test.com", "ext-journey-01")
	artistID := seedArtist(t, "journey-artist", "jj000000-0000-0000-0000-000000jrn001")
	venueID := seedVenue(t, "journey-venue")
	eventID = seedEvent(t, venueID, artistID, "journey-event", "2026-06-01")
	return userID, eventID
}

func TestTicketJourneyRepository_Upsert(t *testing.T) {
	repo := rdb.NewTicketJourneyRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name  string
		setup func() *entity.TicketJourney
		check func(t *testing.T, userID string)
	}{
		{
			name: "creates new journey",
			setup: func() *entity.TicketJourney {
				cleanDatabase(t)
				userID, eventID := seedJourneyDeps(t)
				return &entity.TicketJourney{
					UserID:  userID,
					EventID: eventID,
					Status:  entity.TicketJourneyStatusTracking,
				}
			},
			check: func(t *testing.T, userID string) {
				t.Helper()
				journeys, err := repo.ListByUser(ctx, userID)
				require.NoError(t, err)
				require.Len(t, journeys, 1)
				assert.Equal(t, entity.TicketJourneyStatusTracking, journeys[0].Status)
			},
		},
		{
			name: "updates status on conflict",
			setup: func() *entity.TicketJourney {
				cleanDatabase(t)
				userID, eventID := seedJourneyDeps(t)

				err := repo.Upsert(ctx, &entity.TicketJourney{
					UserID:  userID,
					EventID: eventID,
					Status:  entity.TicketJourneyStatusTracking,
				})
				require.NoError(t, err)

				return &entity.TicketJourney{
					UserID:  userID,
					EventID: eventID,
					Status:  entity.TicketJourneyStatusApplied,
				}
			},
			check: func(t *testing.T, userID string) {
				t.Helper()
				journeys, err := repo.ListByUser(ctx, userID)
				require.NoError(t, err)
				require.Len(t, journeys, 1)
				assert.Equal(t, entity.TicketJourneyStatusApplied, journeys[0].Status)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			journey := tt.setup()

			err := repo.Upsert(ctx, journey)

			require.NoError(t, err)
			tt.check(t, journey.UserID)
		})
	}
}

func TestTicketJourneyRepository_Delete(t *testing.T) {
	repo := rdb.NewTicketJourneyRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func() (userID, eventID string)
		wantErr error
	}{
		{
			name: "deletes existing journey",
			setup: func() (userID, eventID string) {
				cleanDatabase(t)
				userID, eventID = seedJourneyDeps(t)
				err := repo.Upsert(ctx, &entity.TicketJourney{
					UserID:  userID,
					EventID: eventID,
					Status:  entity.TicketJourneyStatusTracking,
				})
				require.NoError(t, err)
				return userID, eventID
			},
			wantErr: nil,
		},
		{
			name: "deleting non-existent journey is idempotent",
			setup: func() (userID, eventID string) {
				cleanDatabase(t)
				return seedJourneyDeps(t)
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID, eventID := tt.setup()

			err := repo.Delete(ctx, userID, eventID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)

			journeys, err := repo.ListByUser(ctx, userID)
			require.NoError(t, err)
			assert.Empty(t, journeys)
		})
	}
}

func TestTicketJourneyRepository_ListUserIDsTrackingSeries(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	repo := rdb.NewTicketJourneyRepository(testDB)
	ctx := context.Background()

	// day returns the date offset from today, so "upcoming" and "past" hold
	// whenever the test runs.
	day := func(offset int) string {
		return time.Now().AddDate(0, 0, offset).Format(time.DateOnly)
	}
	track := func(t *testing.T, userID, eventID string, status entity.TicketJourneyStatus) {
		t.Helper()
		require.NoError(t, repo.Upsert(ctx, &entity.TicketJourney{UserID: userID, EventID: eventID, Status: status}))
	}
	byUser := func(trackers []*entity.SeriesTracker) map[string]string {
		out := make(map[string]string, len(trackers))
		for _, tr := range trackers {
			out[tr.UserID] = tr.EventID
		}
		return out
	}

	// @spec components/entity/ticket-journey/list-user-ids-tracking-series "Fans tracking events of the series"
	// @spec components/entity/ticket-journey/list-user-ids-tracking-series "A fan past the Tracking stage"
	// @spec components/entity/ticket-journey/list-user-ids-tracking-series "Tracking an event of another series"
	t.Run("Fans tracking events of the series", func(t *testing.T) {
		cleanDatabase(t)

		artistID := seedArtist(t, "track-artist", "11112222-3333-4444-5555-666677778888")
		venueID := seedVenue(t, "track-venue")
		seriesID := seedSeriesOnly(t, "TrackTour")
		eventA := seedEventForSeries(t, seriesID, venueID, artistID, day(10))
		eventB := seedEventForSeries(t, seriesID, venueID, artistID, day(11))

		otherSeriesID := seedSeriesOnly(t, "OtherTour")
		otherEvent := seedEventForSeries(t, otherSeriesID, venueID, artistID, day(20))

		fanA := seedUser(t, "track-a", "track-a@test.com", "ext-track-a")
		fanB := seedUser(t, "track-b", "track-b@test.com", "ext-track-b")
		fanC := seedUser(t, "applied", "applied@test.com", "ext-applied")
		fanD := seedUser(t, "other-series", "other@test.com", "ext-other")

		track(t, fanA, eventA, entity.TicketJourneyStatusTracking)
		track(t, fanA, eventB, entity.TicketJourneyStatusTracking)
		track(t, fanB, eventB, entity.TicketJourneyStatusTracking)
		track(t, fanC, eventA, entity.TicketJourneyStatusApplied)
		track(t, fanD, otherEvent, entity.TicketJourneyStatusTracking)

		trackers, err := repo.ListUserIDsTrackingSeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Len(t, trackers, 2, "each fan appears once")
		assert.Equal(t, map[string]string{fanA: eventA, fanB: eventB}, byUser(trackers))
	})

	// @spec components/entity/ticket-journey/list-user-ids-tracking-series "Linked event is the earliest upcoming tracked event"
	t.Run("Linked event is the earliest upcoming tracked event", func(t *testing.T) {
		cleanDatabase(t)

		artistID := seedArtist(t, "track-artist", "11112222-3333-4444-5555-666677778888")
		venueID := seedVenue(t, "track-venue")
		seriesID := seedSeriesOnly(t, "TrackTour")
		past := seedEventForSeries(t, seriesID, venueID, artistID, day(-30))
		_ = seedEventForSeries(t, seriesID, venueID, artistID, day(14)) // earlier, untracked
		nov1 := seedEventForSeries(t, seriesID, venueID, artistID, day(26))
		nov15 := seedEventForSeries(t, seriesID, venueID, artistID, day(40))

		fanA := seedUser(t, "track-a", "track-a@test.com", "ext-track-a")
		track(t, fanA, nov15, entity.TicketJourneyStatusTracking)
		track(t, fanA, past, entity.TicketJourneyStatusTracking)
		track(t, fanA, nov1, entity.TicketJourneyStatusTracking)

		trackers, err := repo.ListUserIDsTrackingSeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{fanA: nov1}, byUser(trackers))
	})

	// @spec components/entity/ticket-journey/list-user-ids-tracking-series "Every tracked event is past"
	t.Run("Every tracked event is past", func(t *testing.T) {
		cleanDatabase(t)

		artistID := seedArtist(t, "track-artist", "11112222-3333-4444-5555-666677778888")
		venueID := seedVenue(t, "track-venue")
		seriesID := seedSeriesOnly(t, "TrackTour")
		sep1 := seedEventForSeries(t, seriesID, venueID, artistID, day(-35))
		sep5 := seedEventForSeries(t, seriesID, venueID, artistID, day(-31))

		fanA := seedUser(t, "track-a", "track-a@test.com", "ext-track-a")
		track(t, fanA, sep5, entity.TicketJourneyStatusTracking)
		track(t, fanA, sep1, entity.TicketJourneyStatusTracking)

		trackers, err := repo.ListUserIDsTrackingSeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{fanA: sep1}, byUser(trackers))
	})

	t.Run("an event today counts as upcoming", func(t *testing.T) {
		cleanDatabase(t)

		artistID := seedArtist(t, "track-artist", "11112222-3333-4444-5555-666677778888")
		venueID := seedVenue(t, "track-venue")
		seriesID := seedSeriesOnly(t, "TrackTour")
		past := seedEventForSeries(t, seriesID, venueID, artistID, day(-3))
		today := seedEventForSeries(t, seriesID, venueID, artistID, day(0))

		fanA := seedUser(t, "track-a", "track-a@test.com", "ext-track-a")
		track(t, fanA, past, entity.TicketJourneyStatusTracking)
		track(t, fanA, today, entity.TicketJourneyStatusTracking)

		trackers, err := repo.ListUserIDsTrackingSeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{fanA: today}, byUser(trackers))
	})

	// @spec components/entity/ticket-journey/list-user-ids-tracking-series "Nobody is tracking"
	t.Run("Nobody is tracking", func(t *testing.T) {
		cleanDatabase(t)
		seriesID := seedSeriesOnly(t, "EmptyTrackTour")

		trackers, err := repo.ListUserIDsTrackingSeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Empty(t, trackers)
	})
}

func TestTicketJourneyRepository_ListByUser(t *testing.T) {
	repo := rdb.NewTicketJourneyRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name      string
		setup     func() string // returns userID
		wantCount int
		wantErr   error
	}{
		{
			name: "returns empty for user with no journeys",
			setup: func() string {
				cleanDatabase(t)
				return seedUser(t, "empty-journey-user", "empty-journey@test.com", "ext-empty-jrn-01")
			},
			wantCount: 0,
			wantErr:   nil,
		},
		{
			name: "returns multiple journeys",
			setup: func() string {
				cleanDatabase(t)
				userID, eventID1 := seedJourneyDeps(t)

				artistID2 := seedArtist(t, "journey-artist-2", "jj000000-0000-0000-0000-000000jrn002")
				venueID2 := seedVenue(t, "journey-venue-2")
				eventID2 := seedEvent(t, venueID2, artistID2, "journey-event-2", "2026-07-01")

				err := repo.Upsert(ctx, &entity.TicketJourney{
					UserID:  userID,
					EventID: eventID1,
					Status:  entity.TicketJourneyStatusTracking,
				})
				require.NoError(t, err)

				err = repo.Upsert(ctx, &entity.TicketJourney{
					UserID:  userID,
					EventID: eventID2,
					Status:  entity.TicketJourneyStatusApplied,
				})
				require.NoError(t, err)

				return userID
			},
			wantCount: 2,
			wantErr:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID := tt.setup()

			got, err := repo.ListByUser(ctx, userID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Len(t, got, tt.wantCount)
		})
	}
}
