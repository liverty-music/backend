package usecase_test

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/liverty-music/backend/internal/usecase"
)

// systemSignal is one AnalyticsClient.EnqueueSystem call.
type systemSignal struct {
	event usecase.AnalyticsEventName
	props usecase.AnalyticsProperties
	// traceID is the trace of the context the signal was sent under.
	traceID string
}

// recordingAnalytics is an AnalyticsClient that records the personless
// signals sent with EnqueueSystem. Enqueue and EnqueueOnce are not used by
// the use cases under test and record nothing.
type recordingAnalytics struct {
	mu      sync.Mutex
	signals []systemSignal
	err     error
}

var _ usecase.AnalyticsClient = (*recordingAnalytics)(nil)

func (r *recordingAnalytics) Enqueue(context.Context, string, usecase.AnalyticsEventName, usecase.AnalyticsProperties) error {
	return nil
}

func (r *recordingAnalytics) EnqueueOnce(context.Context, string, string, time.Time, usecase.AnalyticsEventName, usecase.AnalyticsProperties) error {
	return nil
}

func (r *recordingAnalytics) EnqueueSystem(ctx context.Context, event usecase.AnalyticsEventName, props usecase.AnalyticsProperties) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.signals = append(r.signals, systemSignal{event: event, props: props, traceID: trace.SpanContextFromContext(ctx).TraceID().String()})
	return r.err
}

// sent returns the recorded signals of event.
func (r *recordingAnalytics) sent(event usecase.AnalyticsEventName) []usecase.AnalyticsProperties {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []usecase.AnalyticsProperties
	for _, s := range r.signals {
		if s.event == event {
			out = append(out, s.props)
		}
	}
	return out
}

// traces returns the trace ids the signals of event were sent under.
func (r *recordingAnalytics) traces(event usecase.AnalyticsEventName) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, s := range r.signals {
		if s.event == event {
			out = append(out, s.traceID)
		}
	}
	return out
}
