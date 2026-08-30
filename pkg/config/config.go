// Package config provides application configuration management using environment variables.
// It uses github.com/kelseyhightower/envconfig for loading configuration from environment variables
// with support for validation, default values, and environment-specific helpers.
//
// # Workload-Specific Configuration
//
// Each backend workload loads only the environment variables it needs:
//
//	// API server — loads all fields including JWT and VAPID
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
	"slices"
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

	// GoroutineLeak configures the goroutine-leak detection surface (internal
	// pprof listener + periodic sampler). Shared by all workloads.
	GoroutineLeak GoroutineLeakConfig `envconfig:""`
}

// ServerConfig is the configuration for the API server workload.
type ServerConfig struct {
	BaseConfig

	// Server settings (port, host, timeouts, CORS)
	Server ServerSettings `envconfig:""`

	// Webhook settings — dedicated listener for Zitadel Actions v2 webhooks.
	// Runs on a separate port from the public Connect-RPC server so the
	// webhook paths are physically unreachable via the GKE Gateway.
	Webhook WebhookSettings `envconfig:""`

	// JWT configuration
	JWT JWTConfig `envconfig:""`

	// GCP configuration
	GCP GCPConfig `envconfig:""`

	// NATS configuration for event messaging
	NATS NATSConfig `envconfig:""`

	// VAPID configuration for Web Push notifications
	VAPID VAPIDConfig `envconfig:""`

	// ZitadelMachineKeyForBackendAppPath is the file path to the
	// `backend-app` Zitadel MachineUser's private key JSON, mounted
	// from GSM secret `zitadel-machine-key-for-backend-app`. When empty,
	// the Zitadel API client is disabled (email verification features
	// are unavailable).
	ZitadelMachineKeyForBackendAppPath string `envconfig:"ZITADEL_MACHINE_KEY_FOR_BACKEND_APP_PATH"`

	// ZitadelMachineKeyForOrganizerProvisionerPath is the file path to the
	// `organizer-provisioner` Zitadel MachineUser's private key JSON, mounted
	// from GSM secret `zitadel-machine-key-for-organizer-provisioner`. This
	// credential grants IAM_ORG_MANAGER (tenant org creation + cross-org
	// grants) and is mounted only into the isolated admin workload
	// (`admin-console-api`), never the consumer surface. When empty, organizer
	// tenant provisioning is disabled.
	ZitadelMachineKeyForOrganizerProvisionerPath string `envconfig:"ZITADEL_MACHINE_KEY_FOR_ORGANIZER_PROVISIONER_PATH"`

	// OrganizerConsoleProjectID is the Zitadel `organizer-console` project id,
	// Project-Granted to each Organizer tenant org and used as the role
	// container (the `owner` role) when seeding the initial operator.
	OrganizerConsoleProjectID string `envconfig:"ORGANIZER_CONSOLE_PROJECT_ID"`

	// LastFM API Key
	LastFMAPIKey string `envconfig:"LASTFM_API_KEY"`

	// OrganizerMediaInternalBucket is the GCS bucket that holds raw originals
	// uploaded by organizers (write-only for the API, consumed by the
	// media-processor job). Signed PUT URLs point here.
	OrganizerMediaInternalBucket string `envconfig:"ORGANIZER_MEDIA_INTERNAL_BUCKET"`
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

	// PostHog configuration for product-analytics forwarding by the
	// analytics-consumer.
	PostHog PostHogConfig `envconfig:""`

	// ZitadelDomain is the Zitadel instance URL for API calls.
	// Same value as OIDC_ISSUER_URL used by the API server.
	ZitadelDomain string `envconfig:"OIDC_ISSUER_URL"`

	// ZitadelMachineKeyForBackendAppPath is the file path to the
	// `backend-app` Zitadel MachineUser's private key JSON, mounted
	// from GSM secret `zitadel-machine-key-for-backend-app`. When empty,
	// the email verification consumer skips processing with a warning.
	ZitadelMachineKeyForBackendAppPath string `envconfig:"ZITADEL_MACHINE_KEY_FOR_BACKEND_APP_PATH"`

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

	// Handler timeout is the default safety net for all RPCs.
	// Individual RPC deadlines are controlled by client-side timeoutMs.
	HandlerTimeout time.Duration `envconfig:"SERVER_HANDLER_TIMEOUT" default:"30s"`

	// ConcertHandlerTimeout is the handler timeout for ConcertService RPCs.
	// Gemini API + Google Search grounding takes 25-110s per call, so this
	// must be larger than the default HandlerTimeout.
	ConcertHandlerTimeout time.Duration `envconfig:"SERVER_CONCERT_HANDLER_TIMEOUT" default:"120s"`

	// Idle timeout in seconds
	IdleTimeout time.Duration `envconfig:"SERVER_IDLE_TIMEOUT" default:"3s"`

	// Allowed CORS origins
	AllowedOrigins []string `envconfig:"CORS_ALLOWED_ORIGINS"`

	// AdminPort is the port for the dedicated admin Connect server (admin-scoped
	// RPCs only). It runs as a second listener in the same backend binary on its
	// own ingress host, governed independently of the consumer API.
	AdminPort int `envconfig:"ADMIN_SERVER_PORT" default:"8090"`

	// AdminAllowedOrigins is the CORS allowlist for the admin server (admin
	// origins only), configured independently of AllowedOrigins.
	AdminAllowedOrigins []string `envconfig:"ADMIN_CORS_ALLOWED_ORIGINS"`

	// OrganizerPort is the port for the dedicated organizer Connect server
	// (organizer-facing OrganizerService only). It runs as a third listener in
	// the same backend binary on its own ingress host
	// (api.organizer.{base-domain}), governed independently of the fan and
	// admin servers.
	OrganizerPort int `envconfig:"ORGANIZER_SERVER_PORT" default:"8091"`

	// OrganizerAllowedOrigins is the CORS allowlist for the organizer server
	// (organizer-console origins only), configured independently of other
	// servers' CORS lists.
	OrganizerAllowedOrigins []string `envconfig:"ORGANIZER_CORS_ALLOWED_ORIGINS"`

	// Rate limiting configuration
	RateLimit RateLimitConfig `envconfig:""`
}

// RateLimitConfig holds rate limiting parameters for the API server.
type RateLimitConfig struct {
	// AuthRPS is the sustained request rate for authenticated users (per second).
	AuthRPS float64 `envconfig:"RATE_LIMIT_AUTH_RPS" default:"100"`
	// AuthBurst is the maximum burst size for authenticated users.
	AuthBurst int `envconfig:"RATE_LIMIT_AUTH_BURST" default:"200"`
	// AnonRPS is the sustained request rate for unauthenticated clients (per second).
	AnonRPS float64 `envconfig:"RATE_LIMIT_ANON_RPS" default:"30"`
	// AnonBurst is the maximum burst size for unauthenticated clients.
	AnonBurst int `envconfig:"RATE_LIMIT_ANON_BURST" default:"60"`
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

	// SamplerRatio controls the trace sampling rate (0.0 to 1.0).
	// Uses ParentBased(TraceIDRatioBased) sampler. Default 1.0 samples all traces.
	SamplerRatio float64 `envconfig:"TELEMETRY_SAMPLER_RATIO" default:"1.0"`
}

// GCPConfig represents Google Cloud specific configuration.
type GCPConfig struct {
	// GCP Project ID
	ProjectID string `envconfig:"GCP_PROJECT_ID"`

	// GCP Location (e.g., us-central1)
	Location string `envconfig:"GCP_LOCATION" default:"us-central1"`

	// Gemini Model Name (legacy, fallback when workload-specific vars are unset).
	GeminiModel string `envconfig:"GCP_GEMINI_MODEL" default:"gemini-3-flash-preview"`

	// Per-step model overrides for the two-step grounded-extract concert
	// searcher pipeline. Each unset value falls back to the step-specific
	// default below; defaults are intentionally different per step so
	// there is no shared workload-wide fallback.
	//
	// Step defaults:
	//   - Step 1 (grounded extract, GoogleSearch + URLContext, no schema): gemini-3.6-flash
	//   - Step 2 (JSON coerce, responseJsonSchema, no tools): gemini-3.1-flash-lite
	GeminiSearchModelExtract string `envconfig:"GCP_GEMINI_SEARCH_MODEL_EXTRACT"`
	GeminiSearchModelParse   string `envconfig:"GCP_GEMINI_SEARCH_MODEL_PARSE"`

	// Gemini Model Name for the email parser workload. Empty falls back to GeminiModel.
	GeminiParserModel string `envconfig:"GCP_GEMINI_PARSER_MODEL"`

	// Sampling temperature for the concert searcher's GenerateContent call.
	GeminiSearchTemperature float32 `envconfig:"GCP_GEMINI_SEARCH_TEMPERATURE" default:"1.0"`

	// Thinking level for the concert searcher (Gemini 3 series). Empty leaves the SDK/model
	// default in place. Accepted: "", "low", "medium", "high".
	GeminiSearchThinkingLevel string `envconfig:"GCP_GEMINI_SEARCH_THINKING_LEVEL"`

	// Per-step thinking level overrides for the two-step grounded-extract pipeline.
	// Each unset value falls back to GeminiSearchThinkingLevel. Recommended split
	// (per docs/gemini-concert-searcher-tuning.md §10.7):
	//   - Extract (Step 1, grounded search + URLContext): "medium" or "high"
	//   - Parse   (Step 2, mechanical JSON coercion):     "low"
	// Accepted: "", "low", "medium", "high".
	GeminiSearchThinkingExtract string `envconfig:"GCP_GEMINI_SEARCH_THINKING_EXTRACT"`
	GeminiSearchThinkingParse   string `envconfig:"GCP_GEMINI_SEARCH_THINKING_PARSE"`

	// API key for the Gemini API direct backend (BackendGeminiAPI).
	// REQUIRED for the concert searcher — Vertex AI does not support
	// URLContext or GoogleSearch.TimeRangeFilter, which the two-step
	// grounded-extract pipeline depends on. Get a key at
	// https://aistudio.google.com/apikey and apply API restrictions
	// (Generative Language API only) before 2026-06-19.
	GeminiSearchAPIKey string `envconfig:"GCP_GEMINI_SEARCH_API_KEY"`

	// Freshness window for the search-log cache. A completed search within
	// this window suppresses a repeat external call. Empty/zero falls back
	// to defaultSearchCacheTTL via SearchCacheTTL(); prod sets 72h.
	GeminiSearchCacheTTL time.Duration `envconfig:"GCP_GEMINI_SEARCH_CACHE_TTL"`

	// Skip window after a successful discovery. If a new concert was found
	// for an artist within this window, the external search is skipped
	// (announcements are batch-then-quiet, so re-searching just re-finds the
	// same events). Empty/zero falls back to defaultSearchDiscoveryWindow.
	GeminiSearchDiscoveryWindow time.Duration `envconfig:"GCP_GEMINI_SEARCH_DISCOVERY_WINDOW"`

	// Look-ahead window for the sales-phase discovery job. A series is
	// included in the discovery run when its upcoming events fall within
	// [now, now+window]. Zero falls back to defaultSalesPhaseDiscoveryWindow
	// (90 days — sales phases are announced well ahead of the event).
	SalesPhaseDiscoveryWindow time.Duration `envconfig:"GCP_SALES_PHASE_DISCOVERY_WINDOW"`

	// Look-ahead window for the sales-reminders scan. Phases whose
	// apply_start_at falls within [now, now+window] are included in the
	// reminder evaluation pass. Zero falls back to
	// defaultSalesReminderWindow (7 days).
	SalesReminderWindow time.Duration `envconfig:"GCP_SALES_REMINDER_WINDOW"`
}

// Default models for each step of the two-step grounded-extract search
// pipeline. Step 1 is grounded (search + URLContext) and benefits from
// flash's reliability with those tools; Step 2 is a pure text-to-JSON
// coercion with no tools where lite is cheap and reliable.
const (
	defaultSearchModelExtract = "gemini-3.6-flash"
	defaultSearchModelParse   = "gemini-3.1-flash-lite"
)

// Defaults for the concert-search skip windows. The freshness TTL bounds how
// long a completed search is reused; the discovery window bounds how long a
// fresh discovery suppresses re-searching. Both are env-overridable per
// environment (prod runs a longer TTL); dev/unset inherit these.
const (
	defaultSearchCacheTTL        = 24 * time.Hour
	defaultSearchDiscoveryWindow = 14 * 24 * time.Hour
)

// Defaults for the sales-phase discovery and reminder jobs.
const (
	// defaultSalesPhaseDiscoveryWindow scans series with events in the next
	// 90 days; sales phases are typically announced 1–3 months ahead.
	defaultSalesPhaseDiscoveryWindow = 90 * 24 * time.Hour
	// defaultSalesReminderWindow covers phases whose apply_start_at is within
	// 7 days. Stages due within that horizon are evaluated each scan run.
	defaultSalesReminderWindow = 7 * 24 * time.Hour
)

// SalesPhaseWindow returns the sales-phase discovery look-ahead window.
// Resolution: env override (GCP_SALES_PHASE_DISCOVERY_WINDOW) → built-in default.
func (c *GCPConfig) SalesPhaseWindow() time.Duration {
	if c.SalesPhaseDiscoveryWindow > 0 {
		return c.SalesPhaseDiscoveryWindow
	}
	return defaultSalesPhaseDiscoveryWindow
}

// SalesReminderScanWindow returns the reminder scan look-ahead window.
// Resolution: env override (GCP_SALES_REMINDER_WINDOW) → built-in default.
func (c *GCPConfig) SalesReminderScanWindow() time.Duration {
	if c.SalesReminderWindow > 0 {
		return c.SalesReminderWindow
	}
	return defaultSalesReminderWindow
}

// SearchCacheTTL returns the search-log freshness window. Resolution:
// env override (GCP_GEMINI_SEARCH_CACHE_TTL) → built-in default.
func (c *GCPConfig) SearchCacheTTL() time.Duration {
	if c.GeminiSearchCacheTTL > 0 {
		return c.GeminiSearchCacheTTL
	}
	return defaultSearchCacheTTL
}

// SearchDiscoveryWindow returns the post-discovery skip window. Resolution:
// env override (GCP_GEMINI_SEARCH_DISCOVERY_WINDOW) → built-in default.
func (c *GCPConfig) SearchDiscoveryWindow() time.Duration {
	if c.GeminiSearchDiscoveryWindow > 0 {
		return c.GeminiSearchDiscoveryWindow
	}
	return defaultSearchDiscoveryWindow
}

// SearchModelExtract returns the model name for Step 1 (grounded extract:
// GoogleSearch + URLContext, no schema). Resolution: step-specific env
// override → built-in default.
func (c *GCPConfig) SearchModelExtract() string {
	if c.GeminiSearchModelExtract != "" {
		return c.GeminiSearchModelExtract
	}
	return defaultSearchModelExtract
}

// SearchModelParse returns the model name for Step 2 (JSON coerce with
// responseJsonSchema, no tools). Resolution: step-specific env override →
// built-in default.
func (c *GCPConfig) SearchModelParse() string {
	if c.GeminiSearchModelParse != "" {
		return c.GeminiSearchModelParse
	}
	return defaultSearchModelParse
}

// ParserModel returns the model name for the email parser workload,
// applying the resolution order: workload-specific → legacy → built-in default.
func (c *GCPConfig) ParserModel() string {
	if c.GeminiParserModel != "" {
		return c.GeminiParserModel
	}
	return c.GeminiModel
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

// GoroutineLeakConfig configures goroutine-leak detection (Go 1.27 GA
// `goroutineleak` profile). It exposes two surfaces:
//   - an internal-only pprof HTTP listener (Host:Port) for on-demand full
//     profiles — this listener must never be added to a public Service/ingress;
//   - a periodic in-process sampler that counts leaked goroutines and publishes
//     the `backend_goroutine_leak_count` OTel gauge for alerting.
//
// Disabled by default so local/dev runs and short-lived jobs opt in explicitly;
// long-lived workloads (api, consumer) enable it via env in their overlays.
type GoroutineLeakConfig struct {
	// Enabled turns on the pprof listener and the periodic sampler.
	Enabled bool `envconfig:"GOROUTINE_LEAK_DETECTION_ENABLED" default:"false"`

	// Host to bind the internal pprof listener to. Defaults to loopback so the
	// unauthenticated profiling surface is reachable only from inside the pod
	// (e.g. via `kubectl port-forward`), never from an in-cluster peer — a
	// defense-in-depth complement to the port being absent from any Service.
	Host string `envconfig:"DEBUG_HOST" default:"127.0.0.1"`

	// Port for the internal pprof listener. Distinct from the Connect-RPC,
	// admin, webhook, and health ports; not exposed on any public route.
	Port int `envconfig:"DEBUG_PORT" default:"6060"`

	// SampleInterval is how often the leak-detection GC runs and the gauge is
	// refreshed. Coarse by design: the profile analysis inspects the runtime's
	// goroutine set, and a wedge that matters persists for minutes.
	SampleInterval time.Duration `envconfig:"GOROUTINE_LEAK_SAMPLE_INTERVAL" default:"2m"`
}

// WebhookSettings is the HTTP server configuration for the Zitadel Actions v2
// webhook listener. It runs on a separate port and Service (`server-webhook-svc`)
// from the public Connect-RPC server so the webhook paths are physically
// absent from the GKE Gateway / `server-svc` HTTPRoute.
//
// `PreAccessTokenAudience` pins the `aud` claim the handler's WebhookValidator
// requires. It MUST match the value registered on the corresponding Zitadel
// Target in Pulumi, otherwise every webhook call fails 401 (signature OK,
// audience mismatch).
type WebhookSettings struct {
	// Port to listen on for webhook traffic.
	Port int `envconfig:"WEBHOOK_PORT" default:"9090"`

	// Host to bind to.
	Host string `envconfig:"WEBHOOK_HOST" default:"0.0.0.0"`

	// Read header timeout.
	ReadHeaderTimeout time.Duration `envconfig:"WEBHOOK_READ_HEADER_TIMEOUT" default:"500ms"`

	// Read timeout.
	ReadTimeout time.Duration `envconfig:"WEBHOOK_READ_TIMEOUT" default:"5s"`

	// Idle timeout.
	IdleTimeout time.Duration `envconfig:"WEBHOOK_IDLE_TIMEOUT" default:"30s"`

	// PreAccessTokenAudience is the expected `aud` claim for webhook JWTs
	// delivered to `POST /pre-access-token`. Must match the audience
	// registered on the corresponding Zitadel Target.
	PreAccessTokenAudience string `envconfig:"WEBHOOK_PRE_ACCESS_TOKEN_AUDIENCE" default:"urn:liverty-music:webhook:pre-access-token"`

	// LoginEventSigningKey is the HMAC signing key for the login-event Target
	// (`POST /account-login-event`, the account.login source). The Target uses
	// PAYLOAD_TYPE_JSON; Zitadel signs each body with this key and the handler
	// verifies the `ZITADEL-Signature` header against it. Sourced from a secret
	// (Zitadel generates it at Target creation). Left un-validated/optional so a
	// boot before the secret is synced degrades gracefully (the handler rejects
	// unverifiable requests) rather than crash-looping.
	LoginEventSigningKey string `envconfig:"WEBHOOK_LOGIN_EVENT_SIGNING_KEY"`
}

// MediaJobConfig is the configuration for the media-processor job workload.
// It embeds BaseConfig and adds NATS and the two media bucket names.
type MediaJobConfig struct {
	BaseConfig

	// GCP configuration (project id for IAM SignBlob, etc.)
	GCP GCPConfig `envconfig:""`

	// NATS configuration for event messaging
	NATS NATSConfig `envconfig:""`

	// OrganizerMediaInternalBucket is the GCS bucket that holds the raw originals
	// uploaded by organizers. The processor reads originals from here and deletes
	// them on success.
	OrganizerMediaInternalBucket string `envconfig:"ORGANIZER_MEDIA_INTERNAL_BUCKET"`

	// OrganizerMediaBucket is the GCS bucket that serves CDN-cached WebP variants.
	// The processor writes cdn/{org}/{mediaId}/{variant}.webp objects here.
	OrganizerMediaBucket string `envconfig:"ORGANIZER_MEDIA_BUCKET"`
}

// Validate validates MediaJobConfig including base checks.
func (c *MediaJobConfig) Validate() error {
	if err := c.BaseConfig.Validate(); err != nil {
		return err
	}
	return c.GCP.Validate()
}

// Loadable constrains the config types that can be loaded from environment variables.
type Loadable interface {
	ServerConfig | JobConfig | ConsumerConfig | MediaJobConfig
}

// Load loads configuration from environment variables into the specified workload config type.
func Load[T Loadable]() (*T, error) {
	var cfg T

	if err := envconfig.Process("", &cfg); err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}

	return &cfg, nil
}

// Validate validates the GCPConfig fields:
//   - GeminiSearchThinkingLevel / Extract / Parse: each must be one of "", "low", "medium", "high"
func (c *GCPConfig) Validate() error {
	// validThinkingLevels must mirror the set accepted by
	// gemini.thinkingLevelFromConfig (searcher.go). Keeping these in sync
	// prevents a misconfiguration where the env-var passes Validate but
	// later silently degrades to ThinkingLevelUnspecified at runtime.
	validThinkingLevels := []string{"", "minimal", "low", "medium", "high"}
	if !slices.Contains(validThinkingLevels, c.GeminiSearchThinkingLevel) {
		return fmt.Errorf("invalid GCP_GEMINI_SEARCH_THINKING_LEVEL: %q (allowed: \"\", minimal, low, medium, high)", c.GeminiSearchThinkingLevel)
	}
	if !slices.Contains(validThinkingLevels, c.GeminiSearchThinkingExtract) {
		return fmt.Errorf("invalid GCP_GEMINI_SEARCH_THINKING_EXTRACT: %q (allowed: \"\", minimal, low, medium, high)", c.GeminiSearchThinkingExtract)
	}
	if !slices.Contains(validThinkingLevels, c.GeminiSearchThinkingParse) {
		return fmt.Errorf("invalid GCP_GEMINI_SEARCH_THINKING_PARSE: %q (allowed: \"\", minimal, low, medium, high)", c.GeminiSearchThinkingParse)
	}
	// A non-parseable duration already fails at envconfig.Process (Load);
	// here we reject negatives so a stray "-1h" cannot disable the cache.
	if c.GeminiSearchCacheTTL < 0 {
		return fmt.Errorf("invalid GCP_GEMINI_SEARCH_CACHE_TTL: %s (must be >= 0)", c.GeminiSearchCacheTTL)
	}
	if c.GeminiSearchDiscoveryWindow < 0 {
		return fmt.Errorf("invalid GCP_GEMINI_SEARCH_DISCOVERY_WINDOW: %s (must be >= 0)", c.GeminiSearchDiscoveryWindow)
	}
	return nil
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
	valid := slices.Contains(validEnvironments, c.Environment)

	if !valid {
		return fmt.Errorf("invalid environment: %s", c.Environment)
	}

	validLogLevels := []string{"debug", "info", "warn", "error"}
	valid = slices.Contains(validLogLevels, c.Logging.Level)

	if !valid {
		return fmt.Errorf("invalid log level: %s", c.Logging.Level)
	}

	validLogFormats := []string{"json", "text"}
	valid = slices.Contains(validLogFormats, c.Logging.Format)

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

	if err := c.GCP.Validate(); err != nil {
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

	if c.Webhook.Port <= 0 || c.Webhook.Port > 65535 {
		return fmt.Errorf("invalid webhook port: %d", c.Webhook.Port)
	}

	if c.Webhook.Port == c.Server.Port {
		return fmt.Errorf("webhook port %d must differ from server port to keep webhook listener off the public Gateway", c.Webhook.Port)
	}

	if c.Server.OrganizerPort <= 0 || c.Server.OrganizerPort > 65535 {
		return fmt.Errorf("invalid organizer server port: %d", c.Server.OrganizerPort)
	}

	// All four ports (fan, admin, organizer, webhook) must be distinct.
	ports := []int{c.Server.Port, c.Server.AdminPort, c.Server.OrganizerPort, c.Webhook.Port}
	seen := make(map[int]bool, len(ports))
	for _, p := range ports {
		if seen[p] {
			return fmt.Errorf("all server ports (fan %d, admin %d, organizer %d, webhook %d) must be unique",
				c.Server.Port, c.Server.AdminPort, c.Server.OrganizerPort, c.Webhook.Port)
		}
		seen[p] = true
	}

	if !c.IsLocal() && len(c.Server.OrganizerAllowedOrigins) == 0 {
		return fmt.Errorf("organizer CORS allowed origins are required for non-local environments")
	}

	if c.Webhook.PreAccessTokenAudience == "" {
		return fmt.Errorf("webhook pre-access-token audience is required")
	}

	return nil
}

// Validate validates JobConfig including base checks.
// NATS URL is optional because not all jobs require event messaging
// (e.g., artist-image-sync only needs database access).
func (c *JobConfig) Validate() error {
	if err := c.BaseConfig.Validate(); err != nil {
		return err
	}
	return c.GCP.Validate()
}

// Validate validates ConsumerConfig including base checks plus NATS URL for non-local environments.
func (c *ConsumerConfig) Validate() error {
	if err := c.BaseConfig.Validate(); err != nil {
		return err
	}

	if err := c.GCP.Validate(); err != nil {
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

// PostHogConfig holds the credentials required by the PostHog product-
// analytics client. When ProjectAPIKey is empty (or whitespace-only),
// the analytics-consumer logs a warning and acknowledges messages
// without forwarding — matching the optional-dependency pattern used
// by VAPID and Zitadel in local development.
type PostHogConfig struct {
	// APIHost is the PostHog ingestion endpoint. Defaults to the EU
	// Cloud endpoint, which is the only production destination per
	// the introduce-analytics-tool OpenSpec change.
	APIHost string `envconfig:"POSTHOG_API_HOST" default:"https://eu.i.posthog.com"`

	// ProjectAPIKey is the PostHog public project API key. Sourced
	// from GCP Secret Manager via the workload ConfigMap; left empty
	// in local development to disable forwarding.
	ProjectAPIKey string `envconfig:"POSTHOG_PROJECT_API_KEY"`

	// PersonalAPIKey is the PostHog personal API key required for local
	// feature-flag evaluation (periodic flag-definition sync). Unlike
	// ProjectAPIKey it is a private credential and MUST be sourced from a
	// secret, never a plaintext ConfigMap. Left empty in local
	// development and whenever backend feature flags are not in use, in
	// which case the FeatureFlagEvaluator is not constructed and all
	// flags resolve to their call-site defaults.
	PersonalAPIKey string `envconfig:"POSTHOG_PERSONAL_API_KEY"`
}
