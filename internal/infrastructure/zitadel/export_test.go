package zitadel

import (
	"context"

	userpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user/v2"
	"google.golang.org/grpc"

	"github.com/pannpers/go-logging/logging"
)

// GrpcEndpoint exposes grpcEndpoint for testing.
var GrpcEndpoint = grpcEndpoint

// IdentityDeleteClient exposes identityDeleteClient for testing.
type IdentityDeleteClient interface {
	DeleteUser(ctx context.Context, in *userpb.DeleteUserRequest, opts ...grpc.CallOption) (*userpb.DeleteUserResponse, error)
}

// NewIdentityRemoverWithClient builds an IdentityRemover around the given
// client instead of a live Zitadel connection.
func NewIdentityRemoverWithClient(client IdentityDeleteClient, logger *logging.Logger) *IdentityRemover {
	return &IdentityRemover{client: client, logger: logger}
}

// TokenEndpoint exposes tokenEndpoint for testing.
var TokenEndpoint = tokenEndpoint
