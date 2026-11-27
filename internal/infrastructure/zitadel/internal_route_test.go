package zitadel_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"

	userpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	infrazitadel "github.com/liverty-music/backend/internal/infrastructure/zitadel"
	"github.com/liverty-music/backend/internal/infrastructure/zitadel/route"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// internalZitadel is a sign-in service at an internal address: one plain-text
// listener serving the token endpoint over HTTP/1.1 and the gRPC API over
// HTTP/2, as `zitadel-api` does inside the cluster. It records what each
// call carried.
type internalZitadel struct {
	userpb.UnimplementedUserServiceServer

	grpc *grpc.Server

	mu           sync.Mutex
	tokenHeaders http.Header
	assertionAud any
	grpcMetadata metadata.MD
}

func (z *internalZitadel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
		z.grpc.ServeHTTP(w, r)
		return
	}
	if r.URL.Path != "/oauth/v2/token" {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	z.mu.Lock()
	z.tokenHeaders = r.Header.Clone()
	z.assertionAud = jwtClaim(r.PostForm.Get("assertion"), "aud")
	z.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"access_token":"token-1","token_type":"Bearer","expires_in":3600}`))
}

func (z *internalZitadel) DeleteUser(ctx context.Context, _ *userpb.DeleteUserRequest) (*userpb.DeleteUserResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	z.mu.Lock()
	z.grpcMetadata = md
	z.mu.Unlock()
	return &userpb.DeleteUserResponse{}, nil
}

// jwtClaim returns a claim of a JWT without verifying it.
func jwtClaim(token, name string) any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return nil
	}
	return claims[name]
}

// startInternalZitadel serves z in plain text, HTTP/1.1 and HTTP/2 with prior
// knowledge, and returns its base URL.
func startInternalZitadel(t *testing.T, z *internalZitadel) string {
	t.Helper()
	z.grpc = grpc.NewServer()
	userpb.RegisterUserServiceServer(z.grpc, z)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	srv := &http.Server{Handler: z, Protocols: &protocols}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { _ = srv.Close() })
	return "http://" + lis.Addr().String()
}

// The public issuer never answers (.test is reserved), so the call can only
// succeed through the internal address.
func TestIdentityRemover_CallsTheInternalAddress(t *testing.T) {
	t.Parallel()

	const issuer = "https://auth.liverty-music.test"
	z := &internalZitadel{}
	r, err := route.New(issuer, startInternalZitadel(t, z))
	require.NoError(t, err)
	logger, err := logging.New()
	require.NoError(t, err)

	remover, err := infrazitadel.NewIdentityRemover(context.Background(), r, writeMachineKey(t), logger)
	require.NoError(t, err)
	require.NoError(t, remover.DeleteIdentity(context.Background(), "zitadel-user-1"))

	z.mu.Lock()
	defer z.mu.Unlock()
	assert.Equal(t, "auth.liverty-music.test", z.tokenHeaders.Get(route.InstanceHostHeader), "token request")
	assert.Equal(t, "auth.liverty-music.test", z.tokenHeaders.Get(route.PublicHostHeader), "token request")
	assert.Contains(t, []any{issuer, []any{issuer}}, z.assertionAud, "the assertion's audience stays the public issuer")
	assert.Equal(t, []string{"auth.liverty-music.test"}, z.grpcMetadata.Get(route.InstanceHostHeader), "gRPC call")
	assert.Equal(t, []string{"auth.liverty-music.test"}, z.grpcMetadata.Get(route.PublicHostHeader), "gRPC call")
	assert.Equal(t, []string{"Bearer token-1"}, z.grpcMetadata.Get("authorization"))
}
