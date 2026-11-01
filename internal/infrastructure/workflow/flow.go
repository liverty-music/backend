package workflow

import (
	"context"
	"errors"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/liverty-music/backend/internal/usecase"
	"go.opentelemetry.io/otel/codes"
)

var _ usecase.Flow = (*flow)(nil)

// flow is the usecase.Flow of one workflow run.
type flow struct {
	// run is the DBOS context of the run. A context a usecase derives from it
	// (a span, context.WithoutCancel) is turned back into a DBOS context by
	// dbosContext.
	run dbos.Context
}

// dbosContext returns ctx as a DBOS context of this run, so values and
// cancellation the usecase added to ctx apply to the call.
func (f *flow) dbosContext(ctx context.Context) dbos.Context {
	if d, ok := ctx.(dbos.Context); ok {
		return d
	}
	return dbos.From(f.run, ctx)
}

// Step runs fn as a DBOS step with the step defaults, in a span named after
// the step.
func (f *flow) Step(ctx context.Context, name string, fn func(context.Context) ([]byte, error)) ([]byte, error) {
	out, err := dbos.RunAsStep(f.dbosContext(ctx), func(stepCtx context.Context) ([]byte, error) {
		d, ok := stepCtx.(dbos.Context)
		if !ok {
			return fn(stepCtx)
		}
		stepID, err := dbos.GetStepID(d)
		if err != nil {
			return nil, err
		}
		spanCtx, span := startStepSpan(stepCtx, name, stepID)
		defer span.End()
		out, err := fn(spanCtx)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		return out, err
	}, stepOptions(name)...)
	return out, toAppErr(err, "workflow step "+name)
}

// Start enqueues the named workflow, checkpointed as a step of this run.
func (f *flow) Start(ctx context.Context, workflow, id string, input any, opts ...usecase.StartOption) error {
	return start(f.dbosContext(ctx), ctx, workflow, id, input, opts...)
}

// Recv waits for a message on topic, up to timeout. The wait is durable: a
// resumed run waits only for what is left of it.
func (f *flow) Recv(ctx context.Context, topic string, timeout time.Duration) ([]byte, bool, error) {
	msg, err := dbos.Recv[[]byte](f.dbosContext(ctx), topic, timeout)
	if errors.Is(err, dbos.ErrTimeout) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, toAppErr(err, "workflow receive "+topic)
	}
	return msg, true, nil
}

// Patched reports whether this run takes the steps added under name.
func (f *flow) Patched(ctx context.Context, name string) (bool, error) {
	patched, err := dbos.Patch(f.dbosContext(ctx), name)
	return patched, toAppErr(err, "workflow patch "+name)
}

// DeprecatePatch replaces Patched(name) once no run started before the patch
// is left.
func (f *flow) DeprecatePatch(ctx context.Context, name string) error {
	return toAppErr(dbos.DeprecatePatch(f.dbosContext(ctx), name), "workflow deprecate patch "+name)
}
