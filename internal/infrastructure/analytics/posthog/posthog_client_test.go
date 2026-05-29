package posthog_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	posthogsdk "github.com/posthog/posthog-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/infrastructure/analytics/posthog"
	"github.com/liverty-music/backend/internal/usecase"
)

// fakeEnqueuer captures every Enqueue call into a slice and reports whether
// Close has been invoked. It implements posthog.Enqueuer.
type fakeEnqueuer struct {
	mu         sync.Mutex
	captured   []posthogsdk.Capture
	enqueueErr error
	closeErr   error
	closed     bool
}

func (f *fakeEnqueuer) Enqueue(msg posthogsdk.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.enqueueErr != nil {
		return f.enqueueErr
	}
	if c, ok := msg.(posthogsdk.Capture); ok {
		f.captured = append(f.captured, c)
	}
	return nil
}

func (f *fakeEnqueuer) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return f.closeErr
}

func (f *fakeEnqueuer) Captured() []posthogsdk.Capture {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]posthogsdk.Capture, len(f.captured))
	copy(out, f.captured)
	return out
}

func (f *fakeEnqueuer) IsClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// TestNew validates the production constructor's input-handling surface
// without sending any real network traffic.
func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("rejects empty project API key", func(t *testing.T) {
		t.Parallel()
		client, err := posthog.New("https://eu.i.posthog.com", "")
		assert.Nil(t, client)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "project API key")
	})

	t.Run("succeeds with explicit apiHost and key", func(t *testing.T) {
		t.Parallel()
		client, err := posthog.New("https://eu.i.posthog.com", "phc_test")
		require.NoError(t, err)
		require.NotNil(t, client)
		// Close immediately to release the SDK worker goroutine.
		assert.NoError(t, client.Close(context.Background()))
	})

	t.Run("defaults apiHost when empty", func(t *testing.T) {
		t.Parallel()
		client, err := posthog.New("", "phc_test")
		require.NoError(t, err)
		require.NotNil(t, client)
		assert.NoError(t, client.Close(context.Background()))
	})
}

// TestAnalyticsClient_Enqueue covers the happy path and the validation
// branches of Enqueue using an injected fake enqueuer.
func TestAnalyticsClient_Enqueue(t *testing.T) {
	t.Parallel()

	t.Run("forwards a valid event to the SDK", func(t *testing.T) {
		t.Parallel()
		fake := &fakeEnqueuer{}
		client := posthog.NewWithEnqueuer(fake)

		err := client.Enqueue(
			context.Background(),
			"user-1",
			usecase.EventTicketPurchaseCompleted,
			usecase.AnalyticsProperties{
				"ticket_id":    "ticket-42",
				"concert_id":   "concert-7",
				"price_bucket": "3000-4999",
			},
		)
		require.NoError(t, err)

		captured := fake.Captured()
		require.Len(t, captured, 1)
		assert.Equal(t, "user-1", captured[0].DistinctId)
		assert.Equal(t, string(usecase.EventTicketPurchaseCompleted), captured[0].Event)
		assert.Equal(t, "ticket-42", captured[0].Properties["ticket_id"])
		assert.Equal(t, "concert-7", captured[0].Properties["concert_id"])
		assert.Equal(t, "3000-4999", captured[0].Properties["price_bucket"])
	})

	t.Run("rejects empty distinctID without contacting the SDK", func(t *testing.T) {
		t.Parallel()
		fake := &fakeEnqueuer{}
		client := posthog.NewWithEnqueuer(fake)

		err := client.Enqueue(context.Background(), "", usecase.EventUserCreated, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "distinctID")
		assert.Empty(t, fake.Captured())
	})

	t.Run("rejects empty eventName without contacting the SDK", func(t *testing.T) {
		t.Parallel()
		fake := &fakeEnqueuer{}
		client := posthog.NewWithEnqueuer(fake)

		err := client.Enqueue(context.Background(), "user-1", "", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "eventName")
		assert.Empty(t, fake.Captured())
	})

	t.Run("accepts nil properties and forwards an empty property bag", func(t *testing.T) {
		t.Parallel()
		fake := &fakeEnqueuer{}
		client := posthog.NewWithEnqueuer(fake)

		err := client.Enqueue(context.Background(), "user-2", usecase.EventUserCreated, nil)
		require.NoError(t, err)

		captured := fake.Captured()
		require.Len(t, captured, 1)
		assert.Equal(t, "user-2", captured[0].DistinctId)
		assert.NotNil(t, captured[0].Properties)
	})

	t.Run("wraps SDK errors with the event name for diagnostics", func(t *testing.T) {
		t.Parallel()
		sdkErr := errors.New("queue full")
		fake := &fakeEnqueuer{enqueueErr: sdkErr}
		client := posthog.NewWithEnqueuer(fake)

		err := client.Enqueue(
			context.Background(),
			"user-1",
			usecase.EventArtistFollowCompleted,
			nil,
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), string(usecase.EventArtistFollowCompleted))
		assert.ErrorIs(t, err, sdkErr)
	})
}

// TestAnalyticsClient_Close exercises the Close path including SDK error
// propagation.
func TestAnalyticsClient_Close(t *testing.T) {
	t.Parallel()

	t.Run("closes the underlying enqueuer", func(t *testing.T) {
		t.Parallel()
		fake := &fakeEnqueuer{}
		client := posthog.NewWithEnqueuer(fake)

		require.NoError(t, client.Close(context.Background()))
		assert.True(t, fake.IsClosed())
	})

	t.Run("propagates the SDK close error wrapped", func(t *testing.T) {
		t.Parallel()
		sdkErr := errors.New("shutdown timeout")
		fake := &fakeEnqueuer{closeErr: sdkErr}
		client := posthog.NewWithEnqueuer(fake)

		err := client.Close(context.Background())
		require.Error(t, err)
		assert.ErrorIs(t, err, sdkErr)
	})
}
