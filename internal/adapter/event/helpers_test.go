package event_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/require"
)

func newTestLogger(t *testing.T) *logging.Logger {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return logger
}

// fakeConcertRepo is a shared test double for entity.ConcertRepository.
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
