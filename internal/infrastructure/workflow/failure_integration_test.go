package workflow_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/infrastructure/workflow"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A client reads a run's failure from the database, as an API server does.
func TestClient_AwaitKeepsTheCodeOfAFailedRun(t *testing.T) {
	dsn := setupSystemDB(t)
	ctx := context.Background()

	release := make(chan struct{})
	startWorker(t, dsn, func(rt *workflow.Runtime) {
		workflow.Register(rt, "refused-in-a-step", func(ctx context.Context, f usecase.Flow, _ string) (string, error) {
			return usecase.Step(ctx, f, "check", func(context.Context) (string, error) {
				return "", apperr.New(codes.FailedPrecondition, "user has activity in progress")
			})
		})
		workflow.Register(rt, "refused-in-the-body", func(context.Context, usecase.Flow, string) (string, error) {
			return "", apperr.Wrap(errors.New("boom"), codes.Aborted, "another execution")
		})
		workflow.Register(rt, "slow", func(ctx context.Context, f usecase.Flow, _ string) (string, error) {
			return usecase.Step(ctx, f, "wait", func(context.Context) (string, error) {
				<-release
				return "done", nil
			})
		})
	})
	defer close(release)
	client := newClient(t, dsn)

	for name, want := range map[string]*apperr.AppErr{
		"refused-in-a-step":   apperr.ErrFailedPrecondition,
		"refused-in-the-body": apperr.ErrAborted,
	} {
		require.NoError(t, client.Start(ctx, name, name, ""))
		var got string
		err := client.Await(ctx, name, waitLimit, &got)

		assert.ErrorIs(t, err, want, name)
		assert.ErrorIs(t, err, usecase.ErrWorkflowFailed, name)
		assert.True(t, needsAdmin(workflowStatus(t, dsn, name)), "%s is reported for an admin", name)
	}

	require.NoError(t, client.Start(ctx, "slow", "slow", ""))
	var got string
	err := client.Await(ctx, "slow", 200*time.Millisecond, &got)

	assert.ErrorIs(t, err, apperr.ErrDeadlineExceeded)
	assert.NotErrorIs(t, err, usecase.ErrWorkflowFailed, "the wait timed out; the run did not fail")
}
