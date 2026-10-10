package zitadel_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	zitadelconn "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel"
	userpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user/v2"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	grpcstatus "google.golang.org/grpc/status"

	infrazitadel "github.com/liverty-music/backend/internal/infrastructure/zitadel"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		issuer string
		want   string
	}{
		{issuer: "https://auth.liverty-music.app", want: "https://auth.liverty-music.app/oauth/v2/token"},
		{issuer: "https://auth.liverty-music.app/", want: "https://auth.liverty-music.app/oauth/v2/token"},
	}
	for _, tt := range tests {
		t.Run(tt.issuer, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, infrazitadel.TokenEndpoint(tt.issuer))
		})
	}
}

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
		"userId": "backend-app",
	})
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "key.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

// fakeIssuer is a sign-in service whose token endpoint answers with the queued
// status codes in order, then 200.
type fakeIssuer struct {
	mu       sync.Mutex
	statuses []int
	paths    []string
}

func (f *fakeIssuer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.URL.Path)
	status := http.StatusOK
	if len(f.statuses) > 0 {
		status, f.statuses = f.statuses[0], f.statuses[1:]
	}
	f.mu.Unlock()

	if status != http.StatusOK {
		http.Error(w, "unavailable", status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"access_token":"token-1","token_type":"Bearer","expires_in":3600}`))
}

func (f *fakeIssuer) requestPaths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

// stubDeleteUserServer accepts DeleteUser calls that carry a bearer token.
type stubDeleteUserServer struct {
	userpb.UnimplementedUserServiceServer
}

func (stubDeleteUserServer) DeleteUser(ctx context.Context, _ *userpb.DeleteUserRequest) (*userpb.DeleteUserResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if got := md.Get("authorization"); len(got) != 1 || got[0] != "Bearer token-1" {
		return nil, grpcstatus.Errorf(grpccodes.Unauthenticated, "authorization = %v", got)
	}
	return &userpb.DeleteUserResponse{}, nil
}

func startDeleteUserServer(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := grpc.NewServer()
	userpb.RegisterUserServiceServer(s, stubDeleteUserServer{})
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.GracefulStop)
	return lis.Addr().String()
}

// @spec components/infrastructure/backend/process/startup-dependencies "Start while the sign-in service is down"
func TestNewIdentityRemover_SignInServiceDown(t *testing.T) {
	t.Parallel()

	// Nothing listens on the issuer: building the client must not need it.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	issuer := "http://" + lis.Addr().String()
	require.NoError(t, lis.Close())
	logger, err := logging.New()
	require.NoError(t, err)

	r, err := infrazitadel.NewIdentityRemover(context.Background(), issuer, writeMachineKey(t), logger,
		zitadelconn.WithInsecure(),
		zitadelconn.WithCustomURL(issuer, startDeleteUserServer(t)),
	)

	require.NoError(t, err)
	assert.NotNil(t, r)
}

// @spec components/infrastructure/backend/process/startup-dependencies "Recovery without restart"
func TestIdentityRemover_RecoversWhenSignInServiceReturns(t *testing.T) {
	t.Parallel()

	issuerHandler := &fakeIssuer{statuses: []int{http.StatusServiceUnavailable}}
	issuer := httptest.NewServer(issuerHandler)
	t.Cleanup(issuer.Close)
	logger, err := logging.New()
	require.NoError(t, err)

	r, err := infrazitadel.NewIdentityRemover(context.Background(), issuer.URL, writeMachineKey(t), logger,
		zitadelconn.WithInsecure(),
		zitadelconn.WithCustomURL(issuer.URL, startDeleteUserServer(t)),
	)
	require.NoError(t, err)
	assert.Empty(t, issuerHandler.requestPaths(), "building the client must make no request")

	err = r.DeleteIdentity(context.Background(), "zitadel-user-1")
	assert.ErrorIs(t, err, apperr.ErrInternal, "the call fails as delete-identity specifies")

	err = r.DeleteIdentity(context.Background(), "zitadel-user-1")
	assert.NoError(t, err, "the next call after recovery succeeds")

	assert.Equal(t, []string{"/oauth/v2/token", "/oauth/v2/token"}, issuerHandler.requestPaths(),
		"the token comes from the static endpoint, without discovery")
}
