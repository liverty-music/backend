package di_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	adminuserconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/admin/user/v1/userv1connect"
	userconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/user/v1/userv1connect"
	"connectrpc.com/connect/v2"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/di"
	"github.com/liverty-music/backend/internal/infrastructure/identity"
	"github.com/liverty-music/backend/internal/infrastructure/mail"
	infrazitadel "github.com/liverty-music/backend/internal/infrastructure/zitadel"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeMachineKey writes a Zitadel machine key JSON with a fresh RSA key and
// returns its path.
func writeMachineKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	data, err := json.Marshal(map[string]string{
		"type":   "serviceaccount",
		"keyId":  "key-1",
		"key":    string(keyPEM),
		"userId": "machine-user",
	})
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "key.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

// The organizer and reception workloads run without the identity service: DI
// must build no identity-provider client, say so in the startup log, and
// still register the fan handlers (and the unrouted in-process admin one) on a
// UserUseCase without it.
func TestProvideIdentityClients_None(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	logger, err := di.ProvideLogger(config.LoggingConfig{Format: "json"}, logging.WithWriter(buf))
	require.NoError(t, err)

	identityRemover, emailCodes := di.ProvideIdentityClients(context.Background(),
		&config.ServerConfig{Environment: "production", Workload: config.WorkloadOrganizer}, logger)

	assert.Nil(t, identityRemover)
	assert.Nil(t, emailCodes)
	entries := parseJSONLines(t, buf)
	require.Len(t, entries, 1)
	assert.Equal(t, "identity-provider clients built", entries[0]["msg"])
	assert.Equal(t, "none", entries[0]["clients"])

	userUC := usecase.NewUserUseCase(nil, usecase.DeletionBlockerReads{}, nil, nil, emailCodes, identityRemover, usecase.PasskeyMailers{}, logger)
	server := connect.NewServer()
	assert.NotPanics(t, func() {
		userconnect.RegisterUserServiceHandler(server, rpc.NewUserHandler(userUC, nil, logger))
		adminuserconnect.RegisterUserServiceHandler(server, rpc.NewAdminUserHandler(userUC, logger))
	})
}

func TestProvideIdentityClients(t *testing.T) {
	t.Parallel()
	logger, err := logging.New()
	require.NoError(t, err)

	type want struct {
		remover    bool
		emailCodes bool
	}
	tests := []struct {
		name string
		cfg  *config.ServerConfig
		want want
	}{
		{
			name: "admin removes identities and verifies no email",
			cfg:  &config.ServerConfig{Environment: "production", Workload: config.WorkloadAdmin, IdentityServiceURL: "http://identity:9101"},
			want: want{remover: true},
		},
		{
			// A fan removes their own identity when deleting their account.
			name: "fan verifies emails and removes identities",
			cfg:  &config.ServerConfig{Environment: "production", Workload: config.WorkloadFan, IdentityServiceURL: "http://identity:9102"},
			want: want{remover: true, emailCodes: true},
		},
		{
			name: "local with the identity service does both",
			cfg:  &config.ServerConfig{Environment: "local", IdentityServiceURL: "http://localhost:9101"},
			want: want{remover: true, emailCodes: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			remover, emailCodes := di.ProvideIdentityClients(context.Background(), tt.cfg, logger)
			assert.Equal(t, tt.want, want{remover: remover != nil, emailCodes: emailCodes != nil})
		})
	}
}

func TestNewEmailVerifier(t *testing.T) {
	t.Parallel()
	logger, err := logging.New()
	require.NoError(t, err)

	got, err := di.NewEmailVerifier(&config.AccountEmailConfig{
		IdentityServiceURL: "http://identity:9100",
		ZitadelDomain:      "https://auth.liverty-music.app",
		Postmark:           config.PostmarkConfig{ServerToken: "token"},
	}, logger)
	require.NoError(t, err)
	assert.IsType(t, &mail.PostmarkEmailVerificationSender{}, got, "the identity service URL and Postmark select the Postmark sender")

	got, err = di.NewEmailVerifier(&config.AccountEmailConfig{IdentityServiceURL: "http://identity:9100"}, logger)
	require.NoError(t, err)
	assert.IsType(t, &mail.NoopEmailVerificationSender{}, got, "without Postmark every send fails")

	got, err = di.NewEmailVerifier(&config.AccountEmailConfig{}, logger)
	require.NoError(t, err)
	assert.Nil(t, got, "without the identity service there is no verifier")
}

func TestProvideOrganizationRepository(t *testing.T) {
	t.Parallel()
	logger, err := logging.New()
	require.NoError(t, err)

	tests := []struct {
		name string
		cfg  *config.ServerConfig
		want any
	}{
		{
			name: "admin uses the identity service",
			cfg: &config.ServerConfig{
				Environment:        "production",
				Workload:           config.WorkloadAdmin,
				IdentityServiceURL: "http://identity:9101",
			},
			want: &identity.OrganizationClient{},
		},
		{
			name: "local without the identity service uses the no-op",
			cfg:  &config.ServerConfig{Environment: "local"},
			want: &infrazitadel.NoopOrganizationRepository{},
		},
		{
			name: "another workload outside local refuses every call",
			cfg:  &config.ServerConfig{Environment: "production", Workload: config.WorkloadFan},
			want: &infrazitadel.UnavailableOrganizationRepository{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.IsType(t, tt.want, di.ProvideOrganizationRepository(tt.cfg, logger))
		})
	}
}
