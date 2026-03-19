package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/authn"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/infrastructure/auth/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// testMsg is a simple type for testing.
type testMsg struct{}

// noPublicProcedures is an empty allowlist for tests that do not exercise public endpoints.
var noPublicProcedures = map[string]bool{}

// testPublicProcedures marks /test.Service/PublicMethod as a public procedure.
var testPublicProcedures = map[string]bool{
	"/test.Service/PublicMethod": true,
}

func TestNewAuthFunc(t *testing.T) {
	t.Parallel()

	type args struct {
		authorizationHeader string
		url                 string
		publicProcedures    map[string]bool
	}
	type want struct {
		claimsEqual *auth.Claims
		claimsNil   bool
		connectCode connect.Code
	}
	tests := []struct {
		name      string
		args      args
		setupMock func(t *testing.T) *mocks.MockTokenValidator
		want      want
		wantErr   error
	}{
		{
			name: "return claims when token is valid",
			args: args{
				authorizationHeader: "Bearer valid-token",
				url:                 "/test.Service/Method",
				publicProcedures:    noPublicProcedures,
			},
			setupMock: func(t *testing.T) *mocks.MockTokenValidator {
				t.Helper()
				m := mocks.NewMockTokenValidator(t)
				m.On("ValidateToken", mock.Anything, "valid-token").Return(&auth.Claims{
					Sub:   "user-123",
					Email: "test@example.com",
					Name:  "Test User",
				}, nil)
				return m
			},
			want: want{
				claimsEqual: &auth.Claims{
					Sub:   "user-123",
					Email: "test@example.com",
					Name:  "Test User",
				},
			},
		},
		{
			name: "return unauthenticated error when Authorization header is missing",
			args: args{
				url:              "/test.Service/Method",
				publicProcedures: noPublicProcedures,
			},
			setupMock: func(t *testing.T) *mocks.MockTokenValidator {
				t.Helper()
				return mocks.NewMockTokenValidator(t)
			},
			want:    want{connectCode: connect.CodeUnauthenticated},
			wantErr: errors.New("unauthenticated"),
		},
		{
			name: "return unauthenticated error when token validation fails",
			args: args{
				authorizationHeader: "Bearer bad-token",
				url:                 "/test.Service/Method",
				publicProcedures:    noPublicProcedures,
			},
			setupMock: func(t *testing.T) *mocks.MockTokenValidator {
				t.Helper()
				m := mocks.NewMockTokenValidator(t)
				m.On("ValidateToken", mock.Anything, "bad-token").
					Return((*auth.Claims)(nil), errors.New("token expired"))
				return m
			},
			want:    want{connectCode: connect.CodeUnauthenticated},
			wantErr: errors.New("unauthenticated"),
		},
		{
			name: "return unauthenticated error when Authorization scheme is not Bearer",
			args: args{
				authorizationHeader: "Basic sometoken",
				url:                 "/test.Service/Method",
				publicProcedures:    noPublicProcedures,
			},
			setupMock: func(t *testing.T) *mocks.MockTokenValidator {
				t.Helper()
				return mocks.NewMockTokenValidator(t)
			},
			want:    want{connectCode: connect.CodeUnauthenticated},
			wantErr: errors.New("unauthenticated"),
		},
		{
			name: "return nil claims when public procedure is called without token",
			args: args{
				url:              "/test.Service/PublicMethod",
				publicProcedures: testPublicProcedures,
			},
			setupMock: func(t *testing.T) *mocks.MockTokenValidator {
				t.Helper()
				return mocks.NewMockTokenValidator(t)
			},
			want: want{claimsNil: true},
		},
		{
			name: "return claims when public procedure is called with valid token",
			args: args{
				authorizationHeader: "Bearer valid-token",
				url:                 "/test.Service/PublicMethod",
				publicProcedures:    testPublicProcedures,
			},
			setupMock: func(t *testing.T) *mocks.MockTokenValidator {
				t.Helper()
				m := mocks.NewMockTokenValidator(t)
				m.On("ValidateToken", mock.Anything, "valid-token").Return(&auth.Claims{
					Sub:   "user-789",
					Email: "public@example.com",
					Name:  "Public User",
				}, nil)
				return m
			},
			want: want{
				claimsEqual: &auth.Claims{
					Sub:   "user-789",
					Email: "public@example.com",
					Name:  "Public User",
				},
			},
		},
		{
			name: "return nil claims when public procedure is called with invalid token",
			args: args{
				authorizationHeader: "Bearer expired-token",
				url:                 "/test.Service/PublicMethod",
				publicProcedures:    testPublicProcedures,
			},
			setupMock: func(t *testing.T) *mocks.MockTokenValidator {
				t.Helper()
				m := mocks.NewMockTokenValidator(t)
				m.On("ValidateToken", mock.Anything, "expired-token").
					Return((*auth.Claims)(nil), errors.New("token expired"))
				return m
			},
			want: want{claimsNil: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockValidator := tt.setupMock(t)
			authFunc := auth.NewAuthFunc(mockValidator, tt.args.publicProcedures)

			req := httptest.NewRequest(http.MethodPost, tt.args.url, nil)
			if tt.args.authorizationHeader != "" {
				req.Header.Set("Authorization", tt.args.authorizationHeader)
			}

			info, err := authFunc(context.Background(), req)

			if tt.wantErr != nil {
				assert.Error(t, err)
				assert.Equal(t, tt.want.connectCode, connect.CodeOf(err))
				mockValidator.AssertExpectations(t)
				return
			}

			assert.NoError(t, err)
			if tt.want.claimsNil {
				assert.Nil(t, info)
			} else {
				claims, ok := info.(*auth.Claims)
				assert.True(t, ok)
				assert.Equal(t, tt.want.claimsEqual, claims)
			}
			mockValidator.AssertExpectations(t)
		})
	}
}

func TestClaimsBridgeInterceptor_WrapUnary(t *testing.T) {
	t.Parallel()

	type want struct {
		claimsFound bool
		claims      *auth.Claims
	}
	tests := []struct {
		name     string
		setupCtx func() context.Context
		want     want
		wantErr  error
	}{
		{
			name: "propagate claims to handler context when info is set",
			setupCtx: func() context.Context {
				return authn.SetInfo(context.Background(), &auth.Claims{
					Sub:   "user-456",
					Email: "bridge@example.com",
					Name:  "Bridge User",
				})
			},
			want: want{
				claimsFound: true,
				claims: &auth.Claims{
					Sub:   "user-456",
					Email: "bridge@example.com",
					Name:  "Bridge User",
				},
			},
		},
		{
			name: "not propagate claims to handler context when info is nil",
			setupCtx: func() context.Context {
				return context.Background()
			},
			want: want{claimsFound: false},
		},
		{
			name: "not propagate claims to handler context when info is wrong type",
			setupCtx: func() context.Context {
				return authn.SetInfo(context.Background(), "not-claims")
			},
			want: want{claimsFound: false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bridge := auth.ClaimsBridgeInterceptor{}

			var capturedCtx context.Context
			handler := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
				capturedCtx = ctx
				return connect.NewResponse(&testMsg{}), nil
			}

			wrapped := bridge.WrapUnary(handler)
			_, err := wrapped(tt.setupCtx(), connect.NewRequest(&testMsg{}))

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			assert.NoError(t, err)
			claims, ok := auth.GetClaims(capturedCtx)
			assert.Equal(t, tt.want.claimsFound, ok)
			if tt.want.claimsFound {
				assert.Equal(t, tt.want.claims, claims)
			}
		})
	}
}
