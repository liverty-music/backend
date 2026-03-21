package zitadel_test

import (
	"context"
	"testing"

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

// stubClient implements the emailCodeClient interface for unit testing.
type stubClient struct {
	sendErr   error
	resendErr error

	lastSendUserID   string
	lastResendUserID string
}

func (s *stubClient) SendEmailCode(_ context.Context, in *userpb.SendEmailCodeRequest, _ ...grpc.CallOption) (*userpb.SendEmailCodeResponse, error) {
	s.lastSendUserID = in.UserId
	if s.sendErr != nil {
		return nil, s.sendErr
	}
	return &userpb.SendEmailCodeResponse{}, nil
}

func (s *stubClient) ResendEmailCode(_ context.Context, in *userpb.ResendEmailCodeRequest, _ ...grpc.CallOption) (*userpb.ResendEmailCodeResponse, error) {
	s.lastResendUserID = in.UserId
	if s.resendErr != nil {
		return nil, s.resendErr
	}
	return &userpb.ResendEmailCodeResponse{}, nil
}

func newTestLogger(t *testing.T) *logging.Logger {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return logger
}

func TestEmailVerifier_SendVerification(t *testing.T) {
	t.Parallel()

	type args struct {
		externalID string
	}
	tests := []struct {
		name    string
		args    args
		sendErr error
		wantErr error
		check   func(t *testing.T, stub *stubClient)
	}{
		{
			name:    "success",
			args:    args{externalID: "user-123"},
			sendErr: nil,
			wantErr: nil,
			check: func(t *testing.T, stub *stubClient) {
				t.Helper()
				assert.Equal(t, "user-123", stub.lastSendUserID)
			},
		},
		{
			name:    "gRPC unavailable wraps as internal",
			args:    args{externalID: "user-456"},
			sendErr: grpcstatus.Error(grpccodes.Unavailable, "connection refused"),
			wantErr: apperr.ErrInternal,
		},
		{
			name:    "gRPC not found wraps as internal",
			args:    args{externalID: "nonexistent"},
			sendErr: grpcstatus.Error(grpccodes.NotFound, "user not found"),
			wantErr: apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stub := &stubClient{sendErr: tt.sendErr}
			v := infrazitadel.NewTestEmailVerifier(stub, newTestLogger(t))

			err := v.SendVerification(context.Background(), tt.args.externalID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			assert.NoError(t, err)
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
		check     func(t *testing.T, stub *stubClient)
	}{
		{
			name:      "success",
			args:      args{externalID: "user-123"},
			resendErr: nil,
			wantErr:   nil,
			check: func(t *testing.T, stub *stubClient) {
				t.Helper()
				assert.Equal(t, "user-123", stub.lastResendUserID)
			},
		},
		{
			name:      "already verified maps to FailedPrecondition",
			args:      args{externalID: "verified-user"},
			resendErr: grpcstatus.Error(grpccodes.FailedPrecondition, "email already verified"),
			wantErr:   apperr.ErrFailedPrecondition,
		},
		{
			name:      "gRPC unavailable wraps as internal",
			args:      args{externalID: "user-456"},
			resendErr: grpcstatus.Error(grpccodes.Unavailable, "connection refused"),
			wantErr:   apperr.ErrInternal,
		},
		{
			name:      "gRPC internal wraps as internal",
			args:      args{externalID: "user-789"},
			resendErr: grpcstatus.Error(grpccodes.Internal, "something went wrong"),
			wantErr:   apperr.ErrInternal,
		},
		{
			name:      "gRPC permission denied wraps as internal",
			args:      args{externalID: "no-perms"},
			resendErr: grpcstatus.Error(grpccodes.PermissionDenied, "insufficient permissions"),
			wantErr:   apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			stub := &stubClient{resendErr: tt.resendErr}
			v := infrazitadel.NewTestEmailVerifier(stub, newTestLogger(t))

			err := v.ResendVerification(context.Background(), tt.args.externalID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			assert.NoError(t, err)
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
