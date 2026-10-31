package usecase

import (
	"context"
	"strconv"
	"time"

	"github.com/liverty-music/backend/internal/entity"
)

// WorkflowCreateFromDiscovered is the registered name of
// ConcertCreationUseCase.RunCreateFromDiscovered.
const WorkflowCreateFromDiscovered = "create-from-discovered"

// discoveryWorkflowID names the create-from-discovered workflow of one search:
// the Artist and the search time of its ConcertSearch, so one search starts
// it once.
func discoveryWorkflowID(artistID string, searchTime time.Time) string {
	return WorkflowCreateFromDiscovered + ":" + artistID + ":" + strconv.FormatInt(searchTime.UnixMicro(), 10)
}

// RunCreateFromDiscovered is the create-from-discovered workflow: it
// processes the search's batch through CreateFromDiscovered in one step. A
// step that fails with Unavailable is tried again with the same batch; every
// part of it is safe to repeat.
func (uc *concertCreationUseCase) RunCreateFromDiscovered(ctx context.Context, f Flow, in entity.ConcertDiscoveredData) (struct{}, error) {
	return Step(ctx, f, "create", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, uc.CreateFromDiscovered(ctx, in)
	})
}
