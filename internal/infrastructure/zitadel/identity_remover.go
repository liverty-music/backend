package zitadel

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/zitadel-go/v3/pkg/client/middleware"
	zitadelconn "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel"
	userpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user/v2"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// identityDeleteClient is the subset of the v2 UserServiceClient used by
// DeleteIdentity.
type identityDeleteClient interface {
	DeleteUser(ctx context.Context, in *userpb.DeleteUserRequest, opts ...grpc.CallOption) (*userpb.DeleteUserResponse, error)
}

// Compile-time interface compliance check.
var _ entity.IdentityRemover = (*IdentityRemover)(nil)

// IdentityRemover removes fan users from Zitadel through the v2 User Service.
// It authenticates as the backend-app machine user, whose ORG_USER_MANAGER
// membership on the product org carries user.delete — the least-privileged
// credential that can remove a fan's identity.
type IdentityRemover struct {
	client identityDeleteClient
	logger *logging.Logger
}

// NewIdentityRemover creates an IdentityRemover that authenticates to the
// Zitadel API using a machine user's private key JWT, like NewEmailVerifier.
//
// issuerURL is the OIDC issuer URL (e.g., "https://auth.dev.liverty-music.app").
// keyPath is the file path to the backend-app machine key JSON.
func NewIdentityRemover(ctx context.Context, issuerURL, keyPath string, logger *logging.Logger) (*IdentityRemover, error) {
	apiEndpoint, err := grpcEndpoint(issuerURL)
	if err != nil {
		return nil, fmt.Errorf("parse zitadel domain: %w", err)
	}

	conn, err := zitadelconn.NewConnection(
		ctx,
		issuerURL,
		apiEndpoint,
		[]string{oidc.ScopeOpenID, zitadelconn.ScopeZitadelAPI()},
		zitadelconn.WithJWTProfileTokenSource(middleware.JWTProfileFromPath(ctx, keyPath)),
		zitadelconn.WithDialOptions(grpc.WithStatsHandler(otelgrpc.NewClientHandler())),
	)
	if err != nil {
		return nil, fmt.Errorf("create zitadel connection: %w", err)
	}

	return &IdentityRemover{
		client: userpb.NewUserServiceClient(conn.ClientConn),
		logger: logger,
	}, nil
}

// DeleteIdentity removes the Zitadel user identified by externalID. A NotFound
// response means the user is already gone and counts as success, so a retry
// after a partial deletion completes.
func (r *IdentityRemover) DeleteIdentity(ctx context.Context, externalID string) error {
	if _, err := r.client.DeleteUser(ctx, &userpb.DeleteUserRequest{UserId: externalID}); err != nil {
		if isNotFound(err) {
			r.logger.Info(ctx, "identity already removed", slog.String("external_id", externalID))
			return nil
		}
		return apperr.Wrap(err, codes.Internal, "delete zitadel user")
	}
	r.logger.Info(ctx, "identity removed", slog.String("external_id", externalID))
	return nil
}
