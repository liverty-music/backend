package di_test

import (
	"bytes"
	"context"
	"testing"

	adminuserconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/admin/user/v1/userv1connect"
	userconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/user/v1/userv1connect"
	"connectrpc.com/connect/v2"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/di"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every workload but admin runs without the backend-app key: DI must build no
// identity-provider client, say so in the startup log, and still register the
// fan handlers (and the unrouted in-process admin one) on a UserUseCase without
// them.
func TestProvideIdentityProviderClients_KeyPathUnset(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	logger, err := di.ProvideLogger(config.LoggingConfig{Format: "json"}, logging.WithWriter(buf))
	require.NoError(t, err)

	emailVerifier, identityRemover, err := di.ProvideIdentityProviderClients(context.Background(), "https://auth.example.com", "", logger)

	require.NoError(t, err)
	assert.Nil(t, emailVerifier)
	assert.Nil(t, identityRemover)
	entries := parseJSONLines(t, buf)
	require.Len(t, entries, 1)
	assert.Equal(t, "identity-provider clients built", entries[0]["msg"])
	assert.Equal(t, "none", entries[0]["clients"])

	userUC := usecase.NewUserUseCase(nil, nil, nil, nil, emailVerifier, identityRemover, usecase.PasskeyMailers{}, logger)
	server := connect.NewServer()
	assert.NotPanics(t, func() {
		userconnect.RegisterUserServiceHandler(server, rpc.NewUserHandler(userUC, logger))
		adminuserconnect.RegisterUserServiceHandler(server, rpc.NewAdminUserHandler(userUC, logger))
	})
}
