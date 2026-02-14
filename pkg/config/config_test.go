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
				"APP_JWT_ISSUER":                      "https://test-issuer.com",
			},
			want: &Config{
				Environment:     "development",
				ShutdownTimeout: 30 * time.Second,
				Server: ServerConfig{
					Port:              8080,
					Host:              "localhost",
					ReadHeaderTimeout: 500 * time.Millisecond,
					ReadTimeout:       1 * time.Second,
					HandlerTimeout:    5 * time.Second,
					IdleTimeout:       3 * time.Second,
					AllowedOrigins:    []string{"http://localhost:9000"},
				},
				Database: DatabaseConfig{
					Host:            "localhost",
					Port:            5432,
					Name:            "defaultdb",
					User:            "defaultuser",
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
				JWT: JWTConfig{
					Issuer:              "https://test-issuer.com",
					JWKSRefreshInterval: 15 * time.Minute,
				},
			},
			wantErr: nil,
		},
		{
			name:   "load with custom values",
			prefix: "APP",
			envVars: map[string]string{
				"APP_ENVIRONMENT":                     "production",
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
				"APP_JWT_ISSUER":                      "https://custom-issuer.com",
				"APP_JWT_JWKS_REFRESH_INTERVAL":       "30m",
			},
			want: &Config{
				Environment:     "production",
				ShutdownTimeout: 15 * time.Second,
				Server: ServerConfig{
					Port:              9090,
					Host:              "0.0.0.0",
					ReadHeaderTimeout: 200 * time.Millisecond,
					ReadTimeout:       2 * time.Second,
					HandlerTimeout:    10 * time.Second,
					IdleTimeout:       45 * time.Second,
					AllowedOrigins:    []string{"http://localhost:9000"},
				},
				Database: DatabaseConfig{
					Host:            "localhost",
					Port:            5432,
					Name:            "testdb",
					User:            "testuser",
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
				JWT: JWTConfig{
					Issuer:              "https://custom-issuer.com",
					JWKSRefreshInterval: 30 * time.Minute,
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

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{
			name: "valid development config",
			config: &Config{
				Environment: "development",
				Server:      ServerConfig{Port: 8080},
				Database: DatabaseConfig{
					Port:                   5432,
					InstanceConnectionName: "project:region:instance",
				},
				Logging: LoggingConfig{Level: "info", Format: "json"},
				JWT: JWTConfig{
					Issuer:              "https://test-issuer.com",
					JWKSRefreshInterval: 15 * time.Minute,
				},
			},
			wantErr: false,
		},
		{
			name: "missing connection name in development",
			config: &Config{
				Environment: "development",
				Server:      ServerConfig{Port: 8080},
				Database: DatabaseConfig{
					Port: 5432,
					// Missing InstanceConnectionName
				},
				Logging: LoggingConfig{Level: "info", Format: "json"},
				JWT: JWTConfig{
					Issuer:              "https://test-issuer.com",
					JWKSRefreshInterval: 15 * time.Minute,
				},
			},
			wantErr: true,
		},
		{
			name: "valid local config without connection name",
			config: &Config{
				Environment: "local",
				Server:      ServerConfig{Port: 8080},
				Database: DatabaseConfig{
					Port: 5432,
					// Missing InstanceConnectionName is OK for local
				},
				Logging: LoggingConfig{Level: "info", Format: "json"},
				JWT: JWTConfig{
					Issuer:              "https://test-issuer.com",
					JWKSRefreshInterval: 15 * time.Minute,
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
