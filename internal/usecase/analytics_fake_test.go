package usecase_test

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/trace"

	"github.com/liverty-music/backend/internal/usecase"
)

// systemSignal is one personless AnalyticsClient.Capture call.
type systemSignal struct {
	event usecase.AnalyticsEventName
	props usecase.AnalyticsProperties
	// traceID is the trace of the context the signal was sent under.
	traceID string
}

// recordingAnalytics is an AnalyticsClient that records the captured
// events.
type recordingAnalytics struct {
	mu      sync.Mutex
	signals []systemSignal
	events  []usecase.AnalyticsEvent
	err     error
}

var _ usecase.AnalyticsClient = (*recordingAnalytics)(nil)

func (r *recordingAnalytics) Capture(ctx context.Context, event usecase.AnalyticsEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	r.signals = append(r.signals, systemSignal{event: event.Name, props: event.Properties, traceID: trace.SpanContextFromContext(ctx).TraceID().String()})
	return r.err
}

// captured returns the recorded events of name.
func (r *recordingAnalytics) captured(name usecase.AnalyticsEventName) []usecase.AnalyticsEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []usecase.AnalyticsEvent
	for _, e := range r.events {
		if e.Name == name {
			out = append(out, e)
		}
	}
	return out
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
