package zitadel_test

import (
	"bytes"
	"context"
	"testing"

	userpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user/v2"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	infrazitadel "github.com/liverty-music/backend/internal/infrastructure/zitadel"
	"github.com/liverty-music/backend/internal/infrastructure/zitadel/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const providerUserID = "zitadel-user-1"

func newTestVerifier(t *testing.T, client infrazitadel.EmailCodeClient) (*infrazitadel.EmailVerifier, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	logger, err := logging.New(logging.WithWriter(buf))
	require.NoError(t, err)
	return infrazitadel.NewEmailVerifierWithClient(client, logger), buf
}

// identity answers GetUserByID with a human identity holding address.
func identity(address string, verified bool) *userpb.GetUserByIDResponse {
	return &userpb.GetUserByIDResponse{User: &userpb.User{
		UserId: providerUserID,
		Type: &userpb.User_Human{Human: &userpb.HumanUser{
			Email: &userpb.HumanEmail{Email: address, IsVerified: verified},
		}},
	}}
}

// returnCodeRequest matches a SendEmailCode request for the identity that
// asks Zitadel to return the code instead of sending it.
var returnCodeRequest = mock.MatchedBy(func(in *userpb.SendEmailCodeRequest) bool {
	return in.GetUserId() == providerUserID && in.GetReturnCode() != nil
})

func TestEmailVerifier_IssueEmailVerificationCode(t *testing.T) {
	t.Parallel()

	type want struct {
		address string
		code    string
	}
	tests := []struct {
		name    string
		setup   func(c *mocks.MockEmailCodeClient)
		want    want
		wantErr error
	}{
		{
			name: "an unverified address gets a returned code and no Zitadel email",
			setup: func(c *mocks.MockEmailCodeClient) {
				c.EXPECT().GetUserByID(mock.Anything, mock.Anything).Return(identity("fan@example.com", false), nil).Once()
				code := "ABC123"
				c.EXPECT().SendEmailCode(mock.Anything, returnCodeRequest).Return(&userpb.SendEmailCodeResponse{VerificationCode: &code}, nil).Once()
			},
			want: want{address: "fan@example.com", code: "ABC123"},
		},
		{
			name: "an already verified address gets no code",
			setup: func(c *mocks.MockEmailCodeClient) {
				c.EXPECT().GetUserByID(mock.Anything, mock.Anything).Return(identity("fan@example.com", true), nil).Once()
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name: "an unknown identity is NotFound",
			setup: func(c *mocks.MockEmailCodeClient) {
				c.EXPECT().GetUserByID(mock.Anything, mock.Anything).Return(nil, grpcstatus.Error(grpccodes.NotFound, "user not found")).Once()
			},
			wantErr: apperr.ErrNotFound,
		},
		{
			name: "an unreachable Zitadel is Unavailable",
			setup: func(c *mocks.MockEmailCodeClient) {
				c.EXPECT().GetUserByID(mock.Anything, mock.Anything).Return(nil, grpcstatus.Error(grpccodes.Unavailable, "connection refused")).Once()
			},
			wantErr: apperr.ErrUnavailable,
		},
		{
			name: "a failure to issue is Internal",
			setup: func(c *mocks.MockEmailCodeClient) {
				c.EXPECT().GetUserByID(mock.Anything, mock.Anything).Return(identity("fan@example.com", false), nil).Once()
				c.EXPECT().SendEmailCode(mock.Anything, returnCodeRequest).Return(nil, grpcstatus.Error(grpccodes.Internal, "boom")).Once()
			},
			wantErr: apperr.ErrInternal,
		},
		{
			name: "an empty returned code is Internal",
			setup: func(c *mocks.MockEmailCodeClient) {
				c.EXPECT().GetUserByID(mock.Anything, mock.Anything).Return(identity("fan@example.com", false), nil).Once()
				c.EXPECT().SendEmailCode(mock.Anything, returnCodeRequest).Return(&userpb.SendEmailCodeResponse{}, nil).Once()
			},
			wantErr: apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := mocks.NewMockEmailCodeClient(t)
			tt.setup(client)
			v, logBuf := newTestVerifier(t, client)

			address, code, err := v.IssueEmailVerificationCode(context.Background(), providerUserID)

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
		name      string
		verifyErr error
		wantErr   error
	}{
		{
			// @spec components/entity/user/verify-email "Valid code"
			name: "the latest code verifies the address",
		},
		{
			// @spec components/entity/user/verify-email "Code already used"
			name:      "a used code is InvalidArgument",
			verifyErr: grpcstatus.Error(grpccodes.FailedPrecondition, "Errors.User.Code.Empty"),
			wantErr:   apperr.ErrInvalidArgument,
		},
		{
			// @spec components/entity/user/verify-email "Code older than 1 hour"
			name:      "an expired code is InvalidArgument",
			verifyErr: grpcstatus.Error(grpccodes.FailedPrecondition, "Errors.User.Code.Expired"),
			wantErr:   apperr.ErrInvalidArgument,
		},
		{
			name:      "a wrong code is InvalidArgument",
			verifyErr: grpcstatus.Error(grpccodes.InvalidArgument, "Errors.User.Code.Invalid"),
			wantErr:   apperr.ErrInvalidArgument,
		},
		{
			// @spec components/entity/user/verify-email "Unknown identity"
			name:      "an unknown identity is NotFound",
			verifyErr: grpcstatus.Error(grpccodes.NotFound, "Errors.User.NotFound"),
			wantErr:   apperr.ErrNotFound,
		},
		{
			name:      "an unreachable Zitadel is Unavailable",
			verifyErr: grpcstatus.Error(grpccodes.Unavailable, "connection refused"),
			wantErr:   apperr.ErrUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := mocks.NewMockEmailCodeClient(t)
			var resp *userpb.VerifyEmailResponse
			if tt.verifyErr == nil {
				resp = &userpb.VerifyEmailResponse{}
			}
			client.EXPECT().VerifyEmail(mock.Anything, mock.MatchedBy(func(in *userpb.VerifyEmailRequest) bool {
				return in.GetUserId() == providerUserID && in.GetVerificationCode() == "ABC123"
			})).Return(resp, tt.verifyErr).Once()
			v, logBuf := newTestVerifier(t, client)

			err := v.VerifyEmail(context.Background(), providerUserID, "ABC123")

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
