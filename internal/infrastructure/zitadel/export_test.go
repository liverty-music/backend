package zitadel

import (
	"github.com/pannpers/go-logging/logging"
)

// NewTestEmailVerifier creates an EmailVerifier with a mock gRPC client
// for unit testing. Bypasses the real gRPC connection setup.
func NewTestEmailVerifier(client emailCodeClient, logger *logging.Logger) *EmailVerifier {
	return &EmailVerifier{
		client: client,
		logger: logger,
	}
}

// GrpcEndpoint exposes grpcEndpoint for testing.
var GrpcEndpoint = grpcEndpoint
