// Package throttle provides tools for controlling the rate of task execution.
// It is designed to be generic and can be used for API rate limiting,
// background task processing, or any scenario requiring sequential
// execution with a fixed interval.
package throttle

import (
	"context"
	"time"
)

// Throttler ensures that tasks are executed at a controlled rate in FIFO order.
// It uses a single worker goroutine to process tasks sequentially with a delay.
type Throttler struct {
	tasks chan func()
	done  chan struct{}
}

// New creates a new Throttler that executes one task every interval.
// The bufferSize determines how many tasks can be queued before blocking the caller.
// Usage:
//
//	t := throttle.New(200*time.Millisecond, 100)
//	defer t.Close()
func New(interval time.Duration, bufferSize int) *Throttler {
	t := &Throttler{
		tasks: make(chan func(), bufferSize),
		done:  make(chan struct{}),
	}

	go t.run(interval)

	return t
}

// Close stops the throttler worker goroutine and waits for it to exit.
// It should be called when the throttler is no longer needed to free resources.
func (t *Throttler) Close() {
	close(t.tasks)
	<-t.done
}

func (t *Throttler) run(interval time.Duration) {
	defer close(t.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for task := range t.tasks {
		task()
		// Wait for the next tick before picking up the next task
		<-ticker.C
	}
}

// Do adds a task to the throttler queue and waits for its execution.
// It returns the error produced by the task or a context-related error.
//
// # Possible errors
//
//   - Canceled: The context was canceled before or during task execution.
//   - DeadlineExceeded: The context reached its deadline before or during task execution.
func (t *Throttler) Do(ctx context.Context, f func() error) error {
	// A buffered channel of size 1 ensures that the worker can send the result
	// even if the caller has already stopped waiting due to context timeout.
	res := make(chan error, 1)

	task := func() {
		// Even if the task reached the worker, we check if the caller still cares.
		if ctx.Err() != nil {
			res <- ctx.Err()
			return
		}
		res <- f()
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case t.tasks <- task:
		// Wait for the worker to finish the task or for the context to be cancelled.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-res:
			return err
		}
	}
}
