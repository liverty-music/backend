package zitadel_test

import (
	"bytes"
	"context"
	"net"
	"testing"

	zitadelconn "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel"
	userpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user/v2"
	"golang.org/x/oauth2"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	infrazitadel "github.com/liverty-music/backend/internal/infrastructure/zitadel"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubUserServiceServer implements only the two email code methods under test.
// All other UserServiceServer methods return Unimplemented via the embedded type.
type stubUserServiceServer struct {
	userpb.UnimplementedUserServiceServer
	sendErr   error
	resendErr error

	lastSendUserID   string
	lastResendUserID string
}

func (s *stubUserServiceServer) SendEmailCode(_ context.Context, in *userpb.SendEmailCodeRequest) (*userpb.SendEmailCodeResponse, error) {
	s.lastSendUserID = in.UserId
	if s.sendErr != nil {
		return nil, s.sendErr
	}
	return &userpb.SendEmailCodeResponse{}, nil
}

func (s *stubUserServiceServer) ResendEmailCode(_ context.Context, in *userpb.ResendEmailCodeRequest) (*userpb.ResendEmailCodeResponse, error) {
	s.lastResendUserID = in.UserId
	if s.resendErr != nil {
		return nil, s.resendErr
	}
	return &userpb.ResendEmailCodeResponse{}, nil
}

// startUserServiceServer starts a real gRPC server on a random local port and
// returns the "host:port" address, mirroring the pattern used in the Zitadel SDK
// own tests (pkg/client/zitadel/client_test.go).
func startUserServiceServer(t *testing.T, srv *stubUserServiceServer) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := grpc.NewServer()
	userpb.RegisterUserServiceServer(s, srv)
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.GracefulStop)
	return lis.Addr().String()
}

// newTestVerifier creates an EmailVerifier pointing at the given gRPC server
// address using insecure transport and a static token, bypassing JWT auth.
// The returned bytes.Buffer captures all log output for assertions.
func newTestVerifier(t *testing.T, addr string) (*infrazitadel.EmailVerifier, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	logger, err := logging.New(logging.WithWriter(buf))
	require.NoError(t, err)

	v, err := infrazitadel.NewEmailVerifier(
		context.Background(),
		"http://test-issuer",
		"",
		logger,
		zitadelconn.WithInsecure(),
		zitadelconn.WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test"})),
		zitadelconn.WithCustomURL("http://test-issuer", addr),
	)
	require.NoError(t, err)
	return v, buf
}

func TestEmailVerifier_SendVerification(t *testing.T) {
	t.Parallel()

	type args struct {
		externalID string
	}
	tests := []struct {
		name      string
		args      args
		sendErr   error
		wantErr   error
		wantInLog string
		wantNoLog string
		check     func(t *testing.T, stub *stubUserServiceServer)
	}{
		{
			name:      "success emits INFO log",
			args:      args{externalID: "user-123"},
			sendErr:   nil,
			wantErr:   nil,
			wantInLog: "email verification sent",
			check: func(t *testing.T, stub *stubUserServiceServer) {
				t.Helper()
				assert.Equal(t, "user-123", stub.lastSendUserID)
			},
		},
		{
			name:      "gRPC unavailable wraps as internal without logging (caller logs)",
			args:      args{externalID: "user-456"},
			sendErr:   grpcstatus.Error(grpccodes.Unavailable, "connection refused"),
			wantErr:   apperr.ErrInternal,
			wantNoLog: "failed to send email code",
		},
		{
			name:      "gRPC not found wraps as internal without logging (caller logs)",
			args:      args{externalID: "nonexistent"},
			sendErr:   grpcstatus.Error(grpccodes.NotFound, "user not found"),
			wantErr:   apperr.ErrInternal,
			wantNoLog: "failed to send email code",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stub := &stubUserServiceServer{sendErr: tt.sendErr}
			addr := startUserServiceServer(t, stub)
			v, logBuf := newTestVerifier(t, addr)

			err := v.SendVerification(context.Background(), tt.args.externalID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
			if tt.wantInLog != "" {
				assert.Contains(t, logBuf.String(), tt.wantInLog)
				assert.Contains(t, logBuf.String(), tt.args.externalID)
			}
			if tt.wantNoLog != "" {
				assert.NotContains(t, logBuf.String(), tt.wantNoLog)
			}
			if tt.check != nil {
				tt.check(t, stub)
			}
		})
	}
}

func TestEmailVerifier_ResendVerification(t *testing.T) {
	t.Parallel()

	type args struct {
		externalID string
	}
	tests := []struct {
		name      string
		args      args
		resendErr error
		wantErr   error
		wantInLog string
		check     func(t *testing.T, stub *stubUserServiceServer)
	}{
		{
			name:      "success emits INFO log",
			args:      args{externalID: "user-123"},
			resendErr: nil,
			wantErr:   nil,
			wantInLog: "email verification resent",
			check: func(t *testing.T, stub *stubUserServiceServer) {
				t.Helper()
				assert.Equal(t, "user-123", stub.lastResendUserID)
			},
		},
		{
			name:      "already verified emits ERROR log and maps to FailedPrecondition",
			args:      args{externalID: "verified-user"},
			resendErr: grpcstatus.Error(grpccodes.FailedPrecondition, "email already verified"),
			wantErr:   apperr.ErrFailedPrecondition,
			wantInLog: "failed to resend email code",
		},
		{
			name:      "gRPC unavailable emits ERROR log and wraps as internal",
			args:      args{externalID: "user-456"},
			resendErr: grpcstatus.Error(grpccodes.Unavailable, "connection refused"),
			wantErr:   apperr.ErrInternal,
			wantInLog: "failed to resend email code",
		},
		{
			name:      "gRPC internal emits ERROR log and wraps as internal",
			args:      args{externalID: "user-789"},
			resendErr: grpcstatus.Error(grpccodes.Internal, "something went wrong"),
			wantErr:   apperr.ErrInternal,
			wantInLog: "failed to resend email code",
		},
		{
			name:      "gRPC permission denied emits ERROR log and wraps as internal",
			args:      args{externalID: "no-perms"},
			resendErr: grpcstatus.Error(grpccodes.PermissionDenied, "insufficient permissions"),
			wantErr:   apperr.ErrInternal,
			wantInLog: "failed to resend email code",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stub := &stubUserServiceServer{resendErr: tt.resendErr}
			addr := startUserServiceServer(t, stub)
			v, logBuf := newTestVerifier(t, addr)

			err := v.ResendVerification(context.Background(), tt.args.externalID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
			assert.Contains(t, logBuf.String(), tt.wantInLog)
			assert.Contains(t, logBuf.String(), tt.args.externalID)
			if tt.check != nil {
				tt.check(t, stub)
			}
		})
	}
}

func TestGrpcEndpoint(t *testing.T) {
	t.Parallel()

	type args struct {
		issuerURL string
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr error
	}{
		{
			name: "https URL extracts host with port 443",
			args: args{issuerURL: "https://dev-svijfm.us1.zitadel.cloud"},
			want: "dev-svijfm.us1.zitadel.cloud:443",
		},
		{
			name: "https URL with explicit port",
			args: args{issuerURL: "https://zitadel.example.com:8443"},
			want: "zitadel.example.com:8443",
		},
		{
			name: "http URL defaults to port 443",
			args: args{issuerURL: "http://zitadel.local"},
			want: "zitadel.local:443",
		},
		{
			name:    "empty URL returns error",
			args:    args{issuerURL: ""},
			wantErr: assert.AnError,
		},
		{
			name:    "scheme-only URL returns error",
			args:    args{issuerURL: "https://"},
			wantErr: assert.AnError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := infrazitadel.GrpcEndpoint(tt.args.issuerURL)

			if tt.wantErr != nil {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
