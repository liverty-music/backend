package telemetry

import (
	"context"
	"io"
	"log/slog"
	"runtime/pprof"
	"sync/atomic"
	"time"

	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const goroutineLeakMeterName = "liverty-music/backend/goroutine-leak"

// GoroutineLeakMonitor periodically samples the Go 1.27 GA `goroutineleak`
// profile — goroutines detected as permanently blocked on a concurrency
// primitive (channel op, sync.Mutex, sync.Cond) with no possibility of becoming
// runnable — and publishes the count as the `backend_goroutine_leak_count` OTel
// gauge, tagged with the workload.
//
// The gauge feeds a Cloud Monitoring alert so a silent consumer/RPC wedge is
// escalated without a user report. It is an additive signal that complements,
// and does not replace, backlog-stall and liveness alerting. Its blind spots:
// leaks reachable only via global variables, or via goroutines that remain
// runnable, are not reported — those stay covered by the backlog/liveness
// signals.
//
// Sampling is decoupled from the metric export cadence on purpose: the profile
// analysis runs a leak-detection GC and is not free, so a coarse sampler
// refreshes a stored count and the (cheap) gauge callback just reports it.
type GoroutineLeakMonitor struct {
	workload string
	interval time.Duration
	logger   *logging.Logger
	prof     *pprof.Profile
	last     atomic.Int64
}

// NewGoroutineLeakMonitor registers the gauge and returns a monitor for the
// given workload. Call Start to begin sampling.
func NewGoroutineLeakMonitor(workload string, interval time.Duration, logger *logging.Logger) *GoroutineLeakMonitor {
	m := &GoroutineLeakMonitor{
		workload: workload,
		interval: interval,
		logger:   logger,
		prof:     pprof.Lookup("goroutineleak"),
	}

	meter := otel.Meter(goroutineLeakMeterName)
	_, _ = meter.Int64ObservableGauge("backend_goroutine_leak_count",
		metric.WithDescription("Number of goroutines detected as permanently blocked on a concurrency primitive (leaked), by workload"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(m.last.Load(), metric.WithAttributes(attribute.String("workload", m.workload)))
			return nil
		}),
	)
	return m
}

// Start launches the periodic sampler; it runs until ctx is canceled. It takes
// one sample immediately so the gauge reports a real value promptly.
func (m *GoroutineLeakMonitor) Start(ctx context.Context) {
	go func() {
		m.sample(ctx)
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.sample(ctx)
			}
		}
	}()
}

// sample forces a leak-detection GC cycle (by writing the profile) so that the
// profile's Count reflects the current set of leaked goroutines, stores it for
// the gauge callback, and returns it. Writing to io.Discard is intentional: we
// want the detection side effect, not the serialized profile.
func (m *GoroutineLeakMonitor) sample(ctx context.Context) int64 {
	if m.prof == nil {
		return 0
	}
	if err := m.prof.WriteTo(io.Discard, 0); err != nil {
		m.logger.Warn(ctx, "goroutine leak profile sample failed", slog.Any("error", err))
		return m.last.Load()
	}
	n := int64(m.prof.Count())
	m.last.Store(n)
	if n > 0 {
		m.logger.Warn(ctx, "goroutine leaks detected",
			slog.Int64("count", n),
			slog.String("workload", m.workload),
		)
	}
	return n
}
