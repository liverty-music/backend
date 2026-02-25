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

// publicProcedures marks /test.Service/PublicMethod as a public procedure.
var testPublicProcedures = map[string]bool{
	"/test.Service/PublicMethod": true,
}

func TestNewAuthFunc_ValidToken(t *testing.T) {
	mockValidator := mocks.NewMockTokenValidator(t)
	expectedClaims := &auth.Claims{
		Sub:   "user-123",
		Email: "test@example.com",
		Name:  "Test User",
	}
	mockValidator.On("ValidateToken", mock.Anything, "valid-token").Return(expectedClaims, nil)

	authFunc := auth.NewAuthFunc(mockValidator, noPublicProcedures)

	req := httptest.NewRequest(http.MethodPost, "/test.Service/Method", nil)
	req.Header.Set("Authorization", "Bearer valid-token")

	info, err := authFunc(context.Background(), req)

	assert.NoError(t, err)
	claims, ok := info.(*auth.Claims)
	assert.True(t, ok)
	assert.Equal(t, expectedClaims, claims)
	mockValidator.AssertExpectations(t)
}

func TestNewAuthFunc_MissingToken(t *testing.T) {
	mockValidator := mocks.NewMockTokenValidator(t)

	authFunc := auth.NewAuthFunc(mockValidator, noPublicProcedures)

	req := httptest.NewRequest(http.MethodPost, "/test.Service/Method", nil)

	_, err := authFunc(context.Background(), req)

	assert.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	mockValidator.AssertNotCalled(t, "ValidateToken")
}

func TestNewAuthFunc_InvalidToken(t *testing.T) {
	mockValidator := mocks.NewMockTokenValidator(t)
	mockValidator.On("ValidateToken", mock.Anything, "bad-token").
		Return((*auth.Claims)(nil), errors.New("token expired"))

	authFunc := auth.NewAuthFunc(mockValidator, noPublicProcedures)

	req := httptest.NewRequest(http.MethodPost, "/test.Service/Method", nil)
	req.Header.Set("Authorization", "Bearer bad-token")

	_, err := authFunc(context.Background(), req)

	assert.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	mockValidator.AssertExpectations(t)
}

func TestNewAuthFunc_MalformedBearer(t *testing.T) {
	mockValidator := mocks.NewMockTokenValidator(t)

	authFunc := auth.NewAuthFunc(mockValidator, noPublicProcedures)

	req := httptest.NewRequest(http.MethodPost, "/test.Service/Method", nil)
	req.Header.Set("Authorization", "Basic sometoken")

	_, err := authFunc(context.Background(), req)

	assert.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	mockValidator.AssertNotCalled(t, "ValidateToken")
}

func TestNewAuthFunc_PublicProcedure_NoToken(t *testing.T) {
	mockValidator := mocks.NewMockTokenValidator(t)

	authFunc := auth.NewAuthFunc(mockValidator, testPublicProcedures)

	req := httptest.NewRequest(http.MethodPost, "/test.Service/PublicMethod", nil)

	info, err := authFunc(context.Background(), req)

	assert.NoError(t, err)
	assert.Nil(t, info)
	mockValidator.AssertNotCalled(t, "ValidateToken")
}

func TestNewAuthFunc_PublicProcedure_ValidToken(t *testing.T) {
	mockValidator := mocks.NewMockTokenValidator(t)
	expectedClaims := &auth.Claims{
		Sub:   "user-789",
		Email: "public@example.com",
		Name:  "Public User",
	}
	mockValidator.On("ValidateToken", mock.Anything, "valid-token").Return(expectedClaims, nil)

	authFunc := auth.NewAuthFunc(mockValidator, testPublicProcedures)

	req := httptest.NewRequest(http.MethodPost, "/test.Service/PublicMethod", nil)
	req.Header.Set("Authorization", "Bearer valid-token")

	info, err := authFunc(context.Background(), req)

	assert.NoError(t, err)
	claims, ok := info.(*auth.Claims)
	assert.True(t, ok)
	assert.Equal(t, expectedClaims, claims)
	mockValidator.AssertExpectations(t)
}

func TestNewAuthFunc_PublicProcedure_InvalidToken(t *testing.T) {
	mockValidator := mocks.NewMockTokenValidator(t)
	mockValidator.On("ValidateToken", mock.Anything, "expired-token").
		Return((*auth.Claims)(nil), errors.New("token expired"))

	authFunc := auth.NewAuthFunc(mockValidator, testPublicProcedures)

	req := httptest.NewRequest(http.MethodPost, "/test.Service/PublicMethod", nil)
	req.Header.Set("Authorization", "Bearer expired-token")

	info, err := authFunc(context.Background(), req)

	assert.NoError(t, err)
	assert.Nil(t, info)
	mockValidator.AssertExpectations(t)
}

func TestClaimsBridgeInterceptor_WrapUnary_WithClaims(t *testing.T) {
	expectedClaims := &auth.Claims{
		Sub:   "user-456",
		Email: "bridge@example.com",
		Name:  "Bridge User",
	}

	bridge := auth.ClaimsBridgeInterceptor{}

	var capturedClaims *auth.Claims
	handler := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		claims, ok := auth.GetClaims(ctx)
		if ok {
			capturedClaims = claims
		}
		return connect.NewResponse(&testMsg{}), nil
	}

	wrapped := bridge.WrapUnary(handler)

	// Simulate authn middleware having set info on context
	ctx := authn.SetInfo(context.Background(), expectedClaims)
	req := connect.NewRequest(&testMsg{})

	_, err := wrapped(ctx, req)

	assert.NoError(t, err)
	assert.Equal(t, expectedClaims, capturedClaims)
}

func TestClaimsBridgeInterceptor_WrapUnary_NilInfo(t *testing.T) {
	bridge := auth.ClaimsBridgeInterceptor{}

	var capturedContext context.Context
	handler := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		capturedContext = ctx
		return connect.NewResponse(&testMsg{}), nil
	}

	wrapped := bridge.WrapUnary(handler)

	req := connect.NewRequest(&testMsg{})

	_, err := wrapped(context.Background(), req)

	assert.NoError(t, err)
	_, ok := auth.GetClaims(capturedContext)
	assert.False(t, ok)
}

func TestClaimsBridgeInterceptor_WrapUnary_WrongInfoType(t *testing.T) {
	bridge := auth.ClaimsBridgeInterceptor{}

	var capturedContext context.Context
	handler := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		capturedContext = ctx
		return connect.NewResponse(&testMsg{}), nil
	}

	wrapped := bridge.WrapUnary(handler)

	// Set info with wrong type
	ctx := authn.SetInfo(context.Background(), "not-claims")
	req := connect.NewRequest(&testMsg{})

	_, err := wrapped(ctx, req)

	assert.NoError(t, err)
	_, ok := auth.GetClaims(capturedContext)
	assert.False(t, ok)
}
