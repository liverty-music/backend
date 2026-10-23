package zitadel_test

import (
	"bytes"
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

// fakeEmailCodeClient stands in for Zitadel's v2 User Service: it holds one
// identity's address and answers each call with the configured result.
type fakeEmailCodeClient struct {
	email      string
	verified   bool
	getErr     error
	sendErr    error
	sendCode   string
	verifyErr  error
	sendCalls  []*userpb.SendEmailCodeRequest
	verifyReqs []*userpb.VerifyEmailRequest
}

func (f *fakeEmailCodeClient) GetUserByID(_ context.Context, in *userpb.GetUserByIDRequest, _ ...grpc.CallOption) (*userpb.GetUserByIDResponse, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &userpb.GetUserByIDResponse{User: &userpb.User{
		UserId: in.GetUserId(),
		Type: &userpb.User_Human{Human: &userpb.HumanUser{
			Email: &userpb.HumanEmail{Email: f.email, IsVerified: f.verified},
		}},
	}}, nil
}

func (f *fakeEmailCodeClient) SendEmailCode(_ context.Context, in *userpb.SendEmailCodeRequest, _ ...grpc.CallOption) (*userpb.SendEmailCodeResponse, error) {
	f.sendCalls = append(f.sendCalls, in)
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	return &userpb.SendEmailCodeResponse{VerificationCode: &f.sendCode}, nil
}

func (f *fakeEmailCodeClient) VerifyEmail(_ context.Context, in *userpb.VerifyEmailRequest, _ ...grpc.CallOption) (*userpb.VerifyEmailResponse, error) {
	f.verifyReqs = append(f.verifyReqs, in)
	if f.verifyErr != nil {
		return nil, f.verifyErr
	}
	return &userpb.VerifyEmailResponse{}, nil
}

func newTestVerifier(t *testing.T, client *fakeEmailCodeClient) (*infrazitadel.EmailVerifier, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	logger, err := logging.New(logging.WithWriter(buf))
	require.NoError(t, err)
	return infrazitadel.NewEmailVerifierWithClient(client, logger), buf
}

func TestEmailVerifier_IssueEmailVerificationCode(t *testing.T) {
	t.Parallel()

	type want struct {
		address string
		code    string
	}
	tests := []struct {
		name      string
		client    *fakeEmailCodeClient
		want      want
		wantErr   error
		wantSends int
	}{
		{
			name:      "an unverified address gets a returned code and no Zitadel email",
			client:    &fakeEmailCodeClient{email: "fan@example.com", sendCode: "ABC123"},
			want:      want{address: "fan@example.com", code: "ABC123"},
			wantSends: 1,
		},
		{
			name:    "an already verified address gets no code",
			client:  &fakeEmailCodeClient{email: "fan@example.com", verified: true},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name:    "an unknown identity is NotFound",
			client:  &fakeEmailCodeClient{getErr: grpcstatus.Error(grpccodes.NotFound, "user not found")},
			wantErr: apperr.ErrNotFound,
		},
		{
			name:    "an unreachable Zitadel is Unavailable",
			client:  &fakeEmailCodeClient{getErr: grpcstatus.Error(grpccodes.Unavailable, "connection refused")},
			wantErr: apperr.ErrUnavailable,
		},
		{
			name:      "a failure to issue is Internal",
			client:    &fakeEmailCodeClient{email: "fan@example.com", sendErr: grpcstatus.Error(grpccodes.Internal, "boom")},
			wantErr:   apperr.ErrInternal,
			wantSends: 1,
		},
		{
			name:      "an empty returned code is Internal",
			client:    &fakeEmailCodeClient{email: "fan@example.com"},
			wantErr:   apperr.ErrInternal,
			wantSends: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v, logBuf := newTestVerifier(t, tt.client)

			address, code, err := v.IssueEmailVerificationCode(context.Background(), "zitadel-user-1")

			require.Len(t, tt.client.sendCalls, tt.wantSends)
			for _, req := range tt.client.sendCalls {
				assert.NotNil(t, req.GetReturnCode(), "Zitadel must return the code, not send it")
			}
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, want{address: address, code: code})
			assert.NotContains(t, logBuf.String(), code, "the code is never logged")
		})
	}
}

func TestEmailVerifier_VerifyEmail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		client  *fakeEmailCodeClient
		wantErr error
	}{
		{
			// @spec components/entity/user/verify-email "Valid code"
			name:   "the latest code verifies the address",
			client: &fakeEmailCodeClient{},
		},
		{
			// @spec components/entity/user/verify-email "Code already used"
			name:    "a used code is InvalidArgument",
			client:  &fakeEmailCodeClient{verifyErr: grpcstatus.Error(grpccodes.FailedPrecondition, "Errors.User.Code.Empty")},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			// @spec components/entity/user/verify-email "Code older than 1 hour"
			name:    "an expired code is InvalidArgument",
			client:  &fakeEmailCodeClient{verifyErr: grpcstatus.Error(grpccodes.FailedPrecondition, "Errors.User.Code.Expired")},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "a wrong code is InvalidArgument",
			client:  &fakeEmailCodeClient{verifyErr: grpcstatus.Error(grpccodes.InvalidArgument, "Errors.User.Code.Invalid")},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			// @spec components/entity/user/verify-email "Unknown identity"
			name:    "an unknown identity is NotFound",
			client:  &fakeEmailCodeClient{verifyErr: grpcstatus.Error(grpccodes.NotFound, "Errors.User.NotFound")},
			wantErr: apperr.ErrNotFound,
		},
		{
			name:    "an unreachable Zitadel is Unavailable",
			client:  &fakeEmailCodeClient{verifyErr: grpcstatus.Error(grpccodes.Unavailable, "connection refused")},
			wantErr: apperr.ErrUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v, logBuf := newTestVerifier(t, tt.client)

			err := v.VerifyEmail(context.Background(), "zitadel-user-1", "ABC123")

			require.Len(t, tt.client.verifyReqs, 1)
			assert.Equal(t, "zitadel-user-1", tt.client.verifyReqs[0].GetUserId())
			assert.Equal(t, "ABC123", tt.client.verifyReqs[0].GetVerificationCode())
			assert.NotContains(t, logBuf.String(), "ABC123", "the code is never logged")
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
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
