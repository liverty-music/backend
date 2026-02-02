package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		prefix  string
		envVars map[string]string
		want    *Config
		wantErr error
	}{
		{
			name:   "load with default values",
			prefix: "APP",
			envVars: map[string]string{
				"APP_DATABASE_NAME":                   "defaultdb",
				"APP_DATABASE_USER":                   "defaultuser",
				"APP_DATABASE_PASSWORD":               "defaultpass",
				"APP_GCP_PROJECT_ID":                  "test-project",
				"APP_GCP_VERTEX_AI_SEARCH_DATA_STORE": "test-datastore",
			},
			want: &Config{
				Environment:     "development",
				Debug:           false,
				ShutdownTimeout: 30 * time.Second,
				Server: ServerConfig{
					Port:              8080,
					Host:              "localhost",
					ReadHeaderTimeout: 500 * time.Millisecond,
					ReadTimeout:       1 * time.Second,
					HandlerTimeout:    5 * time.Second,
					IdleTimeout:       3 * time.Second,
				},
				Database: DatabaseConfig{
					Host:            "localhost",
					Port:            5432,
					Name:            "defaultdb",
					User:            "defaultuser",
					Password:        "defaultpass",
					SSLMode:         "disable",
					MaxOpenConns:    25,
					MaxIdleConns:    5,
					ConnMaxLifetime: 300,
				},
				Logging: LoggingConfig{
					Level:         "info",
					Format:        "json",
					Structured:    true,
					IncludeCaller: false,
				},
				Telemetry: TelemetryConfig{
					OTLPEndpoint:   "",
					ServiceName:    "go-backend-scaffold",
					ServiceVersion: "1.0.0",
				},
				GCP: GCPConfig{
					ProjectID:               "test-project",
					Location:                "us-central1",
					GeminiModel:             "gemini-3-flash-preview",
					VertexAISearchDataStore: "test-datastore",
				},
			},
			wantErr: nil,
		},
		{
			name:   "load with custom values",
			prefix: "APP",
			envVars: map[string]string{
				"APP_ENVIRONMENT":                     "production",
				"APP_DEBUG":                           "true",
				"APP_SHUTDOWN_TIMEOUT":                "15s",
				"APP_SERVER_PORT":                     "9090",
				"APP_SERVER_HOST":                     "0.0.0.0",
				"APP_SERVER_READ_HEADER_TIMEOUT":      "200ms",
				"APP_SERVER_READ_TIMEOUT":             "2s",
				"APP_SERVER_HANDLER_TIMEOUT":          "10s",
				"APP_SERVER_IDLE_TIMEOUT":             "45s",
				"APP_DATABASE_NAME":                   "testdb",
				"APP_DATABASE_USER":                   "testuser",
				"APP_DATABASE_PASSWORD":               "testpass",
				"APP_LOGGING_LEVEL":                   "debug",
				"APP_LOGGING_FORMAT":                  "text",
				"APP_GCP_PROJECT_ID":                  "custom-project",
				"APP_GCP_VERTEX_AI_SEARCH_DATA_STORE": "custom-datastore",
			},
			want: &Config{
				Environment:     "production",
				Debug:           true,
				ShutdownTimeout: 15 * time.Second,
				Server: ServerConfig{
					Port:              9090,
					Host:              "0.0.0.0",
					ReadHeaderTimeout: 200 * time.Millisecond,
					ReadTimeout:       2 * time.Second,
					HandlerTimeout:    10 * time.Second,
					IdleTimeout:       45 * time.Second,
				},
				Database: DatabaseConfig{
					Host:            "localhost",
					Port:            5432,
					Name:            "testdb",
					User:            "testuser",
					Password:        "testpass",
					SSLMode:         "disable",
					MaxOpenConns:    25,
					MaxIdleConns:    5,
					ConnMaxLifetime: 300,
				},
				Logging: LoggingConfig{
					Level:         "debug",
					Format:        "text",
					Structured:    true,
					IncludeCaller: false,
				},
				Telemetry: TelemetryConfig{
					OTLPEndpoint:   "",
					ServiceName:    "go-backend-scaffold",
					ServiceVersion: "1.0.0",
				},
				GCP: GCPConfig{
					ProjectID:               "custom-project",
					Location:                "us-central1",
					GeminiModel:             "gemini-3-flash-preview",
					VertexAISearchDataStore: "custom-datastore",
				},
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for key, value := range tt.envVars {
				t.Setenv(key, value)
			}

			got, err := Load(tt.prefix)
			if tt.wantErr != nil {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
