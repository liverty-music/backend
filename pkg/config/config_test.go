package config

import (
	"testing"
	"time"

	"github.com/kelseyhightower/envconfig"
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
				"APP_DATABASE_NAME":     "defaultdb",
				"APP_DATABASE_USER":     "defaultuser",
				"APP_DATABASE_PASSWORD": "defaultpass",
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
			},
			wantErr: nil,
		},
		{
			name:   "load with custom values",
			prefix: "APP",
			envVars: map[string]string{
				"APP_ENVIRONMENT":                "production",
				"APP_DEBUG":                      "true",
				"APP_SHUTDOWN_TIMEOUT":           "15s",
				"APP_SERVER_PORT":                "9090",
				"APP_SERVER_HOST":                "0.0.0.0",
				"APP_SERVER_READ_HEADER_TIMEOUT": "200ms",
				"APP_SERVER_READ_TIMEOUT":        "2s",
				"APP_SERVER_HANDLER_TIMEOUT":     "10s",
				"APP_SERVER_IDLE_TIMEOUT":        "45s",
				"APP_DATABASE_NAME":              "testdb",
				"APP_DATABASE_USER":              "testuser",
				"APP_DATABASE_PASSWORD":          "testpass",
				"APP_LOGGING_LEVEL":              "debug",
				"APP_LOGGING_FORMAT":             "text",
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
			},
			wantErr: nil,
		},
		{
			name:   "missing required database fields",
			prefix: "APP",
			envVars: map[string]string{
				"APP_DATABASE_NAME": "testdb",
				// Missing USER and PASSWORD
			},
			want:    nil,
			wantErr: &envconfig.ParseError{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment variables
			for key, value := range tt.envVars {
				t.Setenv(key, value)
			}

			got, err := Load(tt.prefix)
			if tt.wantErr != nil {
				assert.Error(t, err)
				assert.ErrorAs(t, err, &tt.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{
			name: "valid configuration",
			config: &Config{
				Environment: "development",
				Server: ServerConfig{
					Port: 8080,
				},
				Database: DatabaseConfig{
					Port: 5432,
				},
				Logging: LoggingConfig{
					Level:  "info",
					Format: "json",
				},
			},
		},
		{
			name: "invalid server port",
			config: &Config{
				Environment: "development",
				Server: ServerConfig{
					Port: 70000, // Invalid port
				},
				Database: DatabaseConfig{
					Port: 5432,
				},
				Logging: LoggingConfig{
					Level:  "info",
					Format: "json",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid database port",
			config: &Config{
				Environment: "development",
				Server: ServerConfig{
					Port: 8080,
				},
				Database: DatabaseConfig{
					Port: 70000, // Invalid port
				},
				Logging: LoggingConfig{
					Level:  "info",
					Format: "json",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid environment",
			config: &Config{
				Environment: "invalid",
				Server: ServerConfig{
					Port: 8080,
				},
				Database: DatabaseConfig{
					Port: 5432,
				},
				Logging: LoggingConfig{
					Level:  "info",
					Format: "json",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid log level",
			config: &Config{
				Environment: "development",
				Server: ServerConfig{
					Port: 8080,
				},
				Database: DatabaseConfig{
					Port: 5432,
				},
				Logging: LoggingConfig{
					Level:  "invalid",
					Format: "json",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid log format",
			config: &Config{
				Environment: "development",
				Server: ServerConfig{
					Port: 8080,
				},
				Database: DatabaseConfig{
					Port: 5432,
				},
				Logging: LoggingConfig{
					Level:  "info",
					Format: "invalid",
				},
			},
			wantErr: true,
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

func TestDatabaseConfig_GetDSN(t *testing.T) {
	dbConfig := DatabaseConfig{
		Host:     "localhost",
		Port:     5432,
		User:     "testuser",
		Password: "testpass",
		Name:     "testdb",
		SSLMode:  "disable",
	}

	expected := "postgres://testuser:testpass@localhost:5432/testdb?sslmode=disable"
	assert.Equal(t, expected, dbConfig.GetDSN())
}

func TestConfig_EnvironmentHelpers(t *testing.T) {
	tests := []struct {
		name        string
		environment string
		isDev       bool
		isStaging   bool
		isProd      bool
	}{
		{
			name:        "development environment",
			environment: "development",
			isDev:       true,
			isStaging:   false,
			isProd:      false,
		},
		{
			name:        "staging environment",
			environment: "staging",
			isDev:       false,
			isStaging:   true,
			isProd:      false,
		},
		{
			name:        "production environment",
			environment: "production",
			isDev:       false,
			isStaging:   false,
			isProd:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Environment: tt.environment}
			assert.Equal(t, tt.isDev, cfg.IsDevelopment())
			assert.Equal(t, tt.isStaging, cfg.IsStaging())
			assert.Equal(t, tt.isProd, cfg.IsProduction())
		})
	}
}
