package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validWebhookSettings returns a WebhookSettings that passes Validate.
// Port is 9090 (distinct from the default server port of 8080) and the
// audience is the production default.
func validWebhookSettings() WebhookSettings {
	return WebhookSettings{
		Port:                   9090,
		PreAccessTokenAudience: "urn:liverty-music:webhook:pre-access-token",
	}
}

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
					SamplerRatio:   1.0,
				},
				GoroutineLeak: GoroutineLeakConfig{
					Enabled:        false,
					Host:           "127.0.0.1",
					Port:           6060,
					SampleInterval: 2 * time.Minute,
				},
				Server: ServerSettings{
					Port:                    8080,
					Host:                    "localhost",
					ReadHeaderTimeout:       500 * time.Millisecond,
					ReadTimeout:             1 * time.Second,
					HandlerTimeout:          30 * time.Second,
					ConcertHandlerTimeout:   120 * time.Second,
					IdleTimeout:             3 * time.Second,
					AllowedOrigins:          nil,
					AdminPort:               8090,
					AdminAllowedOrigins:     nil,
					OrganizerPort:           8091,
					OrganizerAllowedOrigins: nil,
					RateLimit:               RateLimitConfig{AuthRPS: 100, AuthBurst: 200, AnonRPS: 30, AnonBurst: 60},
				},
				Webhook: WebhookSettings{
					Port:                   9090,
					Host:                   "0.0.0.0",
					ReadHeaderTimeout:      500 * time.Millisecond,
					ReadTimeout:            5 * time.Second,
					IdleTimeout:            30 * time.Second,
					PreAccessTokenAudience: "urn:liverty-music:webhook:pre-access-token",
				},
				GCP: GCPConfig{
					ProjectID:               "test-project",
					Location:                "us-central1",
					GeminiModel:             "gemini-3-flash-preview",
					GeminiSearchTemperature: 1.0,
				},
				JWT: JWTConfig{
					Issuer:              "https://test-issuer.com",
					JWKSRefreshInterval: 15 * time.Minute,
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
				"SERVER_CONCERT_HANDLER_TIMEOUT":  "180s",
				"SERVER_IDLE_TIMEOUT":             "45s",
				"ADMIN_SERVER_PORT":               "9190",
				"ADMIN_CORS_ALLOWED_ORIGINS":      "https://admin.example.com",
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
					SamplerRatio:   1.0,
				},
				GoroutineLeak: GoroutineLeakConfig{
					Enabled:        false,
					Host:           "127.0.0.1",
					Port:           6060,
					SampleInterval: 2 * time.Minute,
				},
				Server: ServerSettings{
					Port:                    9090,
					Host:                    "0.0.0.0",
					ReadHeaderTimeout:       200 * time.Millisecond,
					ReadTimeout:             2 * time.Second,
					HandlerTimeout:          10 * time.Second,
					ConcertHandlerTimeout:   180 * time.Second,
					IdleTimeout:             45 * time.Second,
					AllowedOrigins:          nil,
					AdminPort:               9190,
					AdminAllowedOrigins:     []string{"https://admin.example.com"},
					OrganizerPort:           8091,
					OrganizerAllowedOrigins: nil,
					RateLimit:               RateLimitConfig{AuthRPS: 100, AuthBurst: 200, AnonRPS: 30, AnonBurst: 60},
				},
				Webhook: WebhookSettings{
					Port:                   9090,
					Host:                   "0.0.0.0",
					ReadHeaderTimeout:      500 * time.Millisecond,
					ReadTimeout:            5 * time.Second,
					IdleTimeout:            30 * time.Second,
					PreAccessTokenAudience: "urn:liverty-music:webhook:pre-access-token",
				},
				GCP: GCPConfig{
					ProjectID:               "custom-project",
					Location:                "us-central1",
					GeminiModel:             "gemini-3-flash-preview",
					GeminiSearchTemperature: 1.0,
				},
				JWT: JWTConfig{
					Issuer:              "https://custom-issuer.com",
					JWKSRefreshInterval: 30 * time.Minute,
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
				Environment: "development",
				Database: DatabaseConfig{
					Port:                   5432,
					InstanceConnectionName: "project:region:instance",
				},
				Logging: LoggingConfig{Level: "info", Format: "json"},
				Server: ServerSettings{
					Port:                    8080,
					AdminPort:               8090,
					OrganizerPort:           8091,
					AllowedOrigins:          []string{"http://localhost:9000"},
					OrganizerAllowedOrigins: []string{"http://localhost:9001"},
				},
				Webhook: validWebhookSettings(),
				NATS:    NATSConfig{URL: "nats://nats.nats.svc.cluster.local:4222"},
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
				Environment: "development",
				Database:    DatabaseConfig{Port: 5432},
				Logging:     LoggingConfig{Level: "info", Format: "json"},
				Server:      ServerSettings{Port: 8080},
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
				Environment: "development",
				Database: DatabaseConfig{
					Port:                   5432,
					InstanceConnectionName: "project:region:instance",
				},
				Logging: LoggingConfig{Level: "info", Format: "json"},
				Server:  ServerSettings{Port: 8080},
			},
			wantErr: true,
		},
		{
			name: "missing NATS URL in development",
			config: &ServerConfig{
				Environment: "development",
				Database: DatabaseConfig{
					Port:                   5432,
					InstanceConnectionName: "project:region:instance",
				},
				Logging: LoggingConfig{Level: "info", Format: "json"},
				Server:  ServerSettings{Port: 8080, AllowedOrigins: []string{"http://localhost:9000"}},
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
				Environment: "local",
				Database:    DatabaseConfig{Port: 5432},
				Logging:     LoggingConfig{Level: "info", Format: "json"},
				Server:      ServerSettings{Port: 8080, AdminPort: 8090, OrganizerPort: 8091},
				Webhook:     validWebhookSettings(),
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
			Environment: "local",
			Database:    DatabaseConfig{Port: 5432},
			Logging:     LoggingConfig{Level: "info", Format: "json"},
		}
		assert.NoError(t, cfg.Validate())
	})

	t.Run("valid development without NATS", func(t *testing.T) {
		cfg := &JobConfig{
			Environment: "development",
			Database: DatabaseConfig{
				Port:                   5432,
				InstanceConnectionName: "project:region:instance",
			},
			Logging: LoggingConfig{Level: "info", Format: "json"},
		}
		assert.NoError(t, cfg.Validate())
	})
}

func TestConsumerConfig_Validate(t *testing.T) {
	t.Run("valid local without NATS", func(t *testing.T) {
		cfg := &ConsumerConfig{
			Environment: "local",
			Database:    DatabaseConfig{Port: 5432},
			Logging:     LoggingConfig{Level: "info", Format: "json"},
		}
		assert.NoError(t, cfg.Validate())
	})

	t.Run("missing NATS URL in development", func(t *testing.T) {
		cfg := &ConsumerConfig{
			Environment: "development",
			Database: DatabaseConfig{
				Port:                   5432,
				InstanceConnectionName: "project:region:instance",
			},
			Logging: LoggingConfig{Level: "info", Format: "json"},
		}
		assert.Error(t, cfg.Validate())
	})
}

func TestGCPConfig_ParserModelResolution(t *testing.T) {
	tests := []struct {
		name           string
		geminiModel    string
		parserOverride string
		wantParser     string
	}{
		{
			name:           "workload-specific var takes precedence",
			geminiModel:    "gemini-3-flash-preview",
			parserOverride: "gemini-3.1-flash-lite",
			wantParser:     "gemini-3.1-flash-lite",
		},
		{
			name:        "legacy fallback to GeminiModel when override empty",
			geminiModel: "gemini-3-flash-preview",
			wantParser:  "gemini-3-flash-preview",
		},
		{
			name:       "all empty leaves it empty (caller must default elsewhere)",
			wantParser: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := GCPConfig{
				GeminiModel:       tt.geminiModel,
				GeminiParserModel: tt.parserOverride,
			}
			assert.Equal(t, tt.wantParser, c.ParserModel())
		})
	}
}

func TestGCPConfig_SearchModelExtractResolution(t *testing.T) {
	t.Run("env override takes precedence", func(t *testing.T) {
		c := GCPConfig{GeminiSearchModelExtract: "gemini-3.5-flash"}
		assert.Equal(t, "gemini-3.5-flash", c.SearchModelExtract())
	})
	t.Run("default applied when unset", func(t *testing.T) {
		c := GCPConfig{}
		assert.Equal(t, defaultSearchModelExtract, c.SearchModelExtract())
		assert.Equal(t, "gemini-3.6-flash", c.SearchModelExtract())
	})
}

func TestGCPConfig_SearchCacheTTLResolution(t *testing.T) {
	t.Run("env override takes precedence", func(t *testing.T) {
		c := GCPConfig{GeminiSearchCacheTTL: 72 * time.Hour}
		assert.Equal(t, 72*time.Hour, c.SearchCacheTTL())
	})
	t.Run("default applied when unset", func(t *testing.T) {
		c := GCPConfig{}
		assert.Equal(t, defaultSearchCacheTTL, c.SearchCacheTTL())
		assert.Equal(t, 24*time.Hour, c.SearchCacheTTL())
	})
}

func TestGCPConfig_SearchDiscoveryWindowResolution(t *testing.T) {
	t.Run("env override takes precedence", func(t *testing.T) {
		c := GCPConfig{GeminiSearchDiscoveryWindow: 168 * time.Hour}
		assert.Equal(t, 168*time.Hour, c.SearchDiscoveryWindow())
	})
	t.Run("default applied when unset", func(t *testing.T) {
		c := GCPConfig{}
		assert.Equal(t, defaultSearchDiscoveryWindow, c.SearchDiscoveryWindow())
		assert.Equal(t, 14*24*time.Hour, c.SearchDiscoveryWindow())
	})
}

func TestGCPConfig_Validate_SearchDurations(t *testing.T) {
	t.Run("accepts zero (falls back to default)", func(t *testing.T) {
		c := GCPConfig{}
		assert.NoError(t, c.Validate())
	})
	t.Run("accepts positive durations", func(t *testing.T) {
		c := GCPConfig{GeminiSearchCacheTTL: 72 * time.Hour, GeminiSearchDiscoveryWindow: 336 * time.Hour}
		assert.NoError(t, c.Validate())
	})
	t.Run("rejects negative TTL", func(t *testing.T) {
		c := GCPConfig{GeminiSearchCacheTTL: -1 * time.Hour}
		err := c.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "GCP_GEMINI_SEARCH_CACHE_TTL")
	})
	t.Run("rejects negative discovery window", func(t *testing.T) {
		c := GCPConfig{GeminiSearchDiscoveryWindow: -1 * time.Hour}
		err := c.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "GCP_GEMINI_SEARCH_DISCOVERY_WINDOW")
	})
}

func TestGCPConfig_Validate_ThinkingLevel(t *testing.T) {
	for _, lvl := range []string{"", "low", "medium", "high"} {
		t.Run("accepts "+lvl, func(t *testing.T) {
			c := GCPConfig{GeminiSearchThinkingLevel: lvl}
			assert.NoError(t, c.Validate())
		})
	}
	t.Run("rejects unknown value", func(t *testing.T) {
		c := GCPConfig{GeminiSearchThinkingLevel: "ultra"}
		err := c.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "GCP_GEMINI_SEARCH_THINKING_LEVEL")
	})
}

// allSet returns a fully-configured PocketSignConfig for use in Validate tests.
func allSetPSConfig() PocketSignConfig {
	return PocketSignConfig{
		BaseURL:     "https://verify.test.p8n.app",
		Token:       "tok",
		TenantID:    "tenant-1",
		CallbackURL: "https://fan.example.app/verify/callback",
	}
}

func TestPocketSignConfig_IsConfigured(t *testing.T) {
	// True only when all four fields are present.
	assert.True(t, allSetPSConfig().IsConfigured())

	// Any single field missing → false.
	assert.False(t, PocketSignConfig{}.IsConfigured())
	assert.False(t, PocketSignConfig{Token: "tok", TenantID: "t", CallbackURL: "https://x.example.com/cb"}.IsConfigured())
	assert.False(t, PocketSignConfig{BaseURL: "https://verify.test.p8n.app", TenantID: "t", CallbackURL: "https://x.example.com/cb"}.IsConfigured())
	assert.False(t, PocketSignConfig{BaseURL: "https://verify.test.p8n.app", Token: "tok", CallbackURL: "https://x.example.com/cb"}.IsConfigured())
	assert.False(t, PocketSignConfig{BaseURL: "https://verify.test.p8n.app", Token: "tok", TenantID: "t"}.IsConfigured())
}

func TestPocketSignConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     PocketSignConfig
		wantErr string // substring; empty means no error
	}{
		{
			name: "empty is valid (stub verifier used)",
			cfg:  PocketSignConfig{},
		},
		{
			name: "all four fields set with a valid p8n.app host is valid",
			cfg:  allSetPSConfig(),
		},
		{
			name: "mock host verify.mock.p8n.app is accepted",
			cfg: PocketSignConfig{
				BaseURL:     "https://verify.mock.p8n.app",
				Token:       "tok",
				TenantID:    "tenant-mock",
				CallbackURL: "https://fan.dev.example.app/verify/callback",
			},
		},
		{
			// The apex host p8n.app (no subdomain) must also be accepted because
			// the Validate check is HasSuffix(".p8n.app") OR Hostname()=="p8n.app".
			name: "apex host p8n.app is accepted",
			cfg: PocketSignConfig{
				BaseURL:     "https://p8n.app",
				Token:       "tok",
				TenantID:    "tenant-apex",
				CallbackURL: "https://fan.example.app/verify/callback",
			},
		},
		{
			name:    "base url without token is rejected",
			cfg:     PocketSignConfig{BaseURL: "https://verify.test.p8n.app"},
			wantErr: "POCKET_SIGN_TOKEN must be set",
		},
		{
			name:    "token without base url is rejected",
			cfg:     PocketSignConfig{Token: "tok"},
			wantErr: "POCKET_SIGN_BASE_URL must be set",
		},
		{
			name: "non-https base url is rejected",
			cfg: PocketSignConfig{
				BaseURL: "http://verify.test.p8n.app", Token: "tok",
				TenantID: "t", CallbackURL: "https://fan.example.app/cb",
			},
			wantErr: "must be https",
		},
		{
			name: "non-pocketsign host is rejected",
			cfg: PocketSignConfig{
				BaseURL: "https://evil.example.com", Token: "tok",
				TenantID: "t", CallbackURL: "https://fan.example.app/cb",
			},
			wantErr: "not a Pocket Sign endpoint",
		},
		{
			name: "non-https callback url is rejected",
			cfg: PocketSignConfig{
				BaseURL: "https://verify.test.p8n.app", Token: "tok",
				TenantID: "t", CallbackURL: "http://fan.example.app/cb",
			},
			wantErr: "POCKET_SIGN_CALLBACK_URL must be https",
		},
		{
			name:    "missing tenant id is rejected",
			cfg:     PocketSignConfig{BaseURL: "https://verify.test.p8n.app", Token: "tok", CallbackURL: "https://fan.example.app/cb"},
			wantErr: "POCKET_SIGN_TENANT_ID must be set",
		},
		{
			name:    "missing callback url is rejected",
			cfg:     PocketSignConfig{BaseURL: "https://verify.test.p8n.app", Token: "tok", TenantID: "t"},
			wantErr: "POCKET_SIGN_CALLBACK_URL must be set",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestServerConfig_Validate_PocketSignPropagation proves that a partially-set
// PocketSignConfig causes ServerConfig.Validate to fail. This pins the
// propagation path (ServerConfig.Validate → PocketSignConfig.Validate).
func TestServerConfig_Validate_PocketSignPropagation(t *testing.T) {
	t.Parallel()

	// Partial PocketSign config: BaseURL set but Token missing.
	// The ServerConfig is otherwise valid for a local environment.
	cfg := &ServerConfig{
		Environment: "local",
		Database:    DatabaseConfig{Port: 5432},
		Logging:     LoggingConfig{Level: "info", Format: "json"},
		Server: ServerSettings{
			Port:          8080,
			AdminPort:     8090,
			OrganizerPort: 8091,
		},
		Webhook: validWebhookSettings(),
		JWT: JWTConfig{
			Issuer:              "https://test-issuer.com",
			JWKSRefreshInterval: 15 * time.Minute,
		},
		PocketSign: PocketSignConfig{
			BaseURL: "https://verify.mock.p8n.app",
			// Token, TenantID, CallbackURL deliberately omitted — partial config.
		},
	}

	err := cfg.Validate()
	require.Error(t, err,
		"a partial PocketSign config must cause ServerConfig.Validate to fail")
	assert.Contains(t, err.Error(), "POCKET_SIGN_TOKEN must be set",
		"error message must identify the missing field")
}
