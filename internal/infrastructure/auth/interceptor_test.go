package auth_test

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/infrastructure/auth/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// testMsg is a simple type for testing
type testMsg struct{}

func TestAuthInterceptor_WrapUnary_ValidToken(t *testing.T) {
	mockValidator := mocks.NewMockTokenValidator(t)
	expectedClaims := &auth.Claims{
		Sub:   "user-123",
		Email: "test@example.com",
		Name:  "Test User",
	}
	mockValidator.On("ValidateToken", mock.Anything, "valid-token").Return(expectedClaims, nil)

	interceptor := auth.NewAuthInterceptor(mockValidator)

	var capturedClaims *auth.Claims
	handler := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		claims, ok := auth.GetClaims(ctx)
		if ok {
			capturedClaims = claims
		}
		return connect.NewResponse(&testMsg{}), nil
	}

	wrapped := interceptor.WrapUnary(handler)

	req := connect.NewRequest(&testMsg{})
	req.Header().Set("Authorization", "Bearer valid-token")

	_, err := wrapped(context.Background(), req)

	assert.NoError(t, err)
	assert.Equal(t, expectedClaims, capturedClaims)
	mockValidator.AssertExpectations(t)
}

func TestAuthInterceptor_WrapUnary_NoAuthHeader(t *testing.T) {
	mockValidator := mocks.NewMockTokenValidator(t)
	interceptor := auth.NewAuthInterceptor(mockValidator)

	var capturedContext context.Context
	handler := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		capturedContext = ctx
		return connect.NewResponse(&testMsg{}), nil
	}

	wrapped := interceptor.WrapUnary(handler)

	req := connect.NewRequest(&testMsg{})

	_, err := wrapped(context.Background(), req)

	assert.NoError(t, err)

	// No auth header, so user ID should not be set
	userID, ok := auth.GetUserID(capturedContext)
	assert.False(t, ok)
	assert.Empty(t, userID)

	// Validator should not have been called
	mockValidator.AssertNotCalled(t, "ValidateToken")
}

func TestAuthInterceptor_WrapUnary_InvalidToken(t *testing.T) {
	mockValidator := mocks.NewMockTokenValidator(t)
	mockValidator.On("ValidateToken", mock.Anything, "invalid-token").Return((*auth.Claims)(nil), errors.New("invalid token"))

	interceptor := auth.NewAuthInterceptor(mockValidator)

	handler := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		t.Error("handler should not be called")
		return connect.NewResponse(&testMsg{}), nil
	}

	wrapped := interceptor.WrapUnary(handler)

	req := connect.NewRequest(&testMsg{})
	req.Header().Set("Authorization", "Bearer invalid-token")

	_, err := wrapped(context.Background(), req)

	assert.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	mockValidator.AssertExpectations(t)
}

func TestAuthInterceptor_WrapUnary_InvalidBearerFormat(t *testing.T) {
	mockValidator := mocks.NewMockTokenValidator(t)
	interceptor := auth.NewAuthInterceptor(mockValidator)

	handler := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		t.Error("handler should not be called")
		return connect.NewResponse(&testMsg{}), nil
	}

	wrapped := interceptor.WrapUnary(handler)

	req := connect.NewRequest(&testMsg{})
	req.Header().Set("Authorization", "Basic sometoken")

	_, err := wrapped(context.Background(), req)

	assert.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	assert.Contains(t, err.Error(), "authorization header must use Bearer scheme")

	// Validator should not have been called
	mockValidator.AssertNotCalled(t, "ValidateToken")
}

func TestAuthInterceptor_WrapUnary_EmptyToken(t *testing.T) {
	mockValidator := mocks.NewMockTokenValidator(t)
	interceptor := auth.NewAuthInterceptor(mockValidator)

	handler := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		t.Error("handler should not be called")
		return connect.NewResponse(&testMsg{}), nil
	}

	wrapped := interceptor.WrapUnary(handler)

	req := connect.NewRequest(&testMsg{})
	req.Header().Set("Authorization", "Bearer ")

	_, err := wrapped(context.Background(), req)

	assert.Error(t, err)
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	assert.Contains(t, err.Error(), "authorization token is empty")

	// Validator should not have been called
	mockValidator.AssertNotCalled(t, "ValidateToken")
}

func TestNewAuthInterceptor(t *testing.T) {
	mockValidator := mocks.NewMockTokenValidator(t)
	interceptor := auth.NewAuthInterceptor(mockValidator)

	assert.NotNil(t, interceptor)
}
