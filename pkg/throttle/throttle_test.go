//go:build synctest

package throttle_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liverty-music/backend/pkg/throttle"
	"github.com/stretchr/testify/assert"
)

func TestThrottler_Do(t *testing.T) {
	t.Run("rate limiting - tasks are spaced by interval", func(t *testing.T) {
		synctest.Run(func() {
			interval := 100 * time.Millisecond
			throttler := throttle.New(interval, 5)
			defer throttler.Close()

			start := time.Now()
			results := make(chan time.Time, 3)

			for i := 0; i < 3; i++ {
				go func() {
					throttler.Do(context.Background(), func() error {
						results <- time.Now()
						return nil
					})
				}()
			}

			t1 := <-results
			t2 := <-results
			t3 := <-results

			assert.True(t, t1.Sub(start) < 10*time.Millisecond, "first task should run immediately")
			assert.True(t, t2.Sub(t1) >= interval, "second task should be delayed by interval")
			assert.True(t, t3.Sub(t2) >= interval, "third task should be delayed by interval")
		})
	})

	t.Run("FIFO order - tasks execute in the order they were queued", func(t *testing.T) {
		synctest.Run(func() {
			interval := 10 * time.Millisecond
			throttler := throttle.New(interval, 10)
			defer throttler.Close()

			order := make(chan int, 3)
			for i := 1; i <= 3; i++ {
				id := i
				go func() {
					throttler.Do(context.Background(), func() error {
						order <- id
						return nil
					})
				}()
				// Small delay to ensure sequence in 'select' within Do
				time.Sleep(1 * time.Millisecond)
			}

			assert.Equal(t, 1, <-order)
			assert.Equal(t, 2, <-order)
			assert.Equal(t, 3, <-order)
		})
	})

	t.Run("context cancellation - Do returns error if context is cancelled", func(t *testing.T) {
		synctest.Run(func() {
			interval := 100 * time.Millisecond
			throttler := throttle.New(interval, 5)
			defer throttler.Close()

			ctx, cancel := context.WithCancel(context.Background())
			cancel() // Cancel immediately

			err := throttler.Do(ctx, func() error {
				return nil
			})

			assert.ErrorIs(t, err, context.Canceled)
		})
	})

	t.Run("task error propagation - returns the error from the task func", func(t *testing.T) {
		synctest.Run(func() {
			throttler := throttle.New(10*time.Millisecond, 5)
			defer throttler.Close()

			expectedErr := errors.New("task failed")
			err := throttler.Do(context.Background(), func() error {
				return expectedErr
			})

			assert.ErrorIs(t, err, expectedErr)
		})
	})
}
