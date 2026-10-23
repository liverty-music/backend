package di_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/di"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setenvAll sets every variable in env for the test.
func setenvAll(t *testing.T, env map[string]string) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
}

// Outside local the event consumer and the admin console API reach the
// identity provider only through the identity service, so without its URL
// they refuse to start instead of starting without verification emails,
// passkey recovery links, fan deletion or Organizer provisioning.
func TestCallersRequireIdentityServiceURLOutsideLocal(t *testing.T) {
	base := map[string]string{
		"ENVIRONMENT":                       "production",
		"DATABASE_INSTANCE_CONNECTION_NAME": "project:region:instance",
		"DATABASE_NAME":                     "liverty-music",
		"DATABASE_USER":                     "backend-app@liverty-music-prod.iam",
		"TELEMETRY_OTLP_ENDPOINT":           "otel-collector:4318",
		"NATS_URL":                          "nats://nats:4222",
		"OIDC_ISSUER_URL":                   "https://auth.liverty-music.app",
		"GCP_GEMINI_SEARCH_API_KEY":         "gemini-key",
		"VAPID_PUBLIC_KEY":                  "vapid-pub",
		"VAPID_PRIVATE_KEY":                 "vapid-priv",
	}

	t.Run("the event consumer", func(t *testing.T) {
		setenvAll(t, base)
		setenvAll(t, map[string]string{
			"FANARTTV_API_KEY":        "fanart-key",
			"GCP_PROJECT_ID":          "liverty-music-prod",
			"POSTHOG_PROJECT_API_KEY": "phc_x",
		})

		_, err := di.InitializeConsumerApp(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "required outside local but not set: IDENTITY_SERVICE_URL")
	})

	t.Run("the admin console API", func(t *testing.T) {
		setenvAll(t, base)
		setenvAll(t, map[string]string{
			"API_WORKLOAD":                    "admin",
			"FAN_WEB_BASE_URL":                "https://liverty-music.app",
			"CORS_ALLOWED_ORIGINS":            "https://liverty-music.app",
			"ADMIN_CORS_ALLOWED_ORIGINS":      "https://admin.liverty-music.app",
			"ORGANIZER_CORS_ALLOWED_ORIGINS":  "https://organizer.liverty-music.app",
			"RECEPTION_CORS_ALLOWED_ORIGINS":  "https://reception.liverty-music.app",
			"CDN_BASE_URL":                    "https://cdn.liverty-music.app",
			"LASTFM_API_KEY":                  "lastfm-key",
			"POCKET_SIGN_BASE_URL":            "https://verify.mock.p8n.app",
			"POCKET_SIGN_CALLBACK_URL":        "https://api.liverty-music.app/pocket-sign/callback",
			"POCKET_SIGN_TENANT_ID":           "tenant",
			"POCKET_SIGN_TOKEN":               "ps-token",
			"STORAGE_BUCKET":                  "storage",
			"STORAGE_INTERNAL_BUCKET":         "storage-internal",
			"STRIPE_ONBOARDING_RETURN_URL":    "https://organizer.liverty-music.app/",
			"STRIPE_SECRET_KEY":               "sk_test_x",
			"STRIPE_WEBHOOK_SIGNING_SECRET":   "whsec_x",
			"WEBHOOK_LOGIN_EVENT_SIGNING_KEY": "login-key",
			"ORGANIZER_CONSOLE_PROJECT_ID":    "organizer-console-project",
		})

		_, err := di.InitializeApp(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "required outside local but not set: IDENTITY_SERVICE_URL")
	})
}
