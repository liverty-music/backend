package posthog_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	posthogsdk "github.com/posthog/posthog-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/trace"

	"github.com/liverty-music/backend/internal/infrastructure/analytics/posthog"
	"github.com/liverty-music/backend/internal/usecase"
)

// testLogger returns a no-op logger suitable for unit tests; mirrors the
// helper used in the musicbrainz/lastfm/fanarttv adapter tests.
func testLogger(t *testing.T) *logging.Logger {
	t.Helper()
	l, _ := logging.New()
	return l
}

// fakeEnqueuer captures every Enqueue call into a slice and reports
// whether Close has been invoked. It satisfies posthog.Enqueuer.
type fakeEnqueuer struct {
	mu         sync.Mutex
	captured   []posthogsdk.Capture
	enqueueErr error
	closeErr   error
	closeCalls int
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
	f.closeCalls++
	return f.closeErr
}

func (f *fakeEnqueuer) Captured() []posthogsdk.Capture {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]posthogsdk.Capture, len(f.captured))
	copy(out, f.captured)
	return out
}

func (f *fakeEnqueuer) CloseCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closeCalls
}

// validUUID is reused across happy-path cases as a stand-in for a real
// platform UserId UUID.
const validUUID = "11111111-2222-3333-4444-555555555555"

// TestNew validates the production constructor's input-handling surface.
func TestNew(t *testing.T) {
	t.Parallel()

	type args struct {
		apiHost       string
		projectAPIKey string
		nilLogger     bool
	}
	tests := []struct {
		name    string
		args    args
		wantErr error
	}{
		{
			name:    "rejects empty project API key",
			args:    args{apiHost: posthog.DefaultAPIHost, projectAPIKey: ""},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "rejects whitespace-only project API key (Finding 03)",
			args:    args{apiHost: posthog.DefaultAPIHost, projectAPIKey: "   \t\n  "},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "rejects nil logger",
			args:    args{apiHost: posthog.DefaultAPIHost, projectAPIKey: "phc_test", nilLogger: true},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "succeeds with explicit apiHost and key",
			args:    args{apiHost: posthog.DefaultAPIHost, projectAPIKey: "phc_test"},
			wantErr: nil,
		},
		{
			name:    "defaults apiHost when empty",
			args:    args{apiHost: "", projectAPIKey: "phc_test"},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var lg *logging.Logger
			if !tt.args.nilLogger {
				lg = testLogger(t)
			}

			client, err := posthog.New(tt.args.apiHost, tt.args.projectAPIKey, lg)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, client)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, client)
			// Release the SDK worker goroutine.
			assert.NoError(t, client.Close())
		})
	}
}

// TestAnalyticsClient_Enqueue covers validation and happy-path branches
// of Enqueue using an injected fake enqueuer.
func TestAnalyticsClient_Enqueue(t *testing.T) {
	t.Parallel()

	type args struct {
		distinctID string
		eventName  usecase.AnalyticsEventName
		properties usecase.AnalyticsProperties
	}
	tests := []struct {
		name     string
		args     args
		setupErr error // optional: makes the fake return this on Enqueue
		wantErr  error
		check    func(t *testing.T, fake *fakeEnqueuer)
	}{
		{
			name:    "rejects empty distinctID",
			args:    args{distinctID: "", eventName: usecase.EventUserCreated},
			wantErr: apperr.ErrInvalidArgument,
			check: func(t *testing.T, fake *fakeEnqueuer) {
				t.Helper()
				assert.Empty(t, fake.Captured(), "no event should reach the SDK")
			},
		},
		{
			name:    "rejects non-UUID distinctID (Finding 05 — Zitadel sub mistakenly passed)",
			args:    args{distinctID: "not-a-uuid", eventName: usecase.EventUserCreated},
			wantErr: apperr.ErrInvalidArgument,
			check: func(t *testing.T, fake *fakeEnqueuer) {
				t.Helper()
				assert.Empty(t, fake.Captured())
			},
		},
		{
			name:    "rejects empty eventName",
			args:    args{distinctID: validUUID, eventName: ""},
			wantErr: apperr.ErrInvalidArgument,
			check: func(t *testing.T, fake *fakeEnqueuer) {
				t.Helper()
				assert.Empty(t, fake.Captured())
			},
		},
		{
			name: "rejects unknown eventName (Finding 06 — typo guard)",
			args: args{
				distinctID: validUUID,
				eventName:  usecase.AnalyticsEventName("ticket.purcase.completed"), // typo
			},
			wantErr: apperr.ErrInvalidArgument,
			check: func(t *testing.T, fake *fakeEnqueuer) {
				t.Helper()
				assert.Empty(t, fake.Captured())
			},
		},
		{
			name: "forwards a known event with properties",
			args: args{
				distinctID: validUUID,
				eventName:  usecase.EventTicketPurchaseCompleted,
				properties: usecase.AnalyticsProperties{
					"ticket_id":    "ticket-42",
					"concert_id":   "concert-7",
					"price_bucket": "3000-4999",
				},
			},
			wantErr: nil,
			check: func(t *testing.T, fake *fakeEnqueuer) {
				t.Helper()
				captured := fake.Captured()
				require.Len(t, captured, 1)
				assert.Equal(t, validUUID, captured[0].DistinctId)
				assert.Equal(t, string(usecase.EventTicketPurchaseCompleted), captured[0].Event)
				assert.Equal(t, "ticket-42", captured[0].Properties["ticket_id"])
				assert.Equal(t, "concert-7", captured[0].Properties["concert_id"])
				assert.Equal(t, "3000-4999", captured[0].Properties["price_bucket"])
			},
		},
		{
			name: "accepts nil properties and forwards nil to the SDK (Finding 15 — no empty allocation)",
			args: args{
				distinctID: validUUID,
				eventName:  usecase.EventUserCreated,
				properties: nil,
			},
			wantErr: nil,
			check: func(t *testing.T, fake *fakeEnqueuer) {
				t.Helper()
				captured := fake.Captured()
				require.Len(t, captured, 1)
				assert.Equal(t, validUUID, captured[0].DistinctId)
				assert.Nil(t, captured[0].Properties, "no empty map should be allocated when caller passes nil and no trace context is active")
			},
		},
		{
			name: "wraps SDK errors with the event name and apperr.ErrInternal",
			args: args{
				distinctID: validUUID,
				eventName:  usecase.EventArtistFollowCompleted,
				properties: nil,
			},
			setupErr: errors.New("queue full"),
			wantErr:  apperr.ErrInternal,
			check: func(t *testing.T, fake *fakeEnqueuer) {
				t.Helper()
				// no captured because the fake's enqueueErr short-circuits.
				assert.Empty(t, fake.Captured())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := &fakeEnqueuer{enqueueErr: tt.setupErr}
			client := posthog.NewWithEnqueuer(fake, testLogger(t))

			err := client.Enqueue(
				context.Background(),
				tt.args.distinctID,
				tt.args.eventName,
				tt.args.properties,
			)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			if tt.check != nil {
				tt.check(t, fake)
			}
		})
	}
}

// TestAnalyticsClient_Enqueue_TracePropagation verifies the OTel
// trace_id injection branch (Finding 10).
func TestAnalyticsClient_Enqueue_TracePropagation(t *testing.T) {
	t.Parallel()

	t.Run("injects trace_id from active OTel span", func(t *testing.T) {
		t.Parallel()
		provider := trace.NewTracerProvider()
		tracer := provider.Tracer("test")
		ctx, span := tracer.Start(context.Background(), "test-op")
		defer span.End()

		fake := &fakeEnqueuer{}
		client := posthog.NewWithEnqueuer(fake, testLogger(t))

		err := client.Enqueue(ctx, validUUID, usecase.EventUserCreated, usecase.AnalyticsProperties{"foo": "bar"})
		require.NoError(t, err)

		captured := fake.Captured()
		require.Len(t, captured, 1)
		expected := span.SpanContext().TraceID().String()
		assert.Equal(t, expected, captured[0].Properties[posthog.TracePropertyKey], "trace_id must match the active span")
		assert.Equal(t, "bar", captured[0].Properties["foo"], "caller-supplied properties must survive the injection copy")
	})

	t.Run("does not overwrite caller-supplied trace_id", func(t *testing.T) {
		t.Parallel()
		provider := trace.NewTracerProvider()
		tracer := provider.Tracer("test")
		ctx, span := tracer.Start(context.Background(), "test-op")
		defer span.End()

		fake := &fakeEnqueuer{}
		client := posthog.NewWithEnqueuer(fake, testLogger(t))

		err := client.Enqueue(ctx, validUUID, usecase.EventUserCreated, usecase.AnalyticsProperties{
			posthog.TracePropertyKey: "caller-supplied-trace",
		})
		require.NoError(t, err)

		captured := fake.Captured()
		require.Len(t, captured, 1)
		assert.Equal(t, "caller-supplied-trace", captured[0].Properties[posthog.TracePropertyKey])
	})

	t.Run("no trace_id when context has no active span", func(t *testing.T) {
		t.Parallel()
		fake := &fakeEnqueuer{}
		client := posthog.NewWithEnqueuer(fake, testLogger(t))

		err := client.Enqueue(context.Background(), validUUID, usecase.EventUserCreated, usecase.AnalyticsProperties{"foo": "bar"})
		require.NoError(t, err)

		captured := fake.Captured()
		require.Len(t, captured, 1)
		_, has := captured[0].Properties[posthog.TracePropertyKey]
		assert.False(t, has, "no trace_id should be auto-injected without an active span")
	})

	t.Run("caller's properties map is not mutated", func(t *testing.T) {
		t.Parallel()
		provider := trace.NewTracerProvider()
		tracer := provider.Tracer("test")
		ctx, span := tracer.Start(context.Background(), "test-op")
		defer span.End()

		fake := &fakeEnqueuer{}
		client := posthog.NewWithEnqueuer(fake, testLogger(t))

		callerProps := usecase.AnalyticsProperties{"foo": "bar"}
		require.NoError(t, client.Enqueue(ctx, validUUID, usecase.EventUserCreated, callerProps))

		_, mutated := callerProps[posthog.TracePropertyKey]
		assert.False(t, mutated, "caller's map must remain unchanged after trace_id injection")
	})
}

// TestAnalyticsClient_Close covers the io.Closer-compatible Close path
// including idempotency (Finding 04).
func TestAnalyticsClient_Close(t *testing.T) {
	t.Parallel()

	t.Run("closes the underlying enqueuer", func(t *testing.T) {
		t.Parallel()
		fake := &fakeEnqueuer{}
		client := posthog.NewWithEnqueuer(fake, testLogger(t))

		require.NoError(t, client.Close())
		assert.Equal(t, 1, fake.CloseCalls())
	})

	t.Run("propagates the SDK close error wrapped as apperr.ErrInternal", func(t *testing.T) {
		t.Parallel()
		sdkErr := errors.New("shutdown timeout")
		fake := &fakeEnqueuer{closeErr: sdkErr}
		client := posthog.NewWithEnqueuer(fake, testLogger(t))

		err := client.Close()
		assert.ErrorIs(t, err, apperr.ErrInternal)
		assert.ErrorIs(t, err, sdkErr)
	})

	t.Run("is idempotent across repeated calls (Finding 04)", func(t *testing.T) {
		t.Parallel()
		fake := &fakeEnqueuer{}
		client := posthog.NewWithEnqueuer(fake, testLogger(t))

		// First call closes successfully.
		require.NoError(t, client.Close())
		// Second and third calls return the same result, NOT a wrapped ErrClosed.
		assert.NoError(t, client.Close())
		assert.NoError(t, client.Close())
		// SDK Close is invoked exactly once.
		assert.Equal(t, 1, fake.CloseCalls(), "sync.Once must gate the underlying SDK call")
	})

	t.Run("idempotent error path returns the same wrapped error on repeated calls", func(t *testing.T) {
		t.Parallel()
		sdkErr := errors.New("shutdown timeout")
		fake := &fakeEnqueuer{closeErr: sdkErr}
		client := posthog.NewWithEnqueuer(fake, testLogger(t))

		first := client.Close()
		second := client.Close()
		assert.ErrorIs(t, first, apperr.ErrInternal)
		assert.ErrorIs(t, second, apperr.ErrInternal)
		assert.Equal(t, first.Error(), second.Error(), "same error on every call")
		assert.Equal(t, 1, fake.CloseCalls())
	})
}

// TestNew_GeneratedKeys exercises uuid.New() round-trips through the
// adapter to confirm production-shape distinctIDs satisfy the UUID guard.
func TestEnqueue_AcceptsUUIDGeneratedDistinctID(t *testing.T) {
	t.Parallel()
	fake := &fakeEnqueuer{}
	client := posthog.NewWithEnqueuer(fake, testLogger(t))

	generated := uuid.New().String()
	require.NoError(t, client.Enqueue(context.Background(), generated, usecase.EventUserCreated, nil))

	captured := fake.Captured()
	require.Len(t, captured, 1)
	assert.Equal(t, generated, captured[0].DistinctId)
}
