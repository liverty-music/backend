package usecase

import (
	"context"
	"encoding/json"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// Flow is the durable workflow a usecase workflow method runs in. A workflow
// method has the shape func(ctx context.Context, f Flow, in In) (Out, error)
// and is registered by a stable name in internal/infrastructure/workflow.
//
// A workflow survives a stopped process: on the next start it continues after
// its last completed step, and a completed step returns its recorded result
// instead of running again. The rules that keep a resumed run equal to the
// first one are in backend/AGENTS.md, "Durable workflows".
type Flow interface {
	// Step runs fn once to completion; a resumed run returns the recorded result.
	// A step that fails with Unavailable or DeadlineExceeded is tried again, at
	// doubling intervals of at most an hour, until the workflow's deadline.
	Step(ctx context.Context, name string, fn func(context.Context) ([]byte, error)) ([]byte, error)
	// Start starts the named workflow with input, once per id; opts set the deadline, delay and queue.
	Start(ctx context.Context, workflow, id string, input any, opts ...StartOption) error
	// Recv waits for a signal sent to this workflow, up to timeout. The bool is
	// false when the timeout passed without one.
	Recv(ctx context.Context, topic string, timeout time.Duration) ([]byte, bool, error)
	// Patched reports whether this run takes the steps added under name.
	Patched(ctx context.Context, name string) (bool, error)
	// DeprecatePatch replaces Patched(name) once no run started before the
	// patch is left: runs that recorded the patch still replay, and the next
	// change removes the call.
	DeprecatePatch(ctx context.Context, name string) error
}

// Workflows starts, signals and waits for workflows from outside a workflow
// (API handlers, event consumers, jobs). These callers never run a workflow
// themselves; the workflow worker does.
type Workflows interface {
	// Start starts the named workflow with input, once per id: starting an id
	// that exists returns without starting a second run.
	Start(ctx context.Context, workflow, id string, input any, opts ...StartOption) error
	// Send sends message to the workflow id under topic, for its Recv.
	Send(ctx context.Context, id, topic string, message any) error
	// Await waits up to timeout for the workflow id to end and decodes its
	// result into out. It fails with DeadlineExceeded when the timeout passes.
	Await(ctx context.Context, id string, timeout time.Duration, out any) error
	// Cancel stops a workflow that has not started yet and reports whether it did;
	// false means it already started or ended.
	Cancel(ctx context.Context, id string) (bool, error)
}

// StartOptions are the settings of one Start; build them with NewStartOptions.
type StartOptions struct {
	// Deadline is when the workflow stops and is reported for an admin if it
	// has not ended. Zero means no deadline.
	Deadline time.Time
	// Delay is how long the workflow waits before it starts.
	Delay time.Duration
	// Queue is the queue the workflow runs on. Empty means the default queue.
	Queue string
}

// StartOption sets one of the StartOptions.
type StartOption func(*StartOptions)

// WithDeadline sets the time by which the workflow must end. The deadline is
// stored with the workflow, so it holds across restarts.
func WithDeadline(deadline time.Time) StartOption {
	return func(o *StartOptions) { o.Deadline = deadline }
}

// WithDelay starts the workflow after d instead of at once.
func WithDelay(d time.Duration) StartOption {
	return func(o *StartOptions) { o.Delay = d }
}

// WithQueue runs the workflow on the named queue.
func WithQueue(name string) StartOption {
	return func(o *StartOptions) { o.Queue = name }
}

// NewStartOptions applies opts in order.
func NewStartOptions(opts ...StartOption) StartOptions {
	var o StartOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// Step runs fn as the step name of f and returns its result, recorded as JSON.
func Step[R any](ctx context.Context, f Flow, name string, fn func(context.Context) (R, error)) (R, error) {
	var out R
	raw, err := f.Step(ctx, name, func(ctx context.Context) ([]byte, error) {
		v, err := fn(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(v)
	})
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, apperr.Wrap(err, codes.Internal, "decode the result of step "+name)
	}
	return out, nil
}

// Await waits up to timeout for the workflow id to end and returns its result.
func Await[R any](ctx context.Context, w Workflows, id string, timeout time.Duration) (R, error) {
	var out R
	err := w.Await(ctx, id, timeout, &out)
	return out, err
}
