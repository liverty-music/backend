package usecase_test

import (
	"context"
	"time"

	"github.com/liverty-music/backend/internal/usecase"
)

// inlineFlow runs a workflow's steps at once, in the test's goroutine, as a
// first run with no recorded step does. Starts are recorded in started.
type inlineFlow struct {
	started []string
}

var _ usecase.Flow = (*inlineFlow)(nil)

func (f *inlineFlow) Step(ctx context.Context, _ string, fn func(context.Context) ([]byte, error)) ([]byte, error) {
	return fn(ctx)
}

func (f *inlineFlow) Start(_ context.Context, _, id string, _ any, _ ...usecase.StartOption) error {
	f.started = append(f.started, id)
	return nil
}

func (f *inlineFlow) Recv(context.Context, string, time.Duration) ([]byte, bool, error) {
	return nil, false, nil
}

func (f *inlineFlow) Patched(context.Context, string) (bool, error) { return true, nil }

func (f *inlineFlow) DeprecatePatch(context.Context, string) error { return nil }
