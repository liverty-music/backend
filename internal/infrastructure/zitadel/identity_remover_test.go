package zitadel_test

import (
	"context"
	"testing"
	"time"

	userpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user/v2"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	infrazitadel "github.com/liverty-music/backend/internal/infrastructure/zitadel"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubIdentityDeleteClient records the user ids passed to DeleteUser and
// returns err.
type stubIdentityDeleteClient struct {
	deletedIDs []string
	err        error
}

func (s *stubIdentityDeleteClient) DeleteUser(_ context.Context, in *userpb.DeleteUserRequest, _ ...grpc.CallOption) (*userpb.DeleteUserResponse, error) {
	s.deletedIDs = append(s.deletedIDs, in.GetUserId())
	if s.err != nil {
		return nil, s.err
	}
	return &userpb.DeleteUserResponse{}, nil
}

func TestIdentityRemover_DeleteIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		stubErr error
		wantErr error
	}{
		{
			// @spec components/entity/user/delete-identity "Existing identity"
			name: "removes the zitadel user",
		},
		{
			// @spec components/entity/user/delete-identity "Already removed"
			name:    "succeeds when the zitadel user no longer exists",
			stubErr: grpcstatus.Error(grpccodes.NotFound, "user not found"),
		},
		{
			name:    "returns Internal when the zitadel user cannot be removed",
			stubErr: grpcstatus.Error(grpccodes.PermissionDenied, "no permission"),
			wantErr: apperr.ErrInternal,
		},
		{
			name:    "returns Unavailable when zitadel answers that it is unavailable",
			stubErr: grpcstatus.Error(grpccodes.Unavailable, "connection refused"),
			wantErr: apperr.ErrUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			logger, err := logging.New()
			require.NoError(t, err)
			stub := &stubIdentityDeleteClient{err: tt.stubErr}
			r := infrazitadel.NewIdentityRemoverWithClient(stub, logger)

			err = r.DeleteIdentity(context.Background(), "zitadel-user-1")

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, []string{"zitadel-user-1"}, stub.deletedIDs)
		})
	}
}

// The issuer host under ".invalid" never resolves (RFC 6761), so neither the
// token nor the call can be sent.
//
// @spec components/entity/user/delete-identity "Provider unreachable"
func TestIdentityRemover_ProviderUnreachable(t *testing.T) {
	t.Parallel()
	logger, err := logging.New()
	require.NoError(t, err)
	r, err := infrazitadel.NewIdentityRemover(context.Background(), mustRoute(t, "https://auth.liverty-music.invalid"), writeMachineKey(t), logger)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	err = r.DeleteIdentity(ctx, "zitadel-user-1")

	assert.ErrorIs(t, err, apperr.ErrUnavailable)
}
