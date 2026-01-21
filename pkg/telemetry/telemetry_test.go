package telemetry_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupTelemetry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		cfg          *config.Config
		expectCloser bool
	}{
		{
			name: "setup without OTLP endpoint",
			cfg: &config.Config{
				Telemetry: config.TelemetryConfig{
					OTLPEndpoint:   "",
					ServiceName:    "test-service",
					ServiceVersion: "1.0.0",
				},
			},
			expectCloser: true,
		},
		{
			name: "setup with default config values",
			cfg: &config.Config{
				Telemetry: config.TelemetryConfig{
					ServiceName:    "go-backend-scaffold",
					ServiceVersion: "1.0.0",
				},
			},
			expectCloser: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			closer, err := telemetry.SetupTelemetry(context.Background(), tt.cfg)

			require.NoError(t, err)
			if tt.expectCloser {
				assert.NotNil(t, closer)
				// Test that closer can be called without error
				err := closer.Close()
				assert.NoError(t, err)
			} else {
				assert.Nil(t, closer)
			}
		})
	}
}
