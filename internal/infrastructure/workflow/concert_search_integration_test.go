package workflow_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/workflow"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

// searchWorker is a worker running the search-concerts workflow of the concert
// use case on its queue, as the workflow worker registers it, with the
// database and the external search mocked.
type searchWorker struct {
	client *workflow.Client
	dsn    string
	// searchTime is the search time of every Artist's pending ConcertSearch.
	searchTime time.Time

	mu    sync.Mutex
	calls []string
}

// startSearchWorker starts a worker whose external search runs search.
func startSearchWorker(t *testing.T, search func(ctx context.Context, artistID string) error) *searchWorker {
	t.Helper()
	dsn := setupSystemDB(t)
	w := &searchWorker{dsn: dsn, searchTime: time.Now().Truncate(time.Microsecond)}

	artists := mocks.NewMockArtistRepository(t)
	artists.EXPECT().Get(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, id string) (*entity.Artist, error) {
		return &entity.Artist{ID: id, Name: id, MBID: "11111111-1111-1111-1111-111111111111"}, nil
	}).Maybe()
	artists.EXPECT().GetOfficialSite(mock.Anything, mock.Anything).Return(nil, apperr.ErrNotFound).Maybe()
	searches := mocks.NewMockConcertSearchRepository(t)
	searches.EXPECT().GetByArtistID(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, id string) (*entity.ConcertSearch, error) {
		return &entity.ConcertSearch{ArtistID: id, SearchTime: w.searchTime, Status: entity.ConcertSearchStatusPending}, nil
	}).Maybe()
	searches.EXPECT().UpdateStatus(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	concerts := mocks.NewMockConcertRepository(t)
	concerts.EXPECT().ListUpcomingByArtist(mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	candidates := mocks.NewMockConcertCandidateRepository(t)
	candidates.EXPECT().ListPendingDedupKeysByArtist(mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	rejected := mocks.NewMockRejectedConcertRepository(t)
	rejected.EXPECT().ListByArtist(mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	searcher := mocks.NewMockConcertSearcher(t)
	searcher.EXPECT().Search(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, a *entity.Artist, _ *entity.OfficialSite, _ time.Time) ([]*entity.DiscoveredSeries, error) {
			w.mu.Lock()
			w.calls = append(w.calls, a.ID)
			w.mu.Unlock()
			return nil, search(ctx, a.ID)
		}).Maybe()
	metrics := ucmocks.NewMockConcertMetrics(t)
	metrics.EXPECT().RecordConcertSearch(mock.Anything, mock.Anything).Maybe()

	logger, _ := newLogger(t)
	uc := usecase.NewConcertUseCase(artists, concerts, nil, nil, nil, searches, candidates, rejected, searcher, nil, nil,
		metrics, nil, 24*time.Hour, 14*24*time.Hour, time.Now, logger)
	startWorker(t, dsn, func(rt *workflow.Runtime) {
		require.NoError(t, rt.RegisterQueue(usecase.ConcertSearchQueue, usecase.ConcertSearchQueueConcurrency))
		workflow.Register(rt, usecase.WorkflowSearchConcerts, uc.RunSearch)
	})
	w.client = newClient(t, dsn)
	return w
}

// start starts the first attempt of the artist's search and returns its id.
func (w *searchWorker) start(t *testing.T, artistID string) string {
	t.Helper()
	id := fmt.Sprintf("%s:%s:%d:1", usecase.WorkflowSearchConcerts, artistID, w.searchTime.UnixMicro())
	require.NoError(t, w.client.Start(context.Background(), usecase.WorkflowSearchConcerts, id,
		usecase.ConcertSearchAttempt{ArtistID: artistID, SearchTime: w.searchTime, Trigger: usecase.SearchTriggerDaily, Attempt: 1},
		usecase.WithQueue(usecase.ConcertSearchQueue)))
	return id
}

// called returns the artists whose search was called, in call order.
func (w *searchWorker) called() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.calls...)
}

// runs counts the stored search-concerts runs in status.
func (w *searchWorker) runs(t *testing.T, status string) int {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, w.dsn)
	require.NoError(t, err)
	defer func() { assert.NoError(t, conn.Close(ctx)) }()
	var n int
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT count(*) FROM dbos.workflow_status WHERE queue_name = $1 AND status = $2`,
		usecase.ConcertSearchQueue, status).Scan(&n))
	return n
}

// @spec components/usecase/concert/search-new-concerts "Eight at once"
func TestConcertSearch_EightAtOnce(t *testing.T) {
	release := make(chan struct{})
	w := startSearchWorker(t, func(ctx context.Context, _ string) error {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	})
	t.Cleanup(func() { close(release) })

	for i := range 20 {
		w.start(t, fmt.Sprintf("artist-%d", i))
	}

	require.Eventually(t, func() bool { return len(w.called()) == 8 }, waitLimit, 100*time.Millisecond)
	assert.Never(t, func() bool { return len(w.called()) > 8 }, 3*time.Second, 100*time.Millisecond)
	assert.Equal(t, 8, w.runs(t, "PENDING"), "calling")
	assert.Equal(t, 12, w.runs(t, "ENQUEUED"), "waiting")
}

// @spec components/usecase/concert/search-new-concerts "Waiting retries hold no place"
func TestConcertSearch_WaitingRetriesHoldNoPlace(t *testing.T) {
	w := startSearchWorker(t, func(_ context.Context, artistID string) error {
		if artistID == "ninth" {
			return nil
		}
		return apperr.New(codes.Unavailable, "busy")
	})
	for i := range 8 {
		w.start(t, fmt.Sprintf("busy-%d", i))
	}
	// Each busy search ends its attempt and leaves the next one waiting.
	require.Eventually(t, func() bool { return w.runs(t, "DELAYED") == 8 && w.runs(t, "SUCCESS") == 8 }, waitLimit, 100*time.Millisecond)

	ninth := w.start(t, "ninth")

	// Well within the 1 minute the 8 retries wait.
	require.Eventually(t, func() bool { return workflowStatus(t, w.dsn, ninth) == "SUCCESS" }, 30*time.Second, 100*time.Millisecond)
	assert.Contains(t, w.called(), "ninth")
	assert.Equal(t, 8, w.runs(t, "DELAYED"))
}

// @spec components/usecase/concert/search-new-concerts "Same attempt started twice"
func TestConcertSearch_SameAttemptStartedTwice(t *testing.T) {
	w := startSearchWorker(t, func(context.Context, string) error { return nil })

	id := w.start(t, "artist-1")
	require.Equal(t, id, w.start(t, "artist-1"))
	require.Eventually(t, func() bool { return workflowStatus(t, w.dsn, id) == "SUCCESS" }, waitLimit, 100*time.Millisecond)
	w.start(t, "artist-1")

	assert.Never(t, func() bool { return len(w.called()) > 1 }, 3*time.Second, 100*time.Millisecond)
	assert.Equal(t, []string{"artist-1"}, w.called())
}
