package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_ServerConfig(t *testing.T) {
	tests := []struct {
		name    string
		envVars map[string]string
		want    *ServerConfig
		wantErr bool
	}{
		{
			name: "load with default values",
			envVars: map[string]string{
				"DATABASE_NAME":                   "defaultdb",
				"DATABASE_USER":                   "defaultuser",
				"GCP_PROJECT_ID":                  "test-project",
				"GCP_VERTEX_AI_SEARCH_DATA_STORE": "test-datastore",
				"OIDC_ISSUER_URL":                 "https://test-issuer.com",
			},
			want: &ServerConfig{
				BaseConfig: BaseConfig{
					Environment:     "local",
					ShutdownTimeout: 30 * time.Second,
					Database: DatabaseConfig{
						Host:              "localhost",
						Port:              5432,
						Name:              "defaultdb",
						User:              "defaultuser",
						SSLMode:           "disable",
						Schema:            "app",
						MaxOpenConns:      10,
						MaxIdleConns:      2,
						ConnMaxLifetime:   1800,
						MaxConnIdleTime:   600,
						HealthCheckPeriod: 60,
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
				},
				Server: ServerSettings{
					Port:              8080,
					Host:              "localhost",
					ReadHeaderTimeout: 500 * time.Millisecond,
					ReadTimeout:       1 * time.Second,
					HandlerTimeout:    30 * time.Second,
					IdleTimeout:       3 * time.Second,
					AllowedOrigins:    nil,
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
				Blockchain: BlockchainConfig{
					ChainID:          84532,
					SafeProxyFactory: "0x4e1DCf7AD4e460CfD30791CCC4F9c8a4f820ec67",
					SafeInitCodeHash: "0x52bede2892dc6ee239117844c91b0bdd458c318980592ab4152f5ea44af17f34",
				},
				VAPID: VAPIDConfig{
					Contact: "mailto:pepperoni9@gmail.com",
				},
				NATS: NATSConfig{},
			},
		},
		{
			name: "load with custom values",
			envVars: map[string]string{
				"ENVIRONMENT":                     "production",
				"SHUTDOWN_TIMEOUT":                "15s",
				"SERVER_PORT":                     "9090",
				"SERVER_HOST":                     "0.0.0.0",
				"SERVER_READ_HEADER_TIMEOUT":      "200ms",
				"SERVER_READ_TIMEOUT":             "2s",
				"SERVER_HANDLER_TIMEOUT":          "10s",
				"SERVER_IDLE_TIMEOUT":             "45s",
				"DATABASE_NAME":                   "testdb",
				"DATABASE_USER":                   "testuser",
				"LOGGING_LEVEL":                   "debug",
				"LOGGING_FORMAT":                  "text",
				"GCP_PROJECT_ID":                  "custom-project",
				"GCP_VERTEX_AI_SEARCH_DATA_STORE": "custom-datastore",
				"OIDC_ISSUER_URL":                 "https://custom-issuer.com",
				"JWKS_REFRESH_INTERVAL":           "30m",
			},
			want: &ServerConfig{
				BaseConfig: BaseConfig{
					Environment:     "production",
					ShutdownTimeout: 15 * time.Second,
					Database: DatabaseConfig{
						Host:              "localhost",
						Port:              5432,
						Name:              "testdb",
						User:              "testuser",
						SSLMode:           "disable",
						Schema:            "app",
						MaxOpenConns:      10,
						MaxIdleConns:      2,
						ConnMaxLifetime:   1800,
						MaxConnIdleTime:   600,
						HealthCheckPeriod: 60,
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
				},
				Server: ServerSettings{
					Port:              9090,
					Host:              "0.0.0.0",
					ReadHeaderTimeout: 200 * time.Millisecond,
					ReadTimeout:       2 * time.Second,
					HandlerTimeout:    10 * time.Second,
					IdleTimeout:       45 * time.Second,
					AllowedOrigins:    nil,
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
				Blockchain: BlockchainConfig{
					ChainID:          84532,
					SafeProxyFactory: "0x4e1DCf7AD4e460CfD30791CCC4F9c8a4f820ec67",
					SafeInitCodeHash: "0x52bede2892dc6ee239117844c91b0bdd458c318980592ab4152f5ea44af17f34",
				},
				VAPID: VAPIDConfig{
					Contact: "mailto:pepperoni9@gmail.com",
				},
				NATS: NATSConfig{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for key, value := range tt.envVars {
				t.Setenv(key, value)
			}

			got, err := Load[ServerConfig]()
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLoad_JobConfig(t *testing.T) {
	t.Run("loads without OIDC_ISSUER_URL", func(t *testing.T) {
		t.Setenv("DATABASE_NAME", "testdb")
		t.Setenv("DATABASE_USER", "testuser")

		got, err := Load[JobConfig]()
		require.NoError(t, err)
		assert.Equal(t, "testdb", got.Database.Name)
		assert.Equal(t, "local", got.Environment)
	})
}

func TestLoad_ConsumerConfig(t *testing.T) {
	t.Run("loads without OIDC_ISSUER_URL", func(t *testing.T) {
		t.Setenv("DATABASE_NAME", "testdb")
		t.Setenv("DATABASE_USER", "testuser")
		t.Setenv("NATS_URL", "nats://localhost:4222")

		got, err := Load[ConsumerConfig]()
		require.NoError(t, err)
		assert.Equal(t, "nats://localhost:4222", got.NATS.URL)
	})
}

func TestServerConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  *ServerConfig
		wantErr bool
	}{
		{
			name: "valid development config",
			config: &ServerConfig{
				BaseConfig: BaseConfig{
					Environment: "development",
					Database: DatabaseConfig{
						Port:                   5432,
						InstanceConnectionName: "project:region:instance",
					},
					Logging: LoggingConfig{Level: "info", Format: "json"},
				},
				Server: ServerSettings{Port: 8080, AllowedOrigins: []string{"http://localhost:9000"}},
				NATS:   NATSConfig{URL: "nats://nats.nats.svc.cluster.local:4222"},
				JWT: JWTConfig{
					Issuer:              "https://test-issuer.com",
					JWKSRefreshInterval: 15 * time.Minute,
				},
			},
			wantErr: false,
		},
		{
			name: "missing connection name in development",
			config: &ServerConfig{
				BaseConfig: BaseConfig{
					Environment: "development",
					Database:    DatabaseConfig{Port: 5432},
					Logging:     LoggingConfig{Level: "info", Format: "json"},
				},
				Server: ServerSettings{Port: 8080},
				JWT: JWTConfig{
					Issuer:              "https://test-issuer.com",
					JWKSRefreshInterval: 15 * time.Minute,
				},
			},
			wantErr: true,
		},
		{
			name: "missing allowed origins in development",
			config: &ServerConfig{
				BaseConfig: BaseConfig{
					Environment: "development",
					Database: DatabaseConfig{
						Port:                   5432,
						InstanceConnectionName: "project:region:instance",
					},
					Logging: LoggingConfig{Level: "info", Format: "json"},
				},
				Server: ServerSettings{Port: 8080},
			},
			wantErr: true,
		},
		{
			name: "missing NATS URL in development",
			config: &ServerConfig{
				BaseConfig: BaseConfig{
					Environment: "development",
					Database: DatabaseConfig{
						Port:                   5432,
						InstanceConnectionName: "project:region:instance",
					},
					Logging: LoggingConfig{Level: "info", Format: "json"},
				},
				Server: ServerSettings{Port: 8080, AllowedOrigins: []string{"http://localhost:9000"}},
				JWT: JWTConfig{
					Issuer:              "https://test-issuer.com",
					JWKSRefreshInterval: 15 * time.Minute,
				},
			},
			wantErr: true,
		},
		{
			name: "valid local config without connection name",
			config: &ServerConfig{
				BaseConfig: BaseConfig{
					Environment: "local",
					Database:    DatabaseConfig{Port: 5432},
					Logging:     LoggingConfig{Level: "info", Format: "json"},
				},
				Server: ServerSettings{Port: 8080},
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

func TestJobConfig_Validate(t *testing.T) {
	t.Run("valid local without NATS", func(t *testing.T) {
		cfg := &JobConfig{
			BaseConfig: BaseConfig{
				Environment: "local",
				Database:    DatabaseConfig{Port: 5432},
				Logging:     LoggingConfig{Level: "info", Format: "json"},
			},
		}
		assert.NoError(t, cfg.Validate())
	})

	t.Run("valid development without NATS", func(t *testing.T) {
		cfg := &JobConfig{
			BaseConfig: BaseConfig{
				Environment: "development",
				Database: DatabaseConfig{
					Port:                   5432,
					InstanceConnectionName: "project:region:instance",
				},
				Logging: LoggingConfig{Level: "info", Format: "json"},
			},
		}
		assert.NoError(t, cfg.Validate())
	})
}

func TestConsumerConfig_Validate(t *testing.T) {
	t.Run("valid local without NATS", func(t *testing.T) {
		cfg := &ConsumerConfig{
			BaseConfig: BaseConfig{
				Environment: "local",
				Database:    DatabaseConfig{Port: 5432},
				Logging:     LoggingConfig{Level: "info", Format: "json"},
			},
		}
		assert.NoError(t, cfg.Validate())
	})

	t.Run("missing NATS URL in development", func(t *testing.T) {
		cfg := &ConsumerConfig{
			BaseConfig: BaseConfig{
				Environment: "development",
				Database: DatabaseConfig{
					Port:                   5432,
					InstanceConnectionName: "project:region:instance",
				},
				Logging: LoggingConfig{Level: "info", Format: "json"},
			},
		}
		assert.Error(t, cfg.Validate())
	})
}
