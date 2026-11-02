package usecase_test

import (
	"context"
	"errors"
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

// errStopped is the failure of a step at which a replayFlow stops, as a
// worker that stops before the step's result is recorded.
var errStopped = errors.New("test: the worker stopped")

// replayFlow runs a workflow's steps as DBOS does across restarts: a step
// whose result is recorded returns it without running again. A run fails at
// the step named stopAt, before running it; clear stopAt and run the workflow
// again with the same flow to resume it. Starts are recorded in started.
type replayFlow struct {
	recorded map[string][]byte
	stopAt   string
	started  []string
}

var _ usecase.Flow = (*replayFlow)(nil)

func newReplayFlow() *replayFlow { return &replayFlow{recorded: map[string][]byte{}} }

func (f *replayFlow) Step(ctx context.Context, name string, fn func(context.Context) ([]byte, error)) ([]byte, error) {
	if out, ok := f.recorded[name]; ok {
		return out, nil
	}
	if name == f.stopAt {
		return nil, errStopped
	}
	out, err := fn(ctx)
	if err != nil {
		return nil, err
	}
	f.recorded[name] = out
	return out, nil
}

func (f *replayFlow) Start(_ context.Context, _, id string, _ any, _ ...usecase.StartOption) error {
	f.started = append(f.started, id)
	return nil
}

func (f *replayFlow) Recv(context.Context, string, time.Duration) ([]byte, bool, error) {
	return nil, false, nil
}

func (f *replayFlow) Patched(context.Context, string) (bool, error) { return true, nil }

func (f *replayFlow) DeprecatePatch(context.Context, string) error { return nil }
