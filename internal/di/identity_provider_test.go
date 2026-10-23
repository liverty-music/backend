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

// Every workload but admin runs without the identity service: DI must build no
// identity-provider client, say so in the
// startup log, and still register the fan handlers (and the unrouted
// in-process admin one) on a UserUseCase without it.
func TestProvideIdentityRemover_NeitherSet(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	logger, err := di.ProvideLogger(config.LoggingConfig{Format: "json"}, logging.WithWriter(buf))
	require.NoError(t, err)

	identityRemover := di.ProvideIdentityRemover(context.Background(), "", logger)

	assert.Nil(t, identityRemover)
	entries := parseJSONLines(t, buf)
	require.Len(t, entries, 1)
	assert.Equal(t, "identity-provider clients built", entries[0]["msg"])
	assert.Equal(t, "none", entries[0]["clients"])

	userUC := usecase.NewUserUseCase(nil, nil, nil, nil, nil, identityRemover, usecase.PasskeyMailers{}, logger)
	server := connect.NewServer()
	assert.NotPanics(t, func() {
		userconnect.RegisterUserServiceHandler(server, rpc.NewUserHandler(userUC, logger))
		adminuserconnect.RegisterUserServiceHandler(server, rpc.NewAdminUserHandler(userUC, logger))
	})
}

func TestProvideIdentityRemover_IdentityService(t *testing.T) {
	t.Parallel()
	logger, err := logging.New()
	require.NoError(t, err)

	got := di.ProvideIdentityRemover(context.Background(), "http://identity:9101", logger)

	assert.IsType(t, &identity.UserClient{}, got)
}

func TestNewConsumerEmailVerifier(t *testing.T) {
	t.Parallel()

	assert.IsType(t, &identity.UserClient{}, di.NewConsumerEmailVerifier(&config.ConsumerConfig{IdentityServiceURL: "http://identity:9100"}),
		"the identity service URL selects the identity service client")
	assert.Nil(t, di.NewConsumerEmailVerifier(&config.ConsumerConfig{Environment: "local"}),
		"without it in local there is no verifier")
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
