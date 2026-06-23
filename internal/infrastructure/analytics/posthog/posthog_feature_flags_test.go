package posthog_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/pannpers/go-apperr/apperr"
	posthogsdk "github.com/posthog/posthog-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/infrastructure/analytics/posthog"
)

// fakeFlagEvaluator stubs the minimal posthog SDK surface the evaluator
// depends on. result/err drive GetFeatureFlag; closeErr/closeCalls cover
// the Close path. It satisfies posthog.FlagEvaluator.
type fakeFlagEvaluator struct {
	mu         sync.Mutex
	result     interface{}
	getErr     error
	getCalls   int
	closeErr   error
	closeCalls int
}

func (f *fakeFlagEvaluator) GetFeatureFlag(posthogsdk.FeatureFlagPayload) (interface{}, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	return f.result, f.getErr
}

func (f *fakeFlagEvaluator) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls++
	return f.closeErr
}

func (f *fakeFlagEvaluator) GetCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.getCalls
}

func (f *fakeFlagEvaluator) CloseCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closeCalls
}

const flagDistinctID = "11111111-2222-3333-4444-555555555555"

// TestNewFeatureFlagEvaluator validates the production constructor's input
// handling, including the personal-API-key requirement that local
// evaluation depends on.
func TestNewFeatureFlagEvaluator(t *testing.T) {
	t.Parallel()

	type args struct {
		apiHost        string
		projectAPIKey  string
		personalAPIKey string
		nilLogger      bool
	}
	tests := []struct {
		name    string
		args    args
		wantErr error
	}{
		{
			name:    "rejects empty project API key",
			args:    args{projectAPIKey: "", personalAPIKey: "phx_test"},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "rejects whitespace-only project API key",
			args:    args{projectAPIKey: "  \t ", personalAPIKey: "phx_test"},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "rejects empty personal API key (local eval cannot work)",
			args:    args{projectAPIKey: "phc_test", personalAPIKey: ""},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "rejects whitespace-only personal API key",
			args:    args{projectAPIKey: "phc_test", personalAPIKey: "   "},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "rejects nil logger",
			args:    args{projectAPIKey: "phc_test", personalAPIKey: "phx_test", nilLogger: true},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "succeeds with explicit apiHost and both keys",
			args:    args{apiHost: posthog.DefaultAPIHost, projectAPIKey: "phc_test", personalAPIKey: "phx_test"},
			wantErr: nil,
		},
		{
			name:    "defaults apiHost when empty",
			args:    args{apiHost: "", projectAPIKey: "phc_test", personalAPIKey: "phx_test"},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var lg = testLogger(t)
			if tt.args.nilLogger {
				lg = nil
			}

			ev, err := posthog.NewFeatureFlagEvaluator(tt.args.apiHost, tt.args.projectAPIKey, tt.args.personalAPIKey, lg)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, ev)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, ev)
			assert.NoError(t, ev.Close())
		})
	}
}

// TestFeatureFlagEvaluator_IsEnabled covers the boolean evaluation path and
// its default-fallback contract.
func TestFeatureFlagEvaluator_IsEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		key        string
		distinctID string
		def        bool
		result     interface{}
		getErr     error
		want       bool
		wantCalls  int
	}{
		{
			name: "returns true when flag resolves true", key: "new-checkout", distinctID: flagDistinctID,
			def: false, result: true, want: true, wantCalls: 1,
		},
		{
			name: "returns false when flag resolves false", key: "new-checkout", distinctID: flagDistinctID,
			def: true, result: false, want: false, wantCalls: 1,
		},
		{
			name: "returns default on SDK error (no definitions synced / PostHog down)", key: "new-checkout", distinctID: flagDistinctID,
			def: true, getErr: errors.New("flags not loaded"), want: true, wantCalls: 1,
		},
		{
			name: "returns default when flag value is a variant string, not bool", key: "experiment", distinctID: flagDistinctID,
			def: false, result: "control", want: false, wantCalls: 1,
		},
		{
			name: "returns default and skips SDK on empty key", key: "", distinctID: flagDistinctID,
			def: true, want: true, wantCalls: 0,
		},
		{
			name: "returns default and skips SDK on empty distinctID", key: "new-checkout", distinctID: "",
			def: true, want: true, wantCalls: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeFlagEvaluator{result: tt.result, getErr: tt.getErr}
			ev := posthog.NewFeatureFlagEvaluatorWith(fake, testLogger(t))

			got := ev.IsEnabled(context.Background(), tt.key, tt.distinctID, tt.def)

			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantCalls, fake.GetCalls())
		})
	}
}

// TestFeatureFlagEvaluator_Variant covers the multivariate evaluation path.
func TestFeatureFlagEvaluator_Variant(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		key        string
		distinctID string
		def        string
		result     interface{}
		getErr     error
		want       string
		wantCalls  int
	}{
		{
			name: "returns assigned variant", key: "experiment", distinctID: flagDistinctID,
			def: "control", result: "treatment", want: "treatment", wantCalls: 1,
		},
		{
			name: "returns default on SDK error", key: "experiment", distinctID: flagDistinctID,
			def: "control", getErr: errors.New("flags not loaded"), want: "control", wantCalls: 1,
		},
		{
			name: "returns default when flag value is bool, not a variant", key: "release-toggle", distinctID: flagDistinctID,
			def: "control", result: true, want: "control", wantCalls: 1,
		},
		{
			name: "returns default and skips SDK on empty key", key: "", distinctID: flagDistinctID,
			def: "control", want: "control", wantCalls: 0,
		},
		{
			name: "returns default and skips SDK on empty distinctID", key: "experiment", distinctID: "",
			def: "control", want: "control", wantCalls: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeFlagEvaluator{result: tt.result, getErr: tt.getErr}
			ev := posthog.NewFeatureFlagEvaluatorWith(fake, testLogger(t))

			got := ev.Variant(context.Background(), tt.key, tt.distinctID, tt.def)

			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantCalls, fake.GetCalls())
		})
	}
}

// TestFeatureFlagEvaluator_NilReceiver confirms a typed-nil evaluator is
// safe to call and returns the call-site default, so a DI graph that leaves
// the evaluator unconfigured never panics a handler.
func TestFeatureFlagEvaluator_NilReceiver(t *testing.T) {
	t.Parallel()

	var ev *posthog.FeatureFlagEvaluator
	assert.True(t, ev.IsEnabled(context.Background(), "any", flagDistinctID, true))
	assert.False(t, ev.IsEnabled(context.Background(), "any", flagDistinctID, false))
	assert.Equal(t, "control", ev.Variant(context.Background(), "any", flagDistinctID, "control"))
}

// TestFeatureFlagEvaluator_Close covers the io.Closer-compatible Close path
// including idempotency and error wrapping.
func TestFeatureFlagEvaluator_Close(t *testing.T) {
	t.Parallel()

	t.Run("closes the underlying client once", func(t *testing.T) {
		t.Parallel()
		fake := &fakeFlagEvaluator{}
		ev := posthog.NewFeatureFlagEvaluatorWith(fake, testLogger(t))

		require.NoError(t, ev.Close())
		assert.NoError(t, ev.Close())
		assert.Equal(t, 1, fake.CloseCalls(), "sync.Once must gate the underlying SDK call")
	})

	t.Run("propagates the SDK close error wrapped as apperr.ErrInternal", func(t *testing.T) {
		t.Parallel()
		sdkErr := errors.New("shutdown timeout")
		fake := &fakeFlagEvaluator{closeErr: sdkErr}
		ev := posthog.NewFeatureFlagEvaluatorWith(fake, testLogger(t))

		err := ev.Close()
		assert.ErrorIs(t, err, apperr.ErrInternal)
		assert.ErrorIs(t, err, sdkErr)
	})
}
