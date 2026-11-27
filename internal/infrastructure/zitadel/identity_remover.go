package zitadel

import (
	"context"
	"log/slog"

	zitadelconn "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel"
	userpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user/v2"
	"google.golang.org/grpc"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/zitadel/route"
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
// r is the route to the sign-in service. keyPath is the file path to the
// backend-app machine key JSON. opts are additional zitadel connection
// options (e.g., to point a test at its own server).
//
// Construction makes no network request; the token is fetched on the first
// DeleteIdentity call.
func NewIdentityRemover(ctx context.Context, r route.Route, keyPath string, logger *logging.Logger, opts ...zitadelconn.Option) (*IdentityRemover, error) {
	conn, err := dial(ctx, r, keyPath, opts...)
	if err != nil {
		return nil, err
	}
	return &IdentityRemover{
		client: userpb.NewUserServiceClient(conn.ClientConn),
		logger: logger,
	}, nil
}

// DeleteIdentity removes the Zitadel user identified by providerUserID. A NotFound
// response means the user is already gone and counts as success, so a retry
// after a partial deletion completes.
//
// # Possible errors
//
//   - Unavailable: Zitadel cannot be reached; the identity is either removed or
//     still there.
//   - Internal: any other failure.
func (r *IdentityRemover) DeleteIdentity(ctx context.Context, providerUserID string) error {
	if _, err := r.client.DeleteUser(ctx, &userpb.DeleteUserRequest{UserId: providerUserID}); err != nil {
		if isNotFound(err) {
			r.logger.Info(ctx, "identity already removed", slog.String("provider_user_id", providerUserID))
			return nil
		}
		return wrapGRPC(err, "delete zitadel user", slog.String("provider_user_id", providerUserID))
	}
	r.logger.Info(ctx, "identity removed", slog.String("provider_user_id", providerUserID))
	return nil
}
