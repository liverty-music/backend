package server_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"golang.org/x/oauth2"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIdentityProviderUnreachable(t *testing.T) {
	t.Parallel()

	wrap := func(err error) error {
		return apperr.Wrap(fmt.Errorf("step: %w", err), codes.Internal, "call identity provider")
	}
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "the API answers Unavailable", err: wrap(status.Error(grpccodes.Unavailable, "connection refused")), want: true},
		{name: "the token request cannot be sent", err: wrap(&url.Error{Op: "Post", URL: "https://auth.example.com/oauth/v2/token", Err: errors.New("no such host")}), want: true},
		{name: "the token endpoint answers 503", err: wrap(&oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusServiceUnavailable}}), want: true},
		{name: "the token endpoint rejects the key", err: wrap(&oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusUnauthorized}})},
		{name: "the API answers NotFound", err: wrap(status.Error(grpccodes.NotFound, "user not found"))},
		{name: "the API answers FailedPrecondition", err: apperr.Wrap(status.Error(grpccodes.FailedPrecondition, "verified"), codes.FailedPrecondition, "already verified")},
		{name: "no cause", err: apperr.New(codes.Internal, "no code returned")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, server.IdentityProviderUnreachable(tt.err))
		})
	}
}
