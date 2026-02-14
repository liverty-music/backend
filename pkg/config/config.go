// Package config provides application configuration management using environment variables.
// It uses github.com/kelseyhightower/envconfig for loading configuration from environment variables
// with support for validation, default values, and environment-specific helpers.
//
// # Basic Usage
//
// Load configuration from environment variables:
//
//	cfg, err := config.Load("APP")
//	if err != nil {
//		log.Fatalf("Failed to load configuration: %v", err)
//	}
//
//	// Validate configuration
//	if err := cfg.Validate(); err != nil {
//		log.Fatalf("Invalid configuration: %v", err)
//	}
//
// # Environment Variables
//
// The following environment variables are supported (using "APP" prefix):
//
// Basic configuration:
//   - APP_ENVIRONMENT: Environment (development, staging, production)
//   - APP_DEBUG: Debug mode (true/false)
//
// Server configuration:
//   - APP_SERVER_PORT: Server port (default: 8080)
//   - APP_SERVER_HOST: Server host (default: localhost)
//   - APP_SERVER_READ_TIMEOUT: Read timeout in seconds (default: 30)
//   - APP_SERVER_WRITE_TIMEOUT: Write timeout in seconds (default: 30)
//   - APP_SERVER_IDLE_TIMEOUT: Idle timeout in seconds (default: 60)
//   - APP_SERVER_SHUTDOWN_TIMEOUT: Shutdown timeout in seconds (default: 30)
//
// Database configuration:
//   - APP_DATABASE_HOST: Database host (default: localhost)
//   - APP_DATABASE_PORT: Database port (default: 5432)
//   - APP_DATABASE_NAME: Database name (required)
//   - APP_DATABASE_USER: Database user (required)
//   - APP_DATABASE_PASSWORD: Database password (required)
//   - APP_DATABASE_SSL_MODE: SSL mode (default: disable)
//   - APP_DATABASE_MAX_OPEN_CONNS: Maximum open connections (default: 25)
//   - APP_DATABASE_MAX_IDLE_CONNS: Maximum idle connections (default: 5)
//   - APP_DATABASE_CONN_MAX_LIFETIME: Connection max lifetime in seconds (default: 300)
//
// Logging configuration:
//   - APP_LOGGING_LEVEL: Log level (debug, info, warn, error, default: info)
//   - APP_LOGGING_FORMAT: Log format (json, text, default: json)
//   - APP_LOGGING_STRUCTURED: Enable structured logging (default: true)
//   - APP_LOGGING_INCLUDE_CALLER: Include caller information (default: false)
//
// Telemetry configuration:
//   - APP_TELEMETRY_OTLP_ENDPOINT: OTLP exporter endpoint for sending traces
//   - APP_TELEMETRY_SERVICE_NAME: Service name for tracing (default: go-backend-scaffold)
//   - APP_TELEMETRY_SERVICE_VERSION: Service version for tracing (default: 1.0.0)
//
// # Environment Helpers
//
// Use environment detection helpers:
//
//	if cfg.IsDevelopment() {
//		// Development-specific logic
//	}
//
//	if cfg.IsProduction() {
//		// Production-specific logic
//	}
//
// # Database Connection
//
// Get database connection string:
//
//	dsn := cfg.Database.GetDSN()
//	// Returns: "postgres://user:pass@host:port/dbname?sslmode=disable"
package config

import (
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// Config represents the application configuration loaded from environment variables.
type Config struct {
	// Server configuration
	Server ServerConfig

	// Database configuration
	Database DatabaseConfig

	// Logging configuration
	Logging LoggingConfig

	// Telemetry configuration
	Telemetry TelemetryConfig

	// GCP configuration
	GCP GCPConfig

	// Environment
	Environment string `envconfig:"ENVIRONMENT" default:"local"`

	// Shutdown timeout in seconds
	ShutdownTimeout time.Duration `envconfig:"SHUTDOWN_TIMEOUT" default:"30s"`
}

// ServerConfig represents server-specific configuration.
type ServerConfig struct {
	// Port to listen on
	Port int `envconfig:"CONNECT_SERVER_PORT" default:"8080"`

	// Host to bind to
	Host string `envconfig:"SERVER_HOST" default:"localhost"`

	// Read header timeout in milliseconds
	ReadHeaderTimeout time.Duration `envconfig:"SERVER_READ_HEADER_TIMEOUT" default:"500ms"`

	// Read timeout in milliseconds
	ReadTimeout time.Duration `envconfig:"SERVER_READ_TIMEOUT" default:"1000ms"`

	// Handler timeout in seconds
	HandlerTimeout time.Duration `envconfig:"SERVER_HANDLER_TIMEOUT" default:"5s"`

	// Idle timeout in seconds
	IdleTimeout time.Duration `envconfig:"SERVER_IDLE_TIMEOUT" default:"3s"`

	// Allowed CORS origins
	AllowedOrigins []string `envconfig:"CORS_ALLOWED_ORIGINS" default:"http://localhost:9000"`
}

// DatabaseConfig represents database-specific configuration.
type DatabaseConfig struct {
	// Database host
	Host string `envconfig:"DATABASE_HOST" default:"localhost"`

	// Database port
	Port int `envconfig:"DATABASE_PORT" default:"5432"`

	// Database name
	Name string `envconfig:"DATABASE_NAME" required:"true"`

	// Database user
	User string `envconfig:"DATABASE_USER" required:"true"`

	// Database SSL mode
	SSLMode string `envconfig:"DATABASE_SSL_MODE" default:"disable"`

	// Connection pool settings
	MaxOpenConns    int `envconfig:"DATABASE_MAX_OPEN_CONNS" default:"25"`
	MaxIdleConns    int `envconfig:"DATABASE_MAX_IDLE_CONNS" default:"5"`
	ConnMaxLifetime int `envconfig:"DATABASE_CONN_MAX_LIFETIME" default:"300"`

	// Instance Connection Name (e.g., project:region:instance)
	// Required for Cloud SQL Connector (non-local environments)
	InstanceConnectionName string `envconfig:"DATABASE_INSTANCE_CONNECTION_NAME"`
}

// LoggingConfig represents logging-specific configuration.
type LoggingConfig struct {
	// Log level (debug, info, warn, error)
	Level string `envconfig:"LOGGING_LEVEL" default:"info"`

	// Log format (json, text)
	Format string `envconfig:"LOGGING_FORMAT" default:"json"`

	// Enable structured logging
	Structured bool `envconfig:"LOGGING_STRUCTURED" default:"true"`

	// Include caller information
	IncludeCaller bool `envconfig:"LOGGING_INCLUDE_CALLER" default:"false"`
}

// TelemetryConfig represents telemetry-specific configuration.
type TelemetryConfig struct {
	// OTLP exporter endpoint for sending traces
	OTLPEndpoint string `envconfig:"TELEMETRY_OTLP_ENDPOINT"`

	// Service name for tracing
	ServiceName string `envconfig:"TELEMETRY_SERVICE_NAME" default:"go-backend-scaffold"`

	// Service version for tracing
	ServiceVersion string `envconfig:"TELEMETRY_SERVICE_VERSION" default:"1.0.0"`
}

// GCPConfig represents Google Cloud specific configuration.
type GCPConfig struct {
	// GCP Project ID
	ProjectID string `envconfig:"GCP_PROJECT_ID"`

	// GCP Location (e.g., us-central1)
	Location string `envconfig:"GCP_LOCATION" default:"us-central1"`

	// Gemini Model Name
	GeminiModel string `envconfig:"GCP_GEMINI_MODEL" default:"gemini-3-flash-preview"`

	// Vertex AI Search Data Store ID (full resource name)
	// Format: projects/{project}/locations/global/collections/default_collection/dataStores/{data_store_id}
	VertexAISearchDataStore string `envconfig:"GCP_VERTEX_AI_SEARCH_DATA_STORE"`
}

// Load loads configuration from environment variables.
// The prefix parameter is used to namespace environment variables.
// For example, with prefix "APP", environment variables like APP_SERVER_PORT will be loaded.
//
// Example:
//
//	cfg, err := config.Load("APP")
//	if err != nil {
//		return fmt.Errorf("failed to load config: %w", err)
//	}
func Load(prefix string) (*Config, error) {
	var cfg Config

	// Process environment variables with the given prefix
	err := envconfig.Process(prefix, &cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}

	return &cfg, nil
}

// Validate validates the configuration according to the following rules:
//   - Server port: 1-65535 range
//   - Database port: 1-65535 range
//   - Environment: development, staging, or production
//   - Log level: debug, info, warn, or error
//   - Log format: json or text
//   - Required fields: Database name, user, and password
func (c *Config) Validate() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}

	if c.Database.Port <= 0 || c.Database.Port > 65535 {
		return fmt.Errorf("invalid database port: %d", c.Database.Port)
	}

	validEnvironments := []string{"local", "development", "staging", "production"}
	valid := false

	for _, env := range validEnvironments {
		if c.Environment == env {
			valid = true

			break
		}
	}

	if !valid {
		return fmt.Errorf("invalid environment: %s", c.Environment)
	}

	validLogLevels := []string{"debug", "info", "warn", "error"}
	valid = false

	for _, level := range validLogLevels {
		if c.Logging.Level == level {
			valid = true

			break
		}
	}

	if !valid {
		return fmt.Errorf("invalid log level: %s", c.Logging.Level)
	}

	validLogFormats := []string{"json", "text"}
	valid = false

	for _, format := range validLogFormats {
		if c.Logging.Format == format {
			valid = true

			break
		}
	}

	if !valid {
		return fmt.Errorf("invalid log format: %s", c.Logging.Format)
	}

	if !c.IsLocal() && c.Database.InstanceConnectionName == "" {
		return fmt.Errorf("database instance connection name is required for non-local environments")
	}

	return nil
}

// GetDSN returns the database connection string.
func (c DatabaseConfig) GetDSN() string {
	return fmt.Sprintf("host=%s port=%d user=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Name, c.SSLMode)
}

// IsDevelopment returns true if the environment is "development".
func (c *Config) IsDevelopment() bool {
	return c.Environment == "development"
}

// IsProduction returns true if the environment is "production".
func (c *Config) IsProduction() bool {
	return c.Environment == "production"
}

// IsStaging returns true if the environment is "staging".
func (c *Config) IsStaging() bool {
	return c.Environment == "staging"
}

// IsLocal returns true if the environment is "local".
func (c *Config) IsLocal() bool {
	return c.Environment == "local"
}
