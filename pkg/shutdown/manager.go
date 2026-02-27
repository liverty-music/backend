// Package shutdown provides ordered, phased resource teardown for
// graceful shutdown of applications running on Kubernetes.
//
// When a Pod is terminated, Kubernetes sends SIGTERM and expects the
// process to exit within terminationGracePeriodSeconds.
// The package orchestrates resource cleanup in a fixed five-phase
// sequence that respects reverse dependency order:
//
//  1. Drain  - stop background goroutines so they no longer produce work.
//  2. Flush  - flush async producers to ensure buffered messages are delivered.
//  3. External - close outbound API clients that are no longer needed.
//  4. Observe - flush observability pipelines so final spans and metrics are exported.
//  5. Datastore - close data stores last, because earlier phases may still reference them.
//
// Phases execute sequentially, but closers within the same phase run
// concurrently for faster teardown.
// If the context is cancelled (e.g. approaching SIGKILL), remaining
// phases are skipped and the error is reported.
//
// The package uses a global registry, so closers can be registered from
// any initializer without threading a manager instance through the call
// graph. Each application process has a single shutdown sequence.
//
// # Typical Usage
//
//	// During initialization:
//	shutdown.Init(logger)
//	shutdown.AddDrainPhase(cache)
//	shutdown.AddFlushPhase(publisher)
//	shutdown.AddExternalPhase(lastfmClient, musicbrainzClient)
//	shutdown.AddObservePhase(telemetryCloser)
//	shutdown.AddDatastorePhase(db)
//
//	// On SIGTERM:
//	if err := shutdown.Shutdown(ctx); err != nil {
//		logger.Error(ctx, "shutdown completed with errors", err)
//	}
//
// # Timeout Budget
//
// Align the context deadline with the Kubernetes termination budget:
//
//	terminationGracePeriodSeconds = preStopDelay + appShutdownTimeout + safetyBuffer
//	example: 60s = 5s (preStop) + 45s (app) + 10s (buffer)
package shutdown

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/pannpers/go-logging/logging"
)

const (
	phaseDrain     = "drain"
	phaseFlush     = "flush"
	phaseExternal  = "external"
	phaseObserve   = "observe"
	phaseDatastore = "datastore"

	numPhases = 5
)

var phaseOrder = [numPhases]string{
	phaseDrain,
	phaseFlush,
	phaseExternal,
	phaseObserve,
	phaseDatastore,
}

var (
	mu       sync.Mutex
	closers  = make(map[string][]io.Closer, numPhases)
	logger   *logging.Logger
	once     sync.Once
	initOnce sync.Once
)

// Init initializes the shutdown package with the given logger.
// It must be called exactly once before any Add or [Shutdown] calls.
// Subsequent calls are silently ignored to prevent accidental
// logger replacement during tests or multi-stage initialization.
func Init(l *logging.Logger) {
	initOnce.Do(func() {
		logger = l
	})
}

// AddDrainPhase registers closers that stop background goroutines.
// Typical closers: in-memory caches with periodic cleanup tickers,
// background workers, or any component that spawns long-lived goroutines.
func AddDrainPhase(c ...io.Closer) {
	mu.Lock()
	defer mu.Unlock()
	closers[phaseDrain] = append(closers[phaseDrain], c...)
}

// AddFlushPhase registers closers that flush async producers.
// Typical closers: message publishers (NATS, Kafka), buffered writers,
// or any component that holds unsent data in memory.
func AddFlushPhase(c ...io.Closer) {
	mu.Lock()
	defer mu.Unlock()
	closers[phaseFlush] = append(closers[phaseFlush], c...)
}

// AddExternalPhase registers closers for outbound API clients.
// Typical closers: HTTP clients with persistent connections, gRPC client
// connections, or third-party SDK clients (e.g. LastFM, MusicBrainz).
func AddExternalPhase(c ...io.Closer) {
	mu.Lock()
	defer mu.Unlock()
	closers[phaseExternal] = append(closers[phaseExternal], c...)
}

// AddObservePhase registers closers that flush observability pipelines.
// Typical closers: OpenTelemetry TracerProvider/MeterProvider shutdown
// functions, or log sinks that buffer before export.
// This phase runs after external clients are closed so that final
// spans referencing those calls are captured.
func AddObservePhase(c ...io.Closer) {
	mu.Lock()
	defer mu.Unlock()
	closers[phaseObserve] = append(closers[phaseObserve], c...)
}

// AddDatastorePhase registers closers for data stores.
// Typical closers: database connection pools (pgxpool), Cloud SQL
// connectors, or Redis clients.
// This phase always runs last because earlier phases may still
// reference data store connections.
func AddDatastorePhase(c ...io.Closer) {
	mu.Lock()
	defer mu.Unlock()
	closers[phaseDatastore] = append(closers[phaseDatastore], c...)
}

// Shutdown runs all registered phases in fixed order, respecting the
// context deadline.
// Phases with no registered closers are skipped.
// Within each phase, all closers run concurrently.
// Errors from individual closers are aggregated and returned as a
// single joined error.
// Shutdown is safe to call multiple times; only the first call
// executes the teardown sequence.
func Shutdown(ctx context.Context) error {
	var result error
	once.Do(func() {
		result = run(ctx)
	})
	return result
}

func run(ctx context.Context) error {
	if logger == nil {
		return errors.New("shutdown: Init() must be called before Shutdown()")
	}

	start := time.Now()
	logger.Info(ctx, "shutdown started")

	var errs error

	for _, name := range phaseOrder {
		cs := closers[name]
		if len(cs) == 0 {
			continue
		}

		if ctx.Err() != nil {
			logger.Error(ctx, "shutdown aborted, skipping remaining phases",
				ctx.Err(),
				slog.String("skipped_phase", name),
			)
			errs = errors.Join(errs, fmt.Errorf("phase %q skipped: %w", name, ctx.Err()))
			break
		}

		phaseStart := time.Now()
		logger.Info(ctx, "shutdown phase started",
			slog.String("phase", name),
			slog.Int("closers", len(cs)),
		)

		var (
			mu       sync.Mutex
			wg       sync.WaitGroup
			phaseErr error
		)
		for _, c := range cs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := c.Close(); err != nil {
					mu.Lock()
					phaseErr = errors.Join(phaseErr, fmt.Errorf("phase %q: %w", name, err))
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		errs = errors.Join(errs, phaseErr)

		logger.Info(ctx, "shutdown phase completed",
			slog.String("phase", name),
			slog.Duration("elapsed", time.Since(phaseStart)),
		)
	}

	logger.Info(ctx, "shutdown finished",
		slog.Duration("total", time.Since(start)),
		slog.Bool("clean", errs == nil),
	)

	return errs
}

// Reset clears all registered closers and resets the once guards.
// It is intended for use in tests only.
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	closers = make(map[string][]io.Closer, numPhases)
	once = sync.Once{}
	initOnce = sync.Once{}
	logger = nil
}
