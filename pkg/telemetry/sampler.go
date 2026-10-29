package telemetry

import (
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// clientRootDrop is the root sampler: it drops a client span that has no
// parent and asks the wrapped sampler about every other root. A client span
// is an outgoing call (a database query, an HTTP call); without a parent it
// belongs to no request, event, job run or workflow run, and its trace would
// answer no question. Used inside sdktrace.ParentBased, it is consulted only
// for spans without a parent, so a call made inside a unit of work follows its
// parent. A dropped span still carries a valid trace id, so log lines written
// with its context keep one.
type clientRootDrop struct {
	root sdktrace.Sampler
}

// ShouldSample implements sdktrace.Sampler.
func (s clientRootDrop) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	if p.Kind == trace.SpanKindClient {
		return sdktrace.SamplingResult{
			Decision:   sdktrace.Drop,
			Tracestate: trace.SpanContextFromContext(p.ParentContext).TraceState(),
		}
	}
	return s.root.ShouldSample(p)
}

// Description implements sdktrace.Sampler.
func (s clientRootDrop) Description() string {
	return "ClientRootDrop{" + s.root.Description() + "}"
}
