package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

// testSearchTime is the search time the ConcertSearch mock returns from Upsert.
var testSearchTime = time.Date(2026, 5, 1, 9, 30, 0, 0, time.UTC)

// searchedArtist is an Artist with everything a search needs.
func searchedArtist() *entity.Artist {
	return &entity.Artist{ID: "artist-1", Name: "Test Artist", MBID: "11111111-1111-1111-1111-111111111111"}
}

// firstAttempt is the first attempt of a search of artist requested now.
func firstAttempt(artist *entity.Artist, trigger usecase.SearchTrigger) usecase.ConcertSearchAttempt {
	return usecase.ConcertSearchAttempt{ArtistID: artist.ID, SearchTime: time.Now(), Trigger: trigger, Attempt: 1}
}

// expectCheck sets up an attempt's check: the ConcertSearch is pending with
// the attempt's search time and the Artist is read.
func expectCheck(d *concertTestDeps, artist *entity.Artist, in usecase.ConcertSearchAttempt) {
	d.concertSearchRepo.EXPECT().GetByArtistID(mock.Anything, artist.ID).Return(&entity.ConcertSearch{
		ArtistID: artist.ID, SearchTime: in.SearchTime, Status: entity.ConcertSearchStatusPending,
	}, nil).Once()
	d.artistRepo.EXPECT().Get(mock.Anything, artist.ID).Return(artist, nil).Once()
}

// expectReads sets up what an attempt reads before it calls the search: the
// artist without an official site, the existing upcoming concerts and no
// pending candidate.
func expectReads(d *concertTestDeps, artist *entity.Artist, existing []*entity.Concert) {
	d.artistRepo.EXPECT().Get(mock.Anything, artist.ID).Return(artist, nil).Once()
	d.artistRepo.EXPECT().GetOfficialSite(mock.Anything, artist.ID).Return(nil, apperr.ErrNotFound).Once()
	d.concertRepo.EXPECT().ListUpcomingByArtist(mock.Anything, artist.ID, mock.Anything).Return(existing, nil).Once()
	d.concertCandidateRepo.EXPECT().ListPendingDedupKeysByArtist(mock.Anything, artist.ID).Return(nil, nil).Once()
}

// expectSearch sets up an attempt up to the external search, which answers
// with found and err.
func expectSearch(d *concertTestDeps, artist *entity.Artist, in usecase.ConcertSearchAttempt, found []*entity.DiscoveredSeries, err error) {
	expectCheck(d, artist, in)
	expectReads(d, artist, nil)
	d.searcher.EXPECT().Search(mock.Anything, artist, (*entity.OfficialSite)(nil), mock.Anything).Return(found, err).Once()
}

// expectStatus expects the search to end with status.
func expectStatus(d *concertTestDeps, artistID string, status entity.ConcertSearchStatus) {
	d.concertSearchRepo.EXPECT().UpdateStatus(mock.Anything, artistID, mock.Anything, status).Return(nil).Once()
}

// runAttempt runs one search-concerts run and returns what it started.
func runAttempt(t *testing.T, d *concertTestDeps, in usecase.ConcertSearchAttempt) *inlineFlow {
	t.Helper()
	f := &inlineFlow{}
	_, err := d.uc.RunSearch(context.Background(), f, in)
	require.NoError(t, err)
	return f
}

// handedOn returns the number of events handed to the create-from-discovered
// workflows f started.
func handedOn(t *testing.T, f *inlineFlow) int {
	t.Helper()
	n := 0
	for _, start := range f.starts {
		require.Equal(t, usecase.WorkflowCreateFromDiscovered, start.workflow)
		data, ok := start.input.(entity.ConcertDiscoveredData)
		require.True(t, ok)
		n += data.EventCount()
	}
	return n
}

// newShows are two series, a tour of two dates and a single show.
func newShows() []*entity.DiscoveredSeries {
	day := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	return []*entity.DiscoveredSeries{
		{Title: "Tour", Type: entity.SeriesTypeTour, Events: []*entity.DiscoveredEvent{
			{ListedVenueName: "Zepp Haneda", LocalDate: day},
			{ListedVenueName: "Zepp Namba", LocalDate: day.AddDate(0, 0, 2)},
		}},
		{Title: "Show", Type: entity.SeriesTypeSingle, Events: []*entity.DiscoveredEvent{
			{ListedVenueName: "Budokan", LocalDate: day.AddDate(0, 1, 0)},
		}},
	}
}

func TestConcertUseCase_SearchNewConcerts_Starts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @spec components/usecase/concert/search-new-concerts "Daily run starts searches and ends"
	t.Run("records each due search as pending and starts it without searching", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		var ids []string
		for i := range 80 {
			artist := searchedArtist()
			artist.ID = fmt.Sprintf("artist-%d", i)
			ids = append(ids, artist.ID)
			d.artistRepo.EXPECT().Get(ctx, artist.ID).Return(artist, nil).Once()
			d.concertSearchRepo.EXPECT().GetByArtistID(ctx, artist.ID).Return(nil, apperr.ErrNotFound).Once()
			d.concertSearchRepo.EXPECT().Upsert(ctx, artist.ID, entity.ConcertSearchStatusPending).Return(testSearchTime, nil).Once()
		}

		// The searcher mock has no expectation: no external search runs here.
		for _, id := range ids {
			require.NoError(t, d.uc.SearchNewConcerts(ctx, id, usecase.SearchTriggerDaily))
		}

		started := d.starts()
		require.Len(t, started, 80)
		first := started[0]
		assert.Equal(t, usecase.WorkflowSearchConcerts, first.workflow)
		assert.Equal(t, fmt.Sprintf("search-concerts:artist-0:%d:1", testSearchTime.UnixMicro()), first.id)
		assert.Equal(t, usecase.ConcertSearchAttempt{ArtistID: "artist-0", SearchTime: testSearchTime, Trigger: usecase.SearchTriggerDaily, Attempt: 1}, first.input)
	})

	t.Run("starts the first attempt on the search queue without a deadline or delay", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		workflows := ucmocks.NewMockWorkflows(t)
		artist := searchedArtist()
		var options usecase.StartOptions
		workflows.EXPECT().Start(mock.Anything, usecase.WorkflowSearchConcerts, mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, _, _ string, _ any, opts ...usecase.StartOption) error {
				options = usecase.NewStartOptions(opts...)
				return nil
			}).Once()
		d.artistRepo.EXPECT().Get(ctx, artist.ID).Return(artist, nil).Once()
		d.concertSearchRepo.EXPECT().GetByArtistID(ctx, artist.ID).Return(nil, apperr.ErrNotFound).Once()
		d.concertSearchRepo.EXPECT().Upsert(ctx, artist.ID, entity.ConcertSearchStatusPending).Return(testSearchTime, nil).Once()

		require.NoError(t, newUseCaseWithWorkflows(t, d, workflows, nil).SearchNewConcerts(ctx, artist.ID, usecase.SearchTriggerDaily))

		assert.Equal(t, usecase.StartOptions{Queue: usecase.ConcertSearchQueue}, options)
	})

	// @spec components/usecase/concert/search-new-concerts "Cannot be started"
	t.Run("fails when the background search cannot be started", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		workflows := ucmocks.NewMockWorkflows(t)
		artist := searchedArtist()
		workflows.EXPECT().Start(mock.Anything, usecase.WorkflowSearchConcerts, mock.Anything, mock.Anything, mock.Anything).
			Return(apperr.New(codes.Unavailable, "database down")).Once()
		d.artistRepo.EXPECT().Get(ctx, artist.ID).Return(artist, nil).Once()
		d.concertSearchRepo.EXPECT().GetByArtistID(ctx, artist.ID).Return(nil, apperr.ErrNotFound).Once()
		d.concertSearchRepo.EXPECT().Upsert(ctx, artist.ID, entity.ConcertSearchStatusPending).Return(testSearchTime, nil).Once()

		err := newUseCaseWithWorkflows(t, d, workflows, nil).SearchNewConcerts(ctx, artist.ID, usecase.SearchTriggerDaily)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})

	t.Run("fails without starting when the search cannot be recorded", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		artist := searchedArtist()
		d.artistRepo.EXPECT().Get(ctx, artist.ID).Return(artist, nil).Once()
		d.concertSearchRepo.EXPECT().GetByArtistID(ctx, artist.ID).Return(nil, apperr.ErrNotFound).Once()
		d.concertSearchRepo.EXPECT().Upsert(ctx, artist.ID, entity.ConcertSearchStatusPending).Return(time.Time{}, apperr.ErrInternal).Once()

		err := d.uc.SearchNewConcerts(ctx, artist.ID, usecase.SearchTriggerDaily)

		assert.ErrorIs(t, err, apperr.ErrInternal)
		assert.Empty(t, d.starts())
	})

	// The strict mock fails the test if the ConcertSearch is written.
	// @spec components/usecase/concert/search-new-concerts "Artist without MBID"
	t.Run("fails an artist without an MBID before recording anything", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		artist := searchedArtist()
		artist.MBID = ""
		d.artistRepo.EXPECT().Get(ctx, artist.ID).Return(artist, nil).Once()
		d.concertSearchRepo.EXPECT().GetByArtistID(ctx, artist.ID).Return(nil, apperr.ErrNotFound).Once()

		err := d.uc.SearchNewConcerts(ctx, artist.ID, usecase.SearchTriggerDaily)

		assert.ErrorIs(t, err, apperr.ErrInternal)
		assert.Empty(t, d.starts())
	})

	// @spec components/usecase/concert/search-new-concerts "Represented artist"
	t.Run("an active organizer's artist is not searched", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		artist := searchedArtist()
		organizers := mocks.NewMockOrganizerRepository(t)
		organizers.EXPECT().IsArtistRepresentedByActiveOrganizer(mock.Anything, artist.ID).Return(true, nil).Once()
		d.artistRepo.EXPECT().Get(ctx, artist.ID).Return(artist, nil).Once()

		require.NoError(t, newUseCaseWithWorkflows(t, d, d.workflows, organizers).SearchNewConcerts(ctx, artist.ID, usecase.SearchTriggerDaily))

		assert.Empty(t, d.starts())
	})

	// @spec components/usecase/concert/search-new-concerts "Representation check unavailable"
	t.Run("searches when the representation check fails", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		artist := searchedArtist()
		organizers := mocks.NewMockOrganizerRepository(t)
		organizers.EXPECT().IsArtistRepresentedByActiveOrganizer(mock.Anything, artist.ID).Return(false, apperr.ErrInternal).Once()
		d.artistRepo.EXPECT().Get(ctx, artist.ID).Return(artist, nil).Once()
		d.concertSearchRepo.EXPECT().GetByArtistID(ctx, artist.ID).Return(nil, apperr.ErrNotFound).Once()
		d.concertSearchRepo.EXPECT().Upsert(ctx, artist.ID, entity.ConcertSearchStatusPending).Return(testSearchTime, nil).Once()

		require.NoError(t, newUseCaseWithWorkflows(t, d, d.workflows, organizers).SearchNewConcerts(ctx, artist.ID, usecase.SearchTriggerDaily))

		assert.Len(t, d.starts(), 1)
	})
}

// newUseCaseWithWorkflows is d's use case with workflows and organizers.
func newUseCaseWithWorkflows(t *testing.T, d *concertTestDeps, workflows usecase.Workflows, organizers entity.OrganizerRepository) usecase.ConcertUseCase {
	t.Helper()
	return usecase.NewConcertUseCase(d.artistRepo, d.concertRepo, d.venueRepo, d.seriesRepo, organizers, d.concertSearchRepo, d.concertCandidateRepo,
		d.rejectedConcertRepo, d.searcher, d.areas, workflows, anyConcertMetrics(t), d.analytics, testSearchCacheTTL, testDiscoveryWindow, time.Now, newTestLogger(t))
}

// The skip rules, on a fake clock.
func TestConcertUseCase_SearchNewConcerts_SkipRules(t *testing.T) {
	t.Parallel()

	const day = 24 * time.Hour
	tests := []struct {
		name string
		// searchedAgo and foundAgo are how long ago the Artist's last search
		// started and last found a concert; foundAgo 0 means never.
		status      entity.ConcertSearchStatus
		searchedAgo time.Duration
		foundAgo    time.Duration
		wantSearch  bool
	}{
		// @spec components/usecase/concert/search-new-concerts "Searched 2 hours ago"
		{name: "Searched 2 hours ago", status: entity.ConcertSearchStatusCompleted, searchedAgo: 2 * time.Hour},
		{name: "completed search older than the freshness window", status: entity.ConcertSearchStatusCompleted, searchedAgo: 25 * time.Hour, wantSearch: true},
		// @spec components/usecase/concert/search-new-concerts "New concert found 3 days ago"
		{name: "New concert found 3 days ago", status: entity.ConcertSearchStatusCompleted, searchedAgo: 30 * time.Hour, foundAgo: 3 * day},
		{name: "found longer ago than the discovery window", status: entity.ConcertSearchStatusCompleted, searchedAgo: 15 * day, foundAgo: 15 * day, wantSearch: true},
		// @spec components/usecase/concert/search-new-concerts "Another search running"
		{name: "Another search running", status: entity.ConcertSearchStatusPending, searchedAgo: 5 * time.Hour},
		// @spec components/usecase/concert/search-new-concerts "Abandoned search"
		{name: "Abandoned search", status: entity.ConcertSearchStatusPending, searchedAgo: 21 * time.Hour, wantSearch: true},
		{name: "pending search started 20 hours ago", status: entity.ConcertSearchStatusPending, searchedAgo: 20 * time.Hour, wantSearch: true},
		// @spec components/usecase/concert/search-new-concerts "Previous search failed"
		{name: "Previous search failed", status: entity.ConcertSearchStatusFailed, searchedAgo: time.Minute, wantSearch: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				ctx := context.Background()
				d := newConcertTestDeps(t)
				artist := searchedArtist()
				now := time.Now()
				search := &entity.ConcertSearch{ArtistID: artist.ID, SearchTime: now.Add(-tt.searchedAgo), Status: tt.status}
				if tt.foundAgo > 0 {
					search.LastFindTime = now.Add(-tt.foundAgo)
				}
				d.artistRepo.EXPECT().Get(ctx, artist.ID).Return(artist, nil).Once()
				d.concertSearchRepo.EXPECT().GetByArtistID(ctx, artist.ID).Return(search, nil).Once()
				if tt.wantSearch {
					d.concertSearchRepo.EXPECT().Upsert(ctx, artist.ID, entity.ConcertSearchStatusPending).Return(now, nil).Once()
				}

				require.NoError(t, d.uc.SearchNewConcerts(ctx, artist.ID, usecase.SearchTriggerDaily))

				if tt.wantSearch {
					assert.Len(t, d.starts(), 1)
				} else {
					assert.Empty(t, d.starts())
				}
			})
		})
	}
}

// TestConcertUseCase_SearchNewConcertsOnFirstFollow verifies the discovery
// trigger consumed by the search-first-followed-artist consumer: it searches
// only when the artist has never been searched before (ConcertSearch.GetByArtistID
// NotFound), and is a no-op otherwise.
func TestConcertUseCase_SearchNewConcertsOnFirstFollow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	artist := searchedArtist()

	// @spec components/usecase/concert/search-new-concerts-on-first-follow "Artist never searched"
	t.Run("starts a search of an artist never searched", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		// Read once for the never-searched check and once by SearchNewConcerts.
		d.concertSearchRepo.EXPECT().GetByArtistID(ctx, artist.ID).Return(nil, apperr.ErrNotFound).Twice()
		d.artistRepo.EXPECT().Get(ctx, artist.ID).Return(artist, nil).Once()
		d.concertSearchRepo.EXPECT().Upsert(ctx, artist.ID, entity.ConcertSearchStatusPending).Return(testSearchTime, nil).Once()

		require.NoError(t, d.uc.SearchNewConcertsOnFirstFollow(ctx, artist.ID))

		started := d.starts()
		require.Len(t, started, 1)
		assert.Equal(t, usecase.SearchTriggerFirstFollow, started[0].input.(usecase.ConcertSearchAttempt).Trigger)
	})

	// @spec components/usecase/concert/search-new-concerts-on-first-follow "Artist searched before"
	t.Run("does nothing for an artist searched before", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		d.concertSearchRepo.EXPECT().GetByArtistID(ctx, artist.ID).Return(&entity.ConcertSearch{
			ArtistID: artist.ID, SearchTime: time.Now().Add(-time.Hour), Status: entity.ConcertSearchStatusCompleted,
		}, nil).Once()

		require.NoError(t, d.uc.SearchNewConcertsOnFirstFollow(ctx, artist.ID))

		assert.Empty(t, d.starts())
	})

	// @spec components/usecase/concert/search-new-concerts-on-first-follow "Search history unreadable"
	t.Run("fails when the search history cannot be read", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		d.concertSearchRepo.EXPECT().GetByArtistID(ctx, artist.ID).Return(nil, apperr.ErrInternal).Once()

		assert.ErrorIs(t, d.uc.SearchNewConcertsOnFirstFollow(ctx, artist.ID), apperr.ErrInternal)
	})

	// @spec components/usecase/concert/search-new-concerts-on-first-follow "Search fails"
	t.Run("fails when SearchNewConcerts fails", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		d.concertSearchRepo.EXPECT().GetByArtistID(ctx, artist.ID).Return(nil, apperr.ErrNotFound).Twice()
		d.artistRepo.EXPECT().Get(ctx, artist.ID).Return(artist, nil).Once()
		d.concertSearchRepo.EXPECT().Upsert(ctx, artist.ID, entity.ConcertSearchStatusPending).Return(time.Time{}, apperr.ErrInternal).Once()

		assert.ErrorIs(t, d.uc.SearchNewConcertsOnFirstFollow(ctx, artist.ID), apperr.ErrInternal)
	})
}

// TestSearchNewConcerts_EndedArtist shows that an artist whose activity has
// ended is never searched: the mocks carry no expectation for the search
// history, so any further call fails the test.
func TestSearchNewConcerts_EndedArtist(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ended := &entity.Artist{ID: "artist-ended", Name: "SMAP", MBID: "11111111-1111-1111-1111-111111111111", ActivityEnded: true}

	// @spec components/usecase/concert/search-new-concerts "Ended artist"
	t.Run("Ended artist", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		d.artistRepo.EXPECT().Get(mock.Anything, ended.ID).Return(ended, nil).Once()

		require.NoError(t, d.uc.SearchNewConcerts(ctx, ended.ID, usecase.SearchTriggerManual))

		assert.Empty(t, d.starts())
	})

	// @spec components/usecase/concert/search-new-concerts "Ended artist in the daily run"
	t.Run("Ended artist in the daily run", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		d.artistRepo.EXPECT().Get(mock.Anything, ended.ID).Return(ended, nil).Once()

		// No error: the daily run (cmd/job/concert-discovery) counts a failure
		// only for an error, so it moves on to the next artist.
		require.NoError(t, d.uc.SearchNewConcerts(ctx, ended.ID, usecase.SearchTriggerDaily))
	})

	// @spec components/usecase/concert/search-new-concerts "Ended artist followed for the first time"
	t.Run("Ended artist followed for the first time", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		d.concertSearchRepo.EXPECT().GetByArtistID(mock.Anything, ended.ID).Return(nil, apperr.ErrNotFound).Once()
		d.artistRepo.EXPECT().Get(mock.Anything, ended.ID).Return(ended, nil).Once()

		require.NoError(t, d.uc.SearchNewConcertsOnFirstFollow(ctx, ended.ID))
	})
}

// searchChain runs the attempts of one search, each after the delay it was
// started with, until none starts another. The search answers every call with
// answer. It returns the delays of the retries and the runs' last flow.
func searchChain(t *testing.T, d *concertTestDeps, artist *entity.Artist, answer func(elapsed time.Duration) ([]*entity.DiscoveredSeries, error)) (delays []time.Duration, last *inlineFlow) {
	t.Helper()
	in := firstAttempt(artist, usecase.SearchTriggerDaily)
	d.concertSearchRepo.EXPECT().GetByArtistID(mock.Anything, artist.ID).Return(&entity.ConcertSearch{
		ArtistID: artist.ID, SearchTime: in.SearchTime, Status: entity.ConcertSearchStatusPending,
	}, nil)
	d.artistRepo.EXPECT().Get(mock.Anything, artist.ID).Return(artist, nil)
	d.artistRepo.EXPECT().GetOfficialSite(mock.Anything, artist.ID).Return(nil, apperr.ErrNotFound)
	d.concertRepo.EXPECT().ListUpcomingByArtist(mock.Anything, artist.ID, mock.Anything).Return(nil, nil)
	d.concertCandidateRepo.EXPECT().ListPendingDedupKeysByArtist(mock.Anything, artist.ID).Return(nil, nil)
	d.searcher.EXPECT().Search(mock.Anything, artist, (*entity.OfficialSite)(nil), mock.Anything).
		RunAndReturn(func(context.Context, *entity.Artist, *entity.OfficialSite, time.Time) ([]*entity.DiscoveredSeries, error) {
			elapsed := time.Since(in.SearchTime)
			require.Less(t, elapsed, 20*time.Hour, "no call 20 hours or more after the search time")
			return answer(elapsed)
		})

	for attempt := in; ; {
		f := runAttempt(t, d, attempt)
		if len(f.starts) == 0 || f.starts[0].workflow != usecase.WorkflowSearchConcerts {
			return delays, f
		}
		require.Len(t, f.starts, 1)
		next := f.starts[0]
		input := next.input.(usecase.ConcertSearchAttempt)
		require.Equal(t, attempt.Attempt+1, input.Attempt)
		require.Equal(t, fmt.Sprintf("search-concerts:%s:%d:%d", artist.ID, in.SearchTime.UnixMicro(), input.Attempt), next.id)
		require.Equal(t, usecase.ConcertSearchQueue, next.options.Queue)
		require.True(t, next.options.Deadline.IsZero())
		delays = append(delays, next.options.Delay)
		time.Sleep(next.options.Delay)
		attempt = input
	}
}

func TestRunSearch_Retries(t *testing.T) {
	t.Parallel()
	busy := apperr.New(codes.Unavailable, "busy")

	// @spec components/usecase/concert/search-new-concerts "Service busy for 3 hours"
	t.Run("retries at doubling intervals of at most an hour, then completes once", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			d := newConcertTestDeps(t)
			artist := searchedArtist()
			expectStatus(d, artist.ID, entity.ConcertSearchStatusCompleted)
			d.concertSearchRepo.EXPECT().MarkFound(mock.Anything, artist.ID).Return(nil).Once()

			delays, last := searchChain(t, d, artist, func(elapsed time.Duration) ([]*entity.DiscoveredSeries, error) {
				if elapsed < 3*time.Hour {
					return nil, busy
				}
				return newShows(), nil
			})

			m := time.Minute
			assert.Equal(t, []time.Duration{m, 2 * m, 4 * m, 8 * m, 16 * m, 32 * m, 60 * m, 60 * m}, delays)
			assert.Equal(t, 3, handedOn(t, last))
			sent := d.signals.sent(usecase.EventConcertSearchCompleted)
			require.Len(t, sent, 1, "retried attempts send no signal of their own")
			assert.Equal(t, "found", sent[0]["outcome"])
		})
	})

	// @spec components/usecase/concert/search-new-concerts "Service busy for a whole day"
	// @spec components/usecase/concert/search-new-concerts "Busy for a whole day"
	t.Run("fails once no further attempt may call within 20 hours", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			d := newConcertTestDeps(t)
			artist := searchedArtist()
			expectStatus(d, artist.ID, entity.ConcertSearchStatusFailed)
			start := time.Now()

			delays, last := searchChain(t, d, artist, func(time.Duration) ([]*entity.DiscoveredSeries, error) {
				return nil, busy
			})

			// 6 doubling waits (63 minutes), then hourly while the next
			// attempt still starts within 20 hours.
			assert.Len(t, delays, 24)
			assert.Equal(t, 19*time.Hour+3*time.Minute, time.Since(start), "the last attempt")
			assert.Empty(t, last.starts)
			assert.Equal(t, []usecase.AnalyticsProperties{{
				"artist_id": artist.ID, "trigger": "daily_run", "outcome": "failed",
				"series_count": 0, "event_count": 0, "error_code": "unavailable",
			}}, d.signals.sent(usecase.EventConcertSearchCompleted))
		})
	})

	// @spec components/usecase/concert/search-new-concerts "Queued until late"
	t.Run("an attempt first able to call 20 hours later does not call and sends no signal on the first attempt", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			d := newConcertTestDeps(t)
			artist := searchedArtist()
			in := firstAttempt(artist, usecase.SearchTriggerDaily)
			time.Sleep(20*time.Hour + 5*time.Minute)
			expectCheck(d, artist, in)
			expectReads(d, artist, nil)
			expectStatus(d, artist.ID, entity.ConcertSearchStatusFailed)

			// The strict searcher mock fails the test if it is called.
			f := runAttempt(t, d, in)

			assert.Empty(t, f.starts)
			assert.Empty(t, d.signals.sent(usecase.EventConcertSearchCompleted))
		})
	})

	t.Run("a late retry ends the search with the last call's error kind", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			d := newConcertTestDeps(t)
			artist := searchedArtist()
			in := firstAttempt(artist, usecase.SearchTriggerDaily)
			in.Attempt, in.LastErrorKind = 5, "deadline_exceeded"
			time.Sleep(21 * time.Hour)
			expectCheck(d, artist, in)
			expectReads(d, artist, nil)
			expectStatus(d, artist.ID, entity.ConcertSearchStatusFailed)

			runAttempt(t, d, in)

			sent := d.signals.sent(usecase.EventConcertSearchCompleted)
			require.Len(t, sent, 1)
			assert.Equal(t, "deadline_exceeded", sent[0]["error_code"])
		})
	})

	// @spec components/usecase/concert/search-new-concerts "Search failed"
	t.Run("a deadline with no time left fails with that kind and no message", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			d := newConcertTestDeps(t)
			artist := searchedArtist()
			in := firstAttempt(artist, usecase.SearchTriggerDaily)
			in.Attempt = 24
			time.Sleep(19*time.Hour + 30*time.Minute)
			expectSearch(d, artist, in, nil, apperr.New(codes.DeadlineExceeded, "caller's context ended during the gemini concert search"))
			expectStatus(d, artist.ID, entity.ConcertSearchStatusFailed)

			f := runAttempt(t, d, in)

			assert.Empty(t, f.starts)
			sent := d.signals.sent(usecase.EventConcertSearchCompleted)
			require.Len(t, sent, 1)
			assert.Equal(t, "failed", sent[0]["outcome"])
			assert.Equal(t, "deadline_exceeded", sent[0]["error_code"])
			for key, value := range sent[0] {
				assert.NotContains(t, fmt.Sprint(value), "caller's context", "property %s carries the message", key)
			}
		})
	})

	t.Run("fails the run when the next attempt cannot be started", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		artist := searchedArtist()
		in := firstAttempt(artist, usecase.SearchTriggerDaily)
		expectSearch(d, artist, in, nil, busy)
		down := apperr.New(codes.Unavailable, "database down")

		_, err := d.uc.RunSearch(context.Background(), &failingStartFlow{err: down}, in)

		assert.ErrorIs(t, err, down)
	})
}

// failingStartFlow runs steps at once and fails every Start with err.
type failingStartFlow struct {
	inlineFlow
	err error
}

// Start implements usecase.Flow.
func (f *failingStartFlow) Start(context.Context, string, string, any, ...usecase.StartOption) error {
	return f.err
}

func TestRunSearch_ChecksAgain(t *testing.T) {
	t.Parallel()

	// @spec components/usecase/concert/search-new-concerts "Represented while waiting"
	t.Run("completes without calling when an organizer came to represent the artist", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		artist := searchedArtist()
		in := firstAttempt(artist, usecase.SearchTriggerDaily)
		in.Attempt = 3
		organizers := mocks.NewMockOrganizerRepository(t)
		organizers.EXPECT().IsArtistRepresentedByActiveOrganizer(mock.Anything, artist.ID).Return(true, nil).Once()
		expectCheck(d, artist, in)
		expectStatus(d, artist.ID, entity.ConcertSearchStatusCompleted)
		f := &inlineFlow{}

		_, err := newUseCaseWithWorkflows(t, d, d.workflows, organizers).RunSearch(context.Background(), f, in)

		require.NoError(t, err)
		assert.Empty(t, f.starts)
		sent := d.signals.sent(usecase.EventConcertSearchCompleted)
		require.Len(t, sent, 1, "an earlier attempt called the search")
		assert.Equal(t, "nothing_new", sent[0]["outcome"])
	})

	t.Run("completes without a signal when the activity ended before the first call", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		artist := searchedArtist()
		artist.ActivityEnded = true
		in := firstAttempt(artist, usecase.SearchTriggerDaily)
		expectCheck(d, artist, in)
		expectStatus(d, artist.ID, entity.ConcertSearchStatusCompleted)

		runAttempt(t, d, in)

		assert.Empty(t, d.signals.sent(usecase.EventConcertSearchCompleted))
	})

	t.Run("searches when the representation check fails", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		artist := searchedArtist()
		in := firstAttempt(artist, usecase.SearchTriggerDaily)
		organizers := mocks.NewMockOrganizerRepository(t)
		organizers.EXPECT().IsArtistRepresentedByActiveOrganizer(mock.Anything, artist.ID).Return(false, apperr.ErrInternal).Once()
		expectSearch(d, artist, in, nil, nil)
		expectStatus(d, artist.ID, entity.ConcertSearchStatusCompleted)

		_, err := newUseCaseWithWorkflows(t, d, d.workflows, organizers).RunSearch(context.Background(), &inlineFlow{}, in)

		require.NoError(t, err)
	})

	// The strict mocks fail the test if anything is read further or written.
	// @spec components/usecase/concert/search-new-concerts "Search replaced"
	t.Run("changes nothing when a later search replaced this one", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		artist := searchedArtist()
		in := firstAttempt(artist, usecase.SearchTriggerDaily)
		d.concertSearchRepo.EXPECT().GetByArtistID(mock.Anything, artist.ID).Return(&entity.ConcertSearch{
			ArtistID: artist.ID, SearchTime: in.SearchTime.Add(time.Hour), Status: entity.ConcertSearchStatusPending,
		}, nil).Once()

		f := runAttempt(t, d, in)

		assert.Empty(t, f.starts)
		assert.Empty(t, d.signals.sent(usecase.EventConcertSearchCompleted))
	})
}

func TestRunSearch_Outcome(t *testing.T) {
	t.Parallel()

	// @spec components/usecase/concert/search-new-concerts "Two series found"
	// @spec components/usecase/concert/search-new-concerts "Productive search"
	t.Run("hands both series on once and records the find", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		artist := searchedArtist()
		in := firstAttempt(artist, usecase.SearchTriggerFirstFollow)
		expectSearch(d, artist, in, newShows(), nil)
		d.concertSearchRepo.EXPECT().MarkFound(mock.Anything, artist.ID).Return(nil).Once()
		expectStatus(d, artist.ID, entity.ConcertSearchStatusCompleted)

		f := runAttempt(t, d, in)

		require.Len(t, f.starts, 1)
		assert.Equal(t, usecase.WorkflowCreateFromDiscovered, f.starts[0].workflow)
		assert.Equal(t, usecase.ExportedDiscoveryWorkflowID(artist.ID, in.SearchTime), f.starts[0].id)
		data := f.starts[0].input.(entity.ConcertDiscoveredData)
		assert.Equal(t, artist.ID, data.ArtistID)
		assert.Equal(t, artist.Name, data.ArtistName)
		assert.Len(t, data.Series, 2)
		assert.Equal(t, []usecase.AnalyticsProperties{{
			"artist_id": artist.ID, "trigger": "first_follow", "outcome": "found",
			"series_count": 2, "event_count": 3,
		}}, d.signals.sent(usecase.EventConcertSearchCompleted))
		// A signal the use case sends directly is personless, with a new
		// event id and the time the search ended.
		sent := d.signals.captured(usecase.EventConcertSearchCompleted)
		assert.True(t, sent[0].Personless)
		assert.NotEmpty(t, sent[0].UUID)
		assert.WithinDuration(t, time.Now(), sent[0].Time, time.Minute)
	})

	// The strict ConcertSearch mock fails the test if a find is recorded.
	// @spec components/usecase/concert/search-new-concerts "Nothing new"
	t.Run("starts nothing and completes when every event is known", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		artist := searchedArtist()
		in := firstAttempt(artist, usecase.SearchTriggerDaily)
		date := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		hall := "Hall A"
		expectCheck(d, artist, in)
		expectReads(d, artist, []*entity.Concert{{LocalDate: date, ListedVenueName: &hall}})
		d.searcher.EXPECT().Search(mock.Anything, artist, (*entity.OfficialSite)(nil), mock.Anything).Return([]*entity.DiscoveredSeries{{
			Title: "Tour", Events: []*entity.DiscoveredEvent{{ListedVenueName: "Hall A", LocalDate: date}},
		}}, nil).Once()
		expectStatus(d, artist.ID, entity.ConcertSearchStatusCompleted)

		f := runAttempt(t, d, in)

		assert.Empty(t, f.starts)
		assert.Equal(t, []usecase.AnalyticsProperties{{
			"artist_id": artist.ID, "trigger": "daily_run", "outcome": "nothing_new",
			"series_count": 0, "event_count": 0,
		}}, d.signals.sent(usecase.EventConcertSearchCompleted))
	})

	// @spec components/usecase/concert/search-new-concerts "External search fails"
	t.Run("fails at once on a permanent error", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		artist := searchedArtist()
		in := firstAttempt(artist, usecase.SearchTriggerDaily)
		expectSearch(d, artist, in, nil, apperr.New(codes.PermissionDenied, "spend cap breached"))
		expectStatus(d, artist.ID, entity.ConcertSearchStatusFailed)

		f := runAttempt(t, d, in)

		assert.Empty(t, f.starts, "no further attempt")
		sent := d.signals.sent(usecase.EventConcertSearchCompleted)
		require.Len(t, sent, 1)
		assert.Equal(t, "permission_denied", sent[0]["error_code"])
	})

	// @spec components/usecase/concert/search-new-concerts "Failure without a kind"
	t.Run("reports an error that states no kind as unknown", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		artist := searchedArtist()
		in := firstAttempt(artist, usecase.SearchTriggerDaily)
		expectSearch(d, artist, in, nil, errors.New("connection reset"))
		expectStatus(d, artist.ID, entity.ConcertSearchStatusFailed)

		runAttempt(t, d, in)

		sent := d.signals.sent(usecase.EventConcertSearchCompleted)
		require.Len(t, sent, 1)
		assert.Equal(t, "failed", sent[0]["outcome"])
		assert.Equal(t, "unknown", sent[0]["error_code"])
	})

	// @spec components/usecase/concert/search-new-concerts "Cannot be started"
	t.Run("fails the search when CreateFromDiscovered cannot be started", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		artist := searchedArtist()
		in := firstAttempt(artist, usecase.SearchTriggerDaily)
		expectSearch(d, artist, in, newShows(), nil)
		expectStatus(d, artist.ID, entity.ConcertSearchStatusFailed)

		_, err := d.uc.RunSearch(context.Background(), &failingStartFlow{err: apperr.New(codes.Unavailable, "database down")}, in)

		require.NoError(t, err, "the outcome is the search's, not the run's")
		sent := d.signals.sent(usecase.EventConcertSearchCompleted)
		require.Len(t, sent, 1)
		assert.Equal(t, "failed", sent[0]["outcome"])
		assert.Equal(t, "unavailable", sent[0]["error_code"])
		assert.Equal(t, 0, sent[0]["event_count"])
	})

	// The strict mocks fail the test if the search is recorded as ended.
	t.Run("leaves a cancelled start to the engine", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		artist := searchedArtist()
		in := firstAttempt(artist, usecase.SearchTriggerDaily)
		expectSearch(d, artist, in, newShows(), nil)
		cancelled := apperr.New(codes.Canceled, "workflow cancelled")

		_, err := d.uc.RunSearch(context.Background(), &failingStartFlow{err: cancelled}, in)

		assert.ErrorIs(t, err, apperr.ErrCanceled)
	})

	t.Run("a transient read failure is the step's error and calls nothing", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		artist := searchedArtist()
		in := firstAttempt(artist, usecase.SearchTriggerDaily)
		expectCheck(d, artist, in)
		d.artistRepo.EXPECT().Get(mock.Anything, artist.ID).Return(nil, apperr.ErrUnavailable).Once()

		_, err := d.uc.RunSearch(context.Background(), &inlineFlow{}, in)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})

	t.Run("any other read failure fails the search without a signal", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		artist := searchedArtist()
		in := firstAttempt(artist, usecase.SearchTriggerDaily)
		expectCheck(d, artist, in)
		d.artistRepo.EXPECT().Get(mock.Anything, artist.ID).Return(nil, apperr.ErrInternal).Once()
		expectStatus(d, artist.ID, entity.ConcertSearchStatusFailed)

		runAttempt(t, d, in)

		assert.Empty(t, d.signals.sent(usecase.EventConcertSearchCompleted))
	})

	// ConcertSearch.UpdateStatus leaves a later search alone, so the end names
	// the search it ends.
	// @spec components/usecase/concert/search-new-concerts "Replaced while the last call ran"
	t.Run("ends only its own search", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		artist := searchedArtist()
		in := firstAttempt(artist, usecase.SearchTriggerFirstFollow)
		expectSearch(d, artist, in, nil, nil)
		d.concertSearchRepo.EXPECT().UpdateStatus(mock.Anything, artist.ID, in.SearchTime, entity.ConcertSearchStatusCompleted).Return(nil).Once()

		runAttempt(t, d, in)

		assert.Len(t, d.signals.sent(usecase.EventConcertSearchCompleted), 1)
	})

	// @spec components/usecase/concert/search-new-concerts "Status write fails for a moment"
	t.Run("tries a transient failure to write the status again before reporting", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		artist := searchedArtist()
		in := firstAttempt(artist, usecase.SearchTriggerDaily)
		expectSearch(d, artist, in, nil, nil)
		d.concertSearchRepo.EXPECT().UpdateStatus(mock.Anything, artist.ID, in.SearchTime, entity.ConcertSearchStatusCompleted).
			Return(apperr.ErrUnavailable).Once()
		f := newReplayFlow()

		_, err := d.uc.RunSearch(context.Background(), f, in)

		require.ErrorIs(t, err, apperr.ErrUnavailable, "the step's error, which the engine retries")
		assert.Empty(t, d.signals.sent(usecase.EventConcertSearchCompleted))

		expectStatus(d, artist.ID, entity.ConcertSearchStatusCompleted)
		_, err = d.uc.RunSearch(context.Background(), f, in)

		require.NoError(t, err)
		assert.Len(t, d.signals.sent(usecase.EventConcertSearchCompleted), 1)
	})

	t.Run("a permanent failure to write the status keeps the outcome", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		artist := searchedArtist()
		in := firstAttempt(artist, usecase.SearchTriggerDaily)
		expectSearch(d, artist, in, nil, nil)
		d.concertSearchRepo.EXPECT().UpdateStatus(mock.Anything, artist.ID, in.SearchTime, entity.ConcertSearchStatusCompleted).
			Return(apperr.ErrInternal).Once()

		runAttempt(t, d, in)

		assert.Len(t, d.signals.sent(usecase.EventConcertSearchCompleted), 1)
	})

	// The strict mocks fail the test if the search is recorded as ended.
	t.Run("a search cut short by the run's cancellation is not an outcome", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		artist := searchedArtist()
		in := firstAttempt(artist, usecase.SearchTriggerDaily)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		expectCheck(d, artist, in)
		expectReads(d, artist, nil)
		d.searcher.EXPECT().Search(mock.Anything, artist, (*entity.OfficialSite)(nil), mock.Anything).
			RunAndReturn(func(context.Context, *entity.Artist, *entity.OfficialSite, time.Time) ([]*entity.DiscoveredSeries, error) {
				cancel()
				return nil, apperr.New(codes.Canceled, "caller's context ended")
			}).Once()

		_, err := d.uc.RunSearch(ctx, &inlineFlow{}, in)

		assert.ErrorIs(t, err, apperr.ErrCanceled)
	})

	t.Run("a status write cut short by the run's cancellation records nothing", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		artist := searchedArtist()
		in := firstAttempt(artist, usecase.SearchTriggerDaily)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		expectSearch(d, artist, in, nil, nil)
		d.concertSearchRepo.EXPECT().UpdateStatus(mock.Anything, artist.ID, in.SearchTime, entity.ConcertSearchStatusCompleted).
			RunAndReturn(func(context.Context, string, time.Time, entity.ConcertSearchStatus) error {
				cancel()
				return context.Canceled
			}).Once()

		_, err := d.uc.RunSearch(ctx, &inlineFlow{}, in)

		assert.ErrorIs(t, err, context.Canceled)
		assert.Empty(t, d.signals.sent(usecase.EventConcertSearchCompleted))
	})

	// A resumed run must not search, hand on or record twice.
	t.Run("a run resumed after the search step does not call the search again", func(t *testing.T) {
		t.Parallel()
		d := newConcertTestDeps(t)
		artist := searchedArtist()
		in := firstAttempt(artist, usecase.SearchTriggerDaily)
		expectSearch(d, artist, in, newShows(), nil)
		d.concertSearchRepo.EXPECT().MarkFound(mock.Anything, artist.ID).Return(nil).Once()
		expectStatus(d, artist.ID, entity.ConcertSearchStatusCompleted)
		f := newReplayFlow()
		f.stopAt = "record-end"

		_, err := d.uc.RunSearch(context.Background(), f, in)
		require.ErrorIs(t, err, errStopped)
		f.stopAt = ""
		_, err = d.uc.RunSearch(context.Background(), f, in)

		require.NoError(t, err)
		assert.Len(t, d.signals.sent(usecase.EventConcertSearchCompleted), 1)
	})
}

// handOnCase runs one attempt whose search answers scraped, against existing
// concerts and pending candidate keys, and asserts how many events are handed
// on.
func handOnCase(t *testing.T, existing []*entity.Concert, pending []entity.ConcertCandidateDedupKey, scraped []*entity.DiscoveredSeries, want int) {
	t.Helper()
	d := newConcertTestDeps(t)
	artist := searchedArtist()
	in := firstAttempt(artist, usecase.SearchTriggerDaily)
	expectCheck(d, artist, in)
	d.artistRepo.EXPECT().Get(mock.Anything, artist.ID).Return(artist, nil).Once()
	d.artistRepo.EXPECT().GetOfficialSite(mock.Anything, artist.ID).Return(nil, apperr.ErrNotFound).Once()
	// The upcoming Concerts are read at the instant the search runs from.
	var listedAt time.Time
	d.concertRepo.EXPECT().ListUpcomingByArtist(mock.Anything, artist.ID, mock.Anything).
		Run(func(_ context.Context, _ string, now time.Time) { listedAt = now }).
		Return(existing, nil).Once()
	d.concertCandidateRepo.EXPECT().ListPendingDedupKeysByArtist(mock.Anything, artist.ID).Return(pending, nil).Once()
	d.searcher.EXPECT().Search(mock.Anything, artist, (*entity.OfficialSite)(nil),
		mock.MatchedBy(func(now time.Time) bool { return !listedAt.IsZero() && now.Equal(listedAt) })).Return(scraped, nil).Once()
	expectStatus(d, artist.ID, entity.ConcertSearchStatusCompleted)
	// A hand-on records last_find_time; nothing handed on records none.
	if want > 0 {
		d.concertSearchRepo.EXPECT().MarkFound(mock.Anything, artist.ID).Return(nil).Once()
	}

	f := runAttempt(t, d, in)

	assert.Equal(t, want, handedOn(t, f))
}

// TestSearchNewConcerts_Deduplication verifies that a search correctly
// deduplicates scraped series against existing DB records via
// entity.FilterNewSeries, aligned with the events physical natural key
// (venue_id, local_event_date, start_time). start_time disambiguates
// asymmetrically: two differing known starts are distinct shows (昼夜2公演),
// an unknown-start event is dropped when anything is already known at that
// (date, venue), and a known-start event passes when only an unknown-start
// row exists (so the creation path can fill it).
func TestSearchNewConcerts_Deduplication(t *testing.T) {
	t.Parallel()
	concertDate := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	startUTC := todAt(18, 0)

	type testCase struct {
		name            string
		existing        []*entity.Concert
		scraped         []*entity.DiscoveredSeries
		wantNewConcerts int // 0 = all deduped (no publish), >0 = event published with N events
	}

	tests := []testCase{
		// ── Cases where dedup SHOULD filter (wantNewConcerts = 0) ──

		{
			name: "same date, same start_time — deduped",
			existing: []*entity.Concert{
				{ID: "c1", LocalDate: concertDate, StartTime: startUTC, ListedVenueName: new("Zepp Tokyo")},
			},
			scraped: []*entity.DiscoveredSeries{
				{
					Title: "Concert A", Type: entity.SeriesTypeSingle, SourceURL: "https://example.com",
					Events: []*entity.DiscoveredEvent{{ListedVenueName: "Zepp Tokyo", LocalDate: concertDate, StartTime: startUTC}},
				},
			},
			wantNewConcerts: 0,
		},
		{
			name: "same date, scraped nil start_time — deduped",
			existing: []*entity.Concert{
				{ID: "c1", LocalDate: concertDate, StartTime: startUTC, ListedVenueName: new("Zepp Tokyo")},
			},
			scraped: []*entity.DiscoveredSeries{
				{
					Title: "Concert A", Type: entity.SeriesTypeSingle, SourceURL: "https://example.com",
					Events: []*entity.DiscoveredEvent{{ListedVenueName: "Zepp Tokyo", LocalDate: concertDate}},
				},
			},
			wantNewConcerts: 0,
		},
		{
			// Existing row has no published start_time; the scrape now carries one.
			// This is the FILL case: it MUST pass the filter so the creation path
			// can fill the announced time onto the existing row rather than being
			// silently dropped here (an unknown-start row does not absorb a known
			// start). The creation path then UPSERT-dedups it onto that row.
			name: "same date, existing nil start_time, scraped has start_time — passes for fill",
			existing: []*entity.Concert{
				{ID: "c1", LocalDate: concertDate, StartTime: nil, ListedVenueName: new("Zepp Tokyo")},
			},
			scraped: []*entity.DiscoveredSeries{
				{
					Title: "Concert A", Type: entity.SeriesTypeSingle, SourceURL: "https://example.com",
					Events: []*entity.DiscoveredEvent{{ListedVenueName: "Zepp Tokyo", LocalDate: concertDate, StartTime: startUTC}},
				},
			},
			wantNewConcerts: 1,
		},
		{
			name: "both nil start_time, same date — deduped",
			existing: []*entity.Concert{
				{ID: "c1", LocalDate: concertDate, StartTime: nil, ListedVenueName: new("Zepp Tokyo")},
			},
			scraped: []*entity.DiscoveredSeries{
				{
					Title: "Concert A", Type: entity.SeriesTypeSingle, SourceURL: "https://example.com",
					Events: []*entity.DiscoveredEvent{{ListedVenueName: "Zepp Tokyo", LocalDate: concertDate}},
				},
			},
			wantNewConcerts: 0,
		},
		{
			// Within-batch dedup: two events within the same series at the same venue/date.
			// The second (unknown-start) is dropped because a known-start entry was already
			// marked for that (date, venue) in the same batch.
			name:     "within-batch dedup: two events at same venue/date",
			existing: nil,
			scraped: []*entity.DiscoveredSeries{
				{
					Title: "Concert A", Type: entity.SeriesTypeSingle, SourceURL: "https://example.com/a",
					Events: []*entity.DiscoveredEvent{
						{ListedVenueName: "Zepp Tokyo", LocalDate: concertDate, StartTime: startUTC},
						// unknown-start duplicate of the same (date, venue) — absorbed
						{ListedVenueName: "Zepp Tokyo", LocalDate: concertDate},
					},
				},
			},
			wantNewConcerts: 1, // second is intra-batch duplicate
		},
		{
			// FilterNewSeries' key is (date, listedVenueName) aligned with the events
			// natural key (series_id, local_event_date, venue_id) — same date at a
			// different venue is a legitimate separate event and MUST NOT be deduplicated.
			name: "same date, different venue — NOT deduped (venue in key)",
			existing: []*entity.Concert{
				{ID: "c1", LocalDate: concertDate, StartTime: nil, ListedVenueName: new("Zepp Tokyo")},
			},
			scraped: []*entity.DiscoveredSeries{
				{
					Title: "Festival B", Type: entity.SeriesTypeSingle, SourceURL: "https://example.com",
					Events: []*entity.DiscoveredEvent{{ListedVenueName: "Tokyo Dome", LocalDate: concertDate}},
				},
			},
			wantNewConcerts: 1,
		},

		// ── Cases where dedup should NOT filter (wantNewConcerts > 0) ──

		{
			name: "different date — distinct concerts",
			existing: []*entity.Concert{
				{ID: "c1", LocalDate: concertDate, StartTime: startUTC, ListedVenueName: new("Zepp Tokyo")},
			},
			scraped: []*entity.DiscoveredSeries{
				{
					Title: "Concert Day 2", Type: entity.SeriesTypeSingle, SourceURL: "https://example.com",
					Events: []*entity.DiscoveredEvent{{ListedVenueName: "Zepp Tokyo", LocalDate: concertDate.AddDate(0, 0, 1), StartTime: startUTC}},
				},
			},
			wantNewConcerts: 1,
		},
		// @spec components/usecase/concert/search-new-concerts "Already in the catalog"
		{
			name: "mixed batch: one matches existing date, one is genuinely new date",
			existing: []*entity.Concert{
				{ID: "c1", LocalDate: concertDate, StartTime: startUTC, ListedVenueName: new("Zepp Tokyo")},
			},
			scraped: []*entity.DiscoveredSeries{
				{
					// existing — deduped away
					Title: "Existing Concert", Type: entity.SeriesTypeSingle, SourceURL: "https://example.com/old",
					Events: []*entity.DiscoveredEvent{{ListedVenueName: "Zepp Tokyo", LocalDate: concertDate}},
				},
				{
					// genuinely new
					Title: "New Concert", Type: entity.SeriesTypeSingle, SourceURL: "https://example.com/new",
					Events: []*entity.DiscoveredEvent{{ListedVenueName: "Tokyo Dome", LocalDate: concertDate.AddDate(0, 0, 7)}},
				},
			},
			wantNewConcerts: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			handOnCase(t, tt.existing, nil, tt.scraped, tt.wantNewConcerts)
		})
	}
}

// TestSearchNewConcerts_CandidateDedup verifies that concerts already pending in
// concert_candidates are excluded from the discovery batch so they are not
// re-staged, while the rejection log is intentionally NOT consulted (a rejected
// concert can always re-enter the queue).
func TestSearchNewConcerts_CandidateDedup(t *testing.T) {
	t.Parallel()

	date := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	show := func(title, venue string, start *entity.TimeOfDay) *entity.DiscoveredSeries {
		return &entity.DiscoveredSeries{
			Title: title, Type: entity.SeriesTypeSingle, SourceURL: "https://example.com/" + title,
			Events: []*entity.DiscoveredEvent{{ListedVenueName: venue, LocalDate: date, StartTime: start}},
		}
	}
	pendingAt := func(venue string) []entity.ConcertCandidateDedupKey {
		return []entity.ConcertCandidateDedupKey{{LocalDate: date, ListedVenueName: venue}}
	}

	t.Run("leaves out a concert matching a pending candidate key", func(t *testing.T) {
		t.Parallel()
		handOnCase(t, nil, pendingAt("Zepp Tokyo"),
			[]*entity.DiscoveredSeries{show("pending", "Zepp Tokyo", nil), show("new", "Osaka Hall", nil)}, 1)
	})

	// The pending candidate was captured under the canonical venue name; this
	// search drifted to a prefixed spelling of the same venue.
	t.Run("matches a drifted venue name to a pending candidate key", func(t *testing.T) {
		t.Parallel()
		handOnCase(t, nil, pendingAt("フェスティバルホール"),
			[]*entity.DiscoveredSeries{show("drifted", "大阪・フェスティバルホール", nil), show("new", "Osaka Hall", nil)}, 1)
	})

	// The strict ConcertCandidate mock fails the test if the pending candidate
	// is refreshed.
	// @spec components/usecase/concert/search-new-concerts "Pending review at another start time"
	t.Run("drops an event pending review at the same date and venue at another start time", func(t *testing.T) {
		t.Parallel()
		handOnCase(t, nil, pendingAt("Zepp Haneda"),
			[]*entity.DiscoveredSeries{show("evening", "Zepp Haneda", todAt(18, 0))}, 0)
	})

	// The strict RejectedConcert mock fails the test if it is consulted.
	// @spec components/usecase/concert/search-new-concerts "Previously rejected"
	t.Run("hands on a previously rejected concert", func(t *testing.T) {
		t.Parallel()
		handOnCase(t, nil, nil, []*entity.DiscoveredSeries{show("rejected", "Zepp Tokyo", nil)}, 1)
	})
}

func TestDiscoveryWorkflowID(t *testing.T) {
	t.Parallel()

	assert.Equal(t, usecase.ExportedDiscoveryWorkflowID("artist-1", testSearchTime), usecase.ExportedDiscoveryWorkflowID("artist-1", testSearchTime),
		"one search names one workflow")
	assert.NotEqual(t, usecase.ExportedDiscoveryWorkflowID("artist-1", testSearchTime), usecase.ExportedDiscoveryWorkflowID("artist-1", testSearchTime.Add(time.Hour)),
		"a later search names a new one")
}
