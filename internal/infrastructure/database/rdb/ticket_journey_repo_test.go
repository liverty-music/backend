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
func seedJourneyDeps(t *testing.T, db *rdb.Database) (userID, eventID string) {
	t.Helper()
	userID = seedUser(t, db, "journey-user", "journey@test.com", "ext-journey-01")
	artistID := seedArtist(t, db, "journey-artist", "jj000000-0000-0000-0000-000000jrn001")
	venueID := seedVenue(t, db, "journey-venue")
	eventID = seedEvent(t, db, venueID, artistID, "journey-event", "2026-06-01")
	return userID, eventID
}

func TestTicketJourneyRepository_Upsert(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewTicketJourneyRepository(db, testOutbox)
	ctx := context.Background()

	tests := []struct {
		name  string
		setup func() *entity.TicketJourney
		check func(t *testing.T, userID string)
	}{
		{
			name: "creates new journey",
			setup: func() *entity.TicketJourney {
				cleanTables(t, db)
				userID, eventID := seedJourneyDeps(t, db)
				return &entity.TicketJourney{
					UserID:      userID,
					EventID:     eventID,
					Status:      entity.TicketJourneyStatusTracking,
					StatusSetBy: entity.TicketJourneySetByFan,
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
				cleanTables(t, db)
				userID, eventID := seedJourneyDeps(t, db)

				err := repo.Upsert(ctx, &entity.TicketJourney{
					UserID:      userID,
					EventID:     eventID,
					Status:      entity.TicketJourneyStatusTracking,
					StatusSetBy: entity.TicketJourneySetByFan,
				})
				require.NoError(t, err)

				return &entity.TicketJourney{
					UserID:      userID,
					EventID:     eventID,
					Status:      entity.TicketJourneyStatusApplied,
					StatusSetBy: entity.TicketJourneySetByFan,
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
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewTicketJourneyRepository(db, testOutbox)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func() (userID, eventID string)
		wantErr error
	}{
		{
			name: "deletes existing journey",
			setup: func() (userID, eventID string) {
				cleanTables(t, db)
				userID, eventID = seedJourneyDeps(t, db)
				err := repo.Upsert(ctx, &entity.TicketJourney{
					UserID:      userID,
					EventID:     eventID,
					Status:      entity.TicketJourneyStatusTracking,
					StatusSetBy: entity.TicketJourneySetByFan,
				})
				require.NoError(t, err)
				return userID, eventID
			},
			wantErr: nil,
		},
		{
			name: "deleting non-existent journey is idempotent",
			setup: func() (userID, eventID string) {
				cleanTables(t, db)
				return seedJourneyDeps(t, db)
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
	t.Parallel()
	db := newTestDB(t)

	repo := rdb.NewTicketJourneyRepository(db, testOutbox)
	ctx := context.Background()

	// day returns the date offset from today, so "upcoming" and "past" hold
	// whenever the test runs.
	day := func(offset int) string {
		return entity.CurrentDateAtVenue(time.Now()).AddDate(0, 0, offset).Format(time.DateOnly)
	}
	track := func(t *testing.T, userID, eventID string, status entity.TicketJourneyStatus) {
		t.Helper()
		require.NoError(t, repo.Upsert(ctx, &entity.TicketJourney{UserID: userID, EventID: eventID, Status: status, StatusSetBy: entity.TicketJourneySetByFan}))
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
		cleanTables(t, db)

		artistID := seedArtist(t, db, "track-artist", "11112222-3333-4444-5555-666677778888")
		venueID := seedVenue(t, db, "track-venue")
		seriesID := seedSeriesOnly(t, db, "TrackTour")
		eventA := seedEventForSeries(t, db, seriesID, venueID, artistID, day(10))
		eventB := seedEventForSeries(t, db, seriesID, venueID, artistID, day(11))

		otherSeriesID := seedSeriesOnly(t, db, "OtherTour")
		otherEvent := seedEventForSeries(t, db, otherSeriesID, venueID, artistID, day(20))

		fanA := seedUser(t, db, "track-a", "track-a@test.com", "ext-track-a")
		fanB := seedUser(t, db, "track-b", "track-b@test.com", "ext-track-b")
		fanC := seedUser(t, db, "applied", "applied@test.com", "ext-applied")
		fanD := seedUser(t, db, "other-series", "other@test.com", "ext-other")

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
		cleanTables(t, db)

		artistID := seedArtist(t, db, "track-artist", "11112222-3333-4444-5555-666677778888")
		venueID := seedVenue(t, db, "track-venue")
		seriesID := seedSeriesOnly(t, db, "TrackTour")
		past := seedEventForSeries(t, db, seriesID, venueID, artistID, day(-30))
		_ = seedEventForSeries(t, db, seriesID, venueID, artistID, day(14)) // earlier, untracked
		nov1 := seedEventForSeries(t, db, seriesID, venueID, artistID, day(26))
		nov15 := seedEventForSeries(t, db, seriesID, venueID, artistID, day(40))

		fanA := seedUser(t, db, "track-a", "track-a@test.com", "ext-track-a")
		track(t, fanA, nov15, entity.TicketJourneyStatusTracking)
		track(t, fanA, past, entity.TicketJourneyStatusTracking)
		track(t, fanA, nov1, entity.TicketJourneyStatusTracking)

		trackers, err := repo.ListUserIDsTrackingSeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{fanA: nov1}, byUser(trackers))
	})

	// @spec components/entity/ticket-journey/list-user-ids-tracking-series "Every tracked event is past"
	t.Run("Every tracked event is past", func(t *testing.T) {
		cleanTables(t, db)

		artistID := seedArtist(t, db, "track-artist", "11112222-3333-4444-5555-666677778888")
		venueID := seedVenue(t, db, "track-venue")
		seriesID := seedSeriesOnly(t, db, "TrackTour")
		sep1 := seedEventForSeries(t, db, seriesID, venueID, artistID, day(-35))
		sep5 := seedEventForSeries(t, db, seriesID, venueID, artistID, day(-31))

		fanA := seedUser(t, db, "track-a", "track-a@test.com", "ext-track-a")
		track(t, fanA, sep5, entity.TicketJourneyStatusTracking)
		track(t, fanA, sep1, entity.TicketJourneyStatusTracking)

		trackers, err := repo.ListUserIDsTrackingSeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{fanA: sep1}, byUser(trackers))
	})

	t.Run("an event today counts as upcoming", func(t *testing.T) {
		cleanTables(t, db)

		artistID := seedArtist(t, db, "track-artist", "11112222-3333-4444-5555-666677778888")
		venueID := seedVenue(t, db, "track-venue")
		seriesID := seedSeriesOnly(t, db, "TrackTour")
		past := seedEventForSeries(t, db, seriesID, venueID, artistID, day(-3))
		today := seedEventForSeries(t, db, seriesID, venueID, artistID, day(0))

		fanA := seedUser(t, db, "track-a", "track-a@test.com", "ext-track-a")
		track(t, fanA, past, entity.TicketJourneyStatusTracking)
		track(t, fanA, today, entity.TicketJourneyStatusTracking)

		trackers, err := repo.ListUserIDsTrackingSeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{fanA: today}, byUser(trackers))
	})

	// @spec components/entity/ticket-journey/list-user-ids-tracking-series "Nobody is tracking"
	t.Run("Nobody is tracking", func(t *testing.T) {
		cleanTables(t, db)
		seriesID := seedSeriesOnly(t, db, "EmptyTrackTour")

		trackers, err := repo.ListUserIDsTrackingSeries(ctx, seriesID)
		require.NoError(t, err)
		assert.Empty(t, trackers)
	})
}

func TestTicketJourneyRepository_ListByUser(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewTicketJourneyRepository(db, testOutbox)
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
				cleanTables(t, db)
				return seedUser(t, db, "empty-journey-user", "empty-journey@test.com", "ext-empty-jrn-01")
			},
			wantCount: 0,
			wantErr:   nil,
		},
		{
			name: "returns multiple journeys",
			setup: func() string {
				cleanTables(t, db)
				userID, eventID1 := seedJourneyDeps(t, db)

				artistID2 := seedArtist(t, db, "journey-artist-2", "jj000000-0000-0000-0000-000000jrn002")
				venueID2 := seedVenue(t, db, "journey-venue-2")
				eventID2 := seedEvent(t, db, venueID2, artistID2, "journey-event-2", "2026-07-01")

				err := repo.Upsert(ctx, &entity.TicketJourney{
					UserID:      userID,
					EventID:     eventID1,
					Status:      entity.TicketJourneyStatusTracking,
					StatusSetBy: entity.TicketJourneySetByFan,
				})
				require.NoError(t, err)

				err = repo.Upsert(ctx, &entity.TicketJourney{
					UserID:      userID,
					EventID:     eventID2,
					Status:      entity.TicketJourneyStatusApplied,
					StatusSetBy: entity.TicketJourneySetByFan,
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

func TestTicketJourneyRepository_ListUserIDsTrackingEvents(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	repo := rdb.NewTicketJourneyRepository(db, testOutbox)
	ctx := context.Background()

	day := func(offset int) string {
		return entity.CurrentDateAtVenue(time.Now()).AddDate(0, 0, offset).Format(time.DateOnly)
	}
	track := func(t *testing.T, userID, eventID string, status entity.TicketJourneyStatus) {
		t.Helper()
		require.NoError(t, repo.Upsert(ctx, &entity.TicketJourney{UserID: userID, EventID: eventID, Status: status, StatusSetBy: entity.TicketJourneySetByFan}))
	}
	byUser := func(trackers []*entity.SeriesTracker) map[string]string {
		out := make(map[string]string, len(trackers))
		for _, tr := range trackers {
			out[tr.UserID] = tr.EventID
		}
		return out
	}

	artistID := seedArtist(t, db, "events-artist", "11112222-3333-4444-5555-666677779999")
	venueID := seedVenue(t, db, "events-venue")
	seriesID := seedSeriesOnly(t, db, "EventsTour")
	oct20 := seedEventForSeries(t, db, seriesID, venueID, artistID, day(7))  // not given
	nov1 := seedEventForSeries(t, db, seriesID, venueID, artistID, day(19))  // given
	nov15 := seedEventForSeries(t, db, seriesID, venueID, artistID, day(33)) // given

	fanA := seedUser(t, db, "events-a", "events-a@test.com", "ext-events-a")
	fanB := seedUser(t, db, "events-b", "events-b@test.com", "ext-events-b")
	fanC := seedUser(t, db, "events-c", "events-c@test.com", "ext-events-c")
	fanD := seedUser(t, db, "events-d", "events-d@test.com", "ext-events-d")

	track(t, fanA, oct20, entity.TicketJourneyStatusTracking)
	track(t, fanA, nov1, entity.TicketJourneyStatusTracking)
	track(t, fanA, nov15, entity.TicketJourneyStatusTracking)
	track(t, fanB, nov15, entity.TicketJourneyStatusTracking)
	track(t, fanC, oct20, entity.TicketJourneyStatusTracking)
	track(t, fanD, nov1, entity.TicketJourneyStatusApplied)

	trackers, err := repo.ListUserIDsTrackingEvents(ctx, []string{nov1, nov15})
	require.NoError(t, err)
	got := byUser(trackers)

	t.Run("fans tracking offered events", func(t *testing.T) {
		// @spec components/entity/ticket-journey/list-user-ids-tracking-events "Fans tracking offered events"
		assert.Len(t, trackers, 2, "each fan appears once")
		assert.Contains(t, got, fanA)
		assert.Contains(t, got, fanB)
	})

	t.Run("linked event is among the given events", func(t *testing.T) {
		// @spec components/entity/ticket-journey/list-user-ids-tracking-events "Linked event is among the given events"
		assert.Equal(t, nov1, got[fanA], "the earlier 20 October event is not given")
		assert.Equal(t, nov15, got[fanB])
	})

	t.Run("tracking only an event not given", func(t *testing.T) {
		// @spec components/entity/ticket-journey/list-user-ids-tracking-events "Tracking only an event not given"
		assert.NotContains(t, got, fanC)
	})

	t.Run("a fan past the tracking stage", func(t *testing.T) {
		// @spec components/entity/ticket-journey/list-user-ids-tracking-events "A fan past the Tracking stage"
		assert.NotContains(t, got, fanD)
	})

	t.Run("no event given", func(t *testing.T) {
		// @spec components/entity/ticket-journey/list-user-ids-tracking-events "No event given"
		none, err := repo.ListUserIDsTrackingEvents(ctx, nil)
		require.NoError(t, err)
		assert.Empty(t, none)
	})
}
