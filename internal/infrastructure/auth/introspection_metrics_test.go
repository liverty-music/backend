package auth_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/liverty-music/backend/internal/infrastructure/auth"
)

// The duration is recorded in seconds, so its buckets must separate answers
// around 0.3 seconds, the p95 an ask must stay under.
//
// Not parallel: it sets the global meter provider.
func TestIntrospector_DurationBuckets(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })
	f := newFakeSignInService(t)
	introspector, err := auth.NewIntrospector(f.issuer(), writeAPIKey(t), http.DefaultTransport)
	require.NoError(t, err)

	_, err = introspector.Active(t.Context(), "token", time.Now().Add(time.Hour))
	require.NoError(t, err)

	var got metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &got))
	var bounds []float64
	for _, scope := range got.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name == "sign_in.introspection.duration" {
				histogram, ok := m.Data.(metricdata.Histogram[float64])
				require.True(t, ok)
				require.NotEmpty(t, histogram.DataPoints)
				bounds = histogram.DataPoints[0].Bounds
			}
		}
	}
	assert.Subset(t, bounds, []float64{0.05, 0.1, 0.3, 0.5, 5})
}
