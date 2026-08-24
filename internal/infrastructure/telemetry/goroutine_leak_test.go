package telemetry

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/require"
)

// TestGoroutineLeakMonitor_HealthyThenWedged exercises the two spec scenarios
// end to end against the real Go 1.27 runtime profile:
//   - a healthy process reports zero leaked goroutines;
//   - a goroutine permanently blocked on a channel receive that can never
//     unblock is detected and increments the count.
//
// Leak detection is only deterministic with GOMAXPROCS=1 (see the runtime's own
// goroutineleak testdata), so we pin it for the duration and yield to let the
// wedged goroutine park before sampling. This is white-box (package telemetry)
// because it asserts on the sampler's stored count that backs the gauge.
func TestGoroutineLeakMonitor_HealthyThenWedged(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))

	logger, err := logging.New()
	require.NoError(t, err)

	m := NewGoroutineLeakMonitor("test-workload", time.Minute, logger)
	ctx := context.Background()

	// Healthy instance: no leaked goroutines. The gauge callback reads the same
	// stored value.
	require.Zero(t, m.sample(ctx), "healthy instance must report zero leaks")
	require.Zero(t, m.last.Load(), "stored count after healthy sample must be zero")

	// Deliberately wedge a goroutine on a nil-channel receive: it can never
	// become runnable and the channel is unreachable, so it is a true leak.
	go func() {
		var c chan struct{}
		<-c
		panic("unreachable: nil-channel receive never returns")
	}()
	// Yield repeatedly so the wedged goroutine is scheduled and parks before
	// the leak-detection GC runs.
	for range 100 {
		runtime.Gosched()
	}

	require.GreaterOrEqual(t, m.sample(ctx), int64(1), "a wedged goroutine must be detected")
	require.GreaterOrEqual(t, m.last.Load(), int64(1), "stored count must reflect the wedge for the gauge")
}
