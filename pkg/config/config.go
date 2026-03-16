// Package config provides application configuration management using environment variables.
// It uses github.com/kelseyhightower/envconfig for loading configuration from environment variables
// with support for validation, default values, and environment-specific helpers.
//
// # Workload-Specific Configuration
//
// Each backend workload loads only the environment variables it needs:
//
//	// API server — loads all fields including JWT, Blockchain, ZKP
//	cfg, err := config.Load[config.ServerConfig]()
//
//	// CronJob — loads base fields plus GCP and NATS
//	cfg, err := config.Load[config.JobConfig]()
//
//	// Event consumer — loads base fields plus NATS, VAPID, Google Maps
//	cfg, err := config.Load[config.ConsumerConfig]()
//
// # BaseConfig
//
// All workload configs embed BaseConfig which provides:
//   - ENVIRONMENT: Environment (local, development, staging, production)
//   - SHUTDOWN_TIMEOUT: Graceful shutdown timeout (default: 30s)
//   - DATABASE_*: Database connection settings
//   - LOGGING_*: Log level and format
//   - TELEMETRY_*: OpenTelemetry tracing
//
// # Validation
//
// Each config type implements Validate() with workload-appropriate checks:
//
//	if err := cfg.Validate(); err != nil {
//		log.Fatalf("Invalid configuration: %v", err)
//	}
package config

import (
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// BaseConfig contains fields shared by all backend workloads.
type BaseConfig struct {
	// Environment
	Environment string `envconfig:"ENVIRONMENT" default:"local"`

	// Shutdown timeout
	ShutdownTimeout time.Duration `envconfig:"SHUTDOWN_TIMEOUT" default:"30s"`

	// Logging configuration
	Logging LoggingConfig `envconfig:""`

	// Database configuration
	Database DatabaseConfig `envconfig:""`

	// Telemetry configuration
	Telemetry TelemetryConfig `envconfig:""`
}

// ServerConfig is the configuration for the API server workload.
type ServerConfig struct {
	BaseConfig

	// Server settings (port, host, timeouts, CORS)
	Server ServerSettings `envconfig:""`

	// JWT configuration
	JWT JWTConfig `envconfig:""`

	// GCP configuration
	GCP GCPConfig `envconfig:""`

	// NATS configuration for event messaging
	NATS NATSConfig `envconfig:""`

	// VAPID configuration for Web Push notifications
	VAPID VAPIDConfig `envconfig:""`

	// Blockchain configuration
	Blockchain BlockchainConfig `envconfig:""`

	// ZKP configuration
	ZKP ZKPConfig `envconfig:""`

	// LastFM API Key
	LastFMAPIKey string `envconfig:"LASTFM_API_KEY"`
}

// JobConfig is the configuration for batch job workloads (e.g., concert-discovery CronJob).
type JobConfig struct {
	BaseConfig

	// GCP configuration
	GCP GCPConfig `envconfig:""`

	// NATS configuration for event messaging
	NATS NATSConfig `envconfig:""`

	// FanartTV API Key for artist image sync job
	FanartTVAPIKey string `envconfig:"FANARTTV_API_KEY"`
}

// ConsumerConfig is the configuration for the event consumer workload.
type ConsumerConfig struct {
	BaseConfig

	// GCP configuration
	GCP GCPConfig `envconfig:""`

	// NATS configuration for event messaging
	NATS NATSConfig `envconfig:""`

	// VAPID configuration for Web Push notifications
	VAPID VAPIDConfig `envconfig:""`

	// FanartTV API Key for artist image resolution
	FanartTVAPIKey string `envconfig:"FANARTTV_API_KEY"`
}

// ServerSettings represents HTTP server settings (port, host, timeouts, CORS).
type ServerSettings struct {
	// Port to listen on
	Port int `envconfig:"SERVER_PORT" default:"8080"`

	// Host to bind to
	Host string `envconfig:"SERVER_HOST" default:"localhost"`

	// Read header timeout in milliseconds
	ReadHeaderTimeout time.Duration `envconfig:"SERVER_READ_HEADER_TIMEOUT" default:"500ms"`

	// Read timeout in milliseconds
	ReadTimeout time.Duration `envconfig:"SERVER_READ_TIMEOUT" default:"1000ms"`

	// Handler timeout is an insurance safety net for all RPCs.
	// Individual RPC deadlines are controlled by client-side timeoutMs.
	HandlerTimeout time.Duration `envconfig:"SERVER_HANDLER_TIMEOUT" default:"30s"`

	// Idle timeout in seconds
	IdleTimeout time.Duration `envconfig:"SERVER_IDLE_TIMEOUT" default:"3s"`

	// Allowed CORS origins
	AllowedOrigins []string `envconfig:"CORS_ALLOWED_ORIGINS"`
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

	// Database schema (sets search_path in DSN)
	Schema string `envconfig:"DATABASE_SCHEMA" default:"app"`

	// Maximum number of open connections to the database.
	// Default 10 is conservative enough for multi-pod deployments against
	// small instances (e.g., Cloud SQL db-f1-micro with max_connections=25).
	// Override per environment via DATABASE_MAX_OPEN_CONNS.
	MaxOpenConns int `envconfig:"DATABASE_MAX_OPEN_CONNS" default:"10"`

	// Minimum number of idle connections maintained in the pool.
	// Keeps a small warm pool to avoid connection setup latency on first queries
	// after idle periods. Maps to pgxpool MinConns.
	MaxIdleConns int `envconfig:"DATABASE_MAX_IDLE_CONNS" default:"2"`

	// Maximum lifetime of a connection in seconds before it is closed and replaced.
	// Set to 30 minutes (1800s) to ensure periodic recycling for server-side resource
	// hygiene and graceful handling of Cloud SQL maintenance events.
	// Note: With Cloud SQL IAM auth, the connector auto-refreshes tokens for new
	// connections, so this does not need to be shorter than the 60-minute token lifetime.
	ConnMaxLifetime int `envconfig:"DATABASE_CONN_MAX_LIFETIME" default:"1800"`

	// Maximum time in seconds a connection can be idle before it is closed.
	// Connections beyond MinConns are released after this duration, freeing DB
	// connection slots for other workloads. Set to 10 minutes (600s) to balance
	// slot efficiency with avoiding excessive reconnection churn.
	MaxConnIdleTime int `envconfig:"DATABASE_MAX_CONN_IDLE_TIME" default:"600"`

	// Interval in seconds between health checks on idle connections.
	// Detects and removes stale connections caused by Cloud SQL restarts or
	// network interruptions. Matches pgxpool default of 1 minute.
	HealthCheckPeriod int `envconfig:"DATABASE_HEALTH_CHECK_PERIOD" default:"60"`

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

// BlockchainConfig holds configuration for EVM interactions and the TicketSBT contract.
type BlockchainConfig struct {
	// RPCURL is the JSON-RPC endpoint URL for the target EVM chain.
	RPCURL string `envconfig:"BLOCKCHAIN_RPC_URL"`

	// ChainID is the EIP-155 chain ID used for transaction signing.
	// Examples: 84532 (Base Sepolia), 8453 (Base Mainnet).
	ChainID int64 `envconfig:"CHAIN_ID" default:"84532"`

	// DeployerPrivateKey is the hex-encoded private key of the backend service EOA
	// that holds MINTER_ROLE on the TicketSBT contract.
	DeployerPrivateKey string `envconfig:"BLOCKCHAIN_DEPLOYER_PRIVATE_KEY"`

	// TicketSBTAddress is the deployed TicketSBT contract address.
	TicketSBTAddress string `envconfig:"TICKET_SBT_ADDRESS"`

	// SafeProxyFactory is the canonical Safe{Wallet} ProxyFactory contract address.
	// Default: Safe v1.4.1 canonical deployment on all EVM chains.
	SafeProxyFactory string `envconfig:"SAFE_PROXY_FACTORY" default:"0x4e1DCf7AD4e460CfD30791CCC4F9c8a4f820ec67"`

	// SafeInitCodeHash is keccak256(SafeProxy creation bytecode ++ abi.encode(Safe singleton)).
	// Default: Safe v1.4.1 canonical init code hash.
	SafeInitCodeHash string `envconfig:"SAFE_INIT_CODE_HASH" default:"0x52bede2892dc6ee239117844c91b0bdd458c318980592ab4152f5ea44af17f34"`
}

// VAPIDConfig holds the Web Push VAPID key pair and contact information.
type VAPIDConfig struct {
	// PublicKey is the VAPID public key used by the browser to identify the push service.
	PublicKey string `envconfig:"VAPID_PUBLIC_KEY"`

	// PrivateKey is the VAPID private key used to sign push notification requests.
	PrivateKey string `envconfig:"VAPID_PRIVATE_KEY"`

	// Contact is the mailto: URI sent to push services for administrative contact.
	Contact string `envconfig:"VAPID_CONTACT" default:"mailto:pepperoni9@gmail.com"`
}

// ZKPConfig holds configuration for zero-knowledge proof verification.
type ZKPConfig struct {
	// VerificationKeyPath is the file path to the snarkjs verification_key.json.
	// When empty, ZKP-based entry verification is disabled.
	VerificationKeyPath string `envconfig:"ZKP_VERIFICATION_KEY_PATH"`
}

// NATSConfig holds configuration for NATS JetStream event messaging.
type NATSConfig struct {
	// URL is the NATS server connection URL.
	// For local development, leave empty to use Watermill GoChannel instead.
	URL string `envconfig:"NATS_URL"`
}

// JWTConfig represents JWT authentication configuration.
type JWTConfig struct {
	// OIDC Issuer URL (e.g., https://your-zitadel-instance.com)
	Issuer string `envconfig:"OIDC_ISSUER_URL" required:"true"`

	// AcceptedIssuers is an optional comma-separated list of additional accepted JWT issuers.
	// When set, tokens from any listed issuer are accepted in addition to Issuer.
	// Use this during Option C migration to accept tokens from a second identity provider.
	// If empty, only Issuer is accepted.
	AcceptedIssuers []string `envconfig:"JWT_ACCEPTED_ISSUERS"`

	// JWKS refresh interval for key rotation
	JWKSRefreshInterval time.Duration `envconfig:"JWKS_REFRESH_INTERVAL" default:"15m"`
}

// Loadable constrains the config types that can be loaded from environment variables.
type Loadable interface {
	ServerConfig | JobConfig | ConsumerConfig
}

// Load loads configuration from environment variables into the specified workload config type.
func Load[T Loadable]() (*T, error) {
	var cfg T

	if err := envconfig.Process("", &cfg); err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}

	return &cfg, nil
}

// Validate validates BaseConfig fields shared by all workloads:
//   - Database port: 1-65535 range
//   - Environment: local, development, staging, or production
//   - Log level: debug, info, warn, or error
//   - Log format: json or text
//   - Database instance connection name: required for non-local environments
func (c *BaseConfig) Validate() error {
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

// Validate validates ServerConfig including base checks plus server-specific rules:
//   - Server port: 1-65535 range
//   - CORS allowed origins: required for non-local environments
//   - NATS URL: required for non-local environments
//   - JWT issuer: required
//   - JWKS refresh interval: must be positive
func (c *ServerConfig) Validate() error {
	if err := c.BaseConfig.Validate(); err != nil {
		return err
	}

	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}

	if !c.IsLocal() && len(c.Server.AllowedOrigins) == 0 {
		return fmt.Errorf("CORS allowed origins are required for non-local environments")
	}

	if !c.IsLocal() && c.NATS.URL == "" {
		return fmt.Errorf("NATS URL is required for non-local environments")
	}

	if c.JWT.Issuer == "" {
		return fmt.Errorf("JWT issuer is required")
	}

	if c.JWT.JWKSRefreshInterval <= 0 {
		return fmt.Errorf("JWT JWKS refresh interval must be positive")
	}

	return nil
}

// Validate validates JobConfig including base checks.
// NATS URL is optional because not all jobs require event messaging
// (e.g., artist-image-sync only needs database access).
func (c *JobConfig) Validate() error {
	return c.BaseConfig.Validate()
}

// Validate validates ConsumerConfig including base checks plus NATS URL for non-local environments.
func (c *ConsumerConfig) Validate() error {
	if err := c.BaseConfig.Validate(); err != nil {
		return err
	}

	if !c.IsLocal() && c.NATS.URL == "" {
		return fmt.Errorf("NATS URL is required for non-local environments")
	}

	return nil
}

// GetDSN returns the database connection string.
func (c DatabaseConfig) GetDSN() string {
	dsn := fmt.Sprintf("host=%s port=%d user=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Name, c.SSLMode)
	if c.Schema != "" {
		dsn += fmt.Sprintf(" search_path=%s,public", c.Schema)
	}
	return dsn
}

// IsDevelopment returns true if the environment is "development".
func (c *BaseConfig) IsDevelopment() bool {
	return c.Environment == "development"
}

// IsProduction returns true if the environment is "production".
func (c *BaseConfig) IsProduction() bool {
	return c.Environment == "production"
}

// IsStaging returns true if the environment is "staging".
func (c *BaseConfig) IsStaging() bool {
	return c.Environment == "staging"
}

// IsLocal returns true if the environment is "local".
func (c *BaseConfig) IsLocal() bool {
	return c.Environment == "local"
}
