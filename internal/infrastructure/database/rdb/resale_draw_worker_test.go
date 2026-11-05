package rdb_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/workflow"
	"github.com/liverty-music/backend/internal/usecase"
)

// failingPublishState reports the publish state of every event but one, whose
// read fails, so that stock's draw fails.
type failingPublishState struct {
	usecase.EventPublishStatePort
	failing string
}

func (s failingPublishState) IsEventPublished(ctx context.Context, eventID string) (bool, error) {
	if eventID == s.failing {
		return false, errors.New("test: the event cannot be read")
	}
	return s.EventPublishStatePort.IsEventPublished(ctx, eventID)
}

// TestResaleDraw_Worker runs draw-resale for one scheduled time twice on a
// real workflow worker: each due stock is drawn once, in its own workflow,
// and a stock whose draw fails leaves the others drawn.
func TestResaleDraw_Worker(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	logger, err := logging.New()
	require.NoError(t, err)
	repo := rdb.NewResaleEntryRepository(testDB, testOutbox)

	// Events on 10 March 2027, so these stocks have a daily draw at 12:00 on
	// 9 March.
	start := time.Date(2027, 3, 10, 19, 0, 0, 0, resaleJST)
	at := time.Date(2027, 3, 9, 12, 0, 0, 0, resaleJST)
	apply := time.Date(2027, 3, 1, 10, 0, 0, 0, resaleJST)
	expire := apply.Add(30 * 24 * time.Hour)
	stock := func() (*stockFixture, *entity.ResaleEntry, entity.ResaleListingID) {
		f := seedResaleStock(t, 2, 4)
		_, err := testDB.Pool.Exec(ctx, `UPDATE events SET start_at = $2 WHERE id = $1`, f.EventID, start)
		require.NoError(t, err)
		l := seedListing(t, f)
		e := newResaleEntry(f, newUserID(t), 1)
		e.CreateTime, e.CardAuthorizationExpireTime = apply, &expire
		created, err := repo.Create(ctx, e)
		require.NoError(t, err)
		return f, created, l
	}
	drawnStock, drawnEntry, drawnListing := stock()
	failedStock, failedEntry, _ := stock()

	uc := usecase.NewResaleEntryUseCase(usecase.ResaleEntryDeps{
		EntryRepo:  repo,
		StockRepo:  rdb.NewTicketStockRepository(testDB),
		EventRepo:  rdb.NewEventRepository(testDB),
		EventState: failingPublishState{EventPublishStatePort: rdb.NewEventPublishStateRepository(testDB), failing: failedStock.EventID},
		Clock:      func() time.Time { return at },
		Logger:     logger,
	})
	story := &lotteryStory{t: t, ctx: ctx, logger: logger}
	rt, err := workflow.NewRuntime(ctx, story.newPool(), logger)
	require.NoError(t, err)
	workflow.Register(rt, usecase.WorkflowDrawResale, uc.DrawResale)
	workflow.Register(rt, usecase.WorkflowDrawResaleStock, uc.DrawStock)
	require.NoError(t, rt.Launch())
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { _ = rt.Close() }) })
	client, err := workflow.NewClient(ctx, story.newPool(), logger)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	// The schedule runs one workflow per scheduled time; a second start of the
	// same run starts nothing new. The id carries this test run's time too:
	// the workflow system database keeps finished runs, and a run left by an
	// earlier test run would answer without starting this run's stocks.
	id := usecase.WorkflowDrawResale + ":" + at.UTC().Format(time.RFC3339) + ":" + time.Now().Format(time.RFC3339Nano)
	for range 2 {
		require.NoError(t, client.Start(ctx, usecase.WorkflowDrawResale, id, at))
		due, err := usecase.Await[int](ctx, client, id, storyWait)
		require.NoError(t, err)
		// Other tests' stocks whose resale closed with Entered entries left
		// are due too, as missed last draws; this test's two are among them.
		assert.GreaterOrEqual(t, due, 2)
	}

	require.NoError(t, client.Await(ctx, usecase.DrawResaleStockWorkflowID(drawnStock.ID, at), storyWait, nil))
	assert.Error(t, client.Await(ctx, usecase.DrawResaleStockWorkflowID(failedStock.ID, at), storyWait, nil), "the failing stock's draw ends with its error")

	won, err := repo.Get(ctx, drawnEntry.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.ResaleEntryStateWon, won.State, "the other stock is drawn")
	assert.Equal(t, drawnEntry.ID, listingEntry(t, drawnListing))
	waiting, err := repo.Get(ctx, failedEntry.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.ResaleEntryStateEntered, waiting.State)

	// Drawing the same time again records nothing more: the stock's draw ran
	// once under its name.
	var draws int
	require.NoError(t, testDB.Pool.QueryRow(ctx,
		`SELECT count(*) FROM dbos.workflow_status WHERE workflow_uuid = $1`, usecase.DrawResaleStockWorkflowID(drawnStock.ID, at)).Scan(&draws))
	assert.Equal(t, 1, draws)
}
