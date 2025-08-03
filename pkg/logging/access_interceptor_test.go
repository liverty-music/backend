package logging_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/liverty-music/backend/pkg/logging"
	"github.com/stretchr/testify/assert"
)

// mockMessage represents a simple message for testing.
type mockMessage struct {
	Value string `json:"value"`
}

// mockRequestWithProcedure wraps a Connect request to override the procedure.
type mockRequestWithProcedure struct {
	*connect.Request[mockMessage]
	procedure string
}

func (m *mockRequestWithProcedure) Spec() connect.Spec {
	spec := m.Request.Spec()
	spec.Procedure = m.procedure
	return spec
}

// TestNewAccessLogInterceptor tests the access log interceptor functionality.
func TestNewAccessLogInterceptor(t *testing.T) {
	t.Parallel()

	// Create error instances to reuse for proper error assertion
	connectErr := connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user data"))
	unknownErr := errors.New("unexpected error")

	type args struct {
		procedure string
		headers   map[string]string
		err       error
	}

	tests := []struct {
		name       string
		args       args
		wantErr    error
		wantStatus string
	}{
		{
			name: "return success log when request succeeds",
			args: args{
				procedure: "/api.UserService/GetUser",
				headers: map[string]string{
					"User-Agent":      "connect-go/1.18.1 (go1.21.0)",
					"X-Forwarded-For": "192.168.1.100",
					"X-Http-Method":   "POST",
				},
				err: nil,
			},
			wantErr:    nil,
			wantStatus: "ok",
		},
		{
			name: "return error log when request fails with connect error",
			args: args{
				procedure: "/api.UserService/CreateUser",
				headers: map[string]string{
					"User-Agent":    "buf/1.55.1",
					"X-Real-IP":     "10.0.0.1",
					"X-Http-Method": "POST",
				},
				err: connectErr,
			},
			wantErr:    connectErr,
			wantStatus: "invalid_argument",
		},
		{
			name: "return unknown error log when request fails with unknown error",
			args: args{
				procedure: "/api.PostService/GetPost",
				headers: map[string]string{
					"User-Agent": "test-client/1.0",
				},
				err: unknownErr,
			},
			wantErr:    unknownErr,
			wantStatus: "unknown",
		},
		{
			name: "return log with empty headers when no headers provided",
			args: args{
				procedure: "/api.UserService/ListUsers",
				headers:   map[string]string{},
				err:       nil,
			},
			wantErr:    nil,
			wantStatus: "ok",
		},
		{
			name: "return log with X-Real-IP when X-Forwarded-For is not present",
			args: args{
				procedure: "/api.UserService/GetUser",
				headers: map[string]string{
					"User-Agent": "connect-go/1.18.1",
					"X-Real-IP":  "172.16.0.1",
				},
				err: nil,
			},
			wantErr:    nil,
			wantStatus: "ok",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			// Create logger with buffer output and without time/duration for consistent testing
			logger := logging.New(
				logging.WithLevel(slog.LevelInfo),
				logging.WithFormat(logging.FormatJSON),
				logging.WithWriter(&buf),
				logging.WithReplaceAttr(func(_ []string, a slog.Attr) slog.Attr {
					if a.Key == slog.TimeKey || a.Key == "duration_ms" {
						return slog.Attr{} // Discard time and duration attributes for test stability
					}
					return a
				}),
			)

			// Create access log interceptor
			interceptor := logging.NewAccessLogInterceptor(logger)

			// Create Connect request with message
			req := connect.NewRequest(&mockMessage{Value: "test"})

			// Set headers
			for key, value := range tc.args.headers {
				req.Header().Set(key, value)
			}

			// Create a mock request that returns the desired procedure
			mockReq := &mockRequestWithProcedure{
				Request:   req,
				procedure: tc.args.procedure,
			}

			// Create mock next function
			next := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
				if tc.args.err != nil {
					return nil, tc.args.err
				}
				return connect.NewResponse(&mockMessage{Value: "response"}), nil
			}

			// Execute interceptor
			handler := interceptor(next)
			resp, err := handler(context.Background(), mockReq)

			// Verify error handling
			if tc.wantErr != nil {
				assert.Error(t, err)
				assert.ErrorIs(t, err, tc.wantErr)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, resp)
			}

			// Verify log output using JSONEq
			logOutput := strings.TrimSpace(buf.String())
			assert.NotEmpty(t, logOutput, "Expected log output but got empty")

			// Extract expected values for JSON comparison
			expectedUserAgent := tc.args.headers["User-Agent"]
			expectedRemoteAddr := tc.args.headers["X-Forwarded-For"]
			if expectedRemoteAddr == "" {
				expectedRemoteAddr = tc.args.headers["X-Real-IP"]
			}
			expectedMethod := tc.args.headers["X-Http-Method"]
			if expectedMethod == "" {
				expectedMethod = "POST"
			}

			// Build expected JSON
			expectedJSON := fmt.Sprintf(`{
				"level": "INFO",
				"msg": "Access log",
				"procedure": "%s",
				"method": "%s",
				"status": "%s",
				"user_agent": "%s",
				"remote_addr": "%s"
			}`, tc.args.procedure, expectedMethod, tc.wantStatus, expectedUserAgent, expectedRemoteAddr)

			// Use JSONEq for proper JSON comparison
			assert.JSONEq(t, expectedJSON, logOutput)
		})
	}
}

// TestAccessLogInterceptor_HeaderExtraction tests specific header extraction scenarios.
func TestAccessLogInterceptor_HeaderExtraction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		headers            map[string]string
		expectedUserAgent  string
		expectedRemoteAddr string
		expectedMethod     string
	}{
		{
			name: "extract X-Forwarded-For when both headers present",
			headers: map[string]string{
				"X-Forwarded-For": "192.168.1.100",
				"X-Real-IP":       "10.0.0.1",
				"User-Agent":      "test-client/1.0",
			},
			expectedUserAgent:  "test-client/1.0",
			expectedRemoteAddr: "192.168.1.100",
			expectedMethod:     "POST",
		},
		{
			name: "extract X-Real-IP when X-Forwarded-For is empty",
			headers: map[string]string{
				"X-Real-IP":     "10.0.0.1",
				"User-Agent":    "buf/1.55.1",
				"X-Http-Method": "PUT",
			},
			expectedUserAgent:  "buf/1.55.1",
			expectedRemoteAddr: "10.0.0.1",
			expectedMethod:     "PUT",
		},
		{
			name: "use default method when X-Http-Method is not present",
			headers: map[string]string{
				"User-Agent": "connect-go/1.18.1",
			},
			expectedUserAgent:  "connect-go/1.18.1",
			expectedRemoteAddr: "",
			expectedMethod:     "POST",
		},
		{
			name:               "handle empty headers",
			headers:            map[string]string{},
			expectedUserAgent:  "",
			expectedRemoteAddr: "",
			expectedMethod:     "POST",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			logger := logging.New(
				logging.WithLevel(slog.LevelInfo),
				logging.WithFormat(logging.FormatJSON),
				logging.WithWriter(&buf),
				logging.WithReplaceAttr(func(_ []string, a slog.Attr) slog.Attr {
					if a.Key == slog.TimeKey || a.Key == "duration_ms" {
						return slog.Attr{}
					}
					return a
				}),
			)

			interceptor := logging.NewAccessLogInterceptor(logger)

			// Create Connect request
			req := connect.NewRequest(&mockMessage{Value: "test"})

			// Set headers
			for key, value := range tc.headers {
				req.Header().Set(key, value)
			}

			// Create a mock request that returns the desired procedure
			mockReq := &mockRequestWithProcedure{
				Request:   req,
				procedure: "/api.UserService/GetUser",
			}

			next := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
				return connect.NewResponse(&mockMessage{Value: "response"}), nil
			}

			handler := interceptor(next)
			_, err := handler(context.Background(), mockReq)

			assert.NoError(t, err)

			logOutput := strings.TrimSpace(buf.String())
			assert.NotEmpty(t, logOutput)

			// Build expected JSON for header extraction test
			expectedJSON := fmt.Sprintf(`{
				"level": "INFO",
				"msg": "Access log",
				"procedure": "/api.UserService/GetUser",
				"method": "%s",
				"status": "ok",
				"user_agent": "%s",
				"remote_addr": "%s"
			}`, tc.expectedMethod, tc.expectedUserAgent, tc.expectedRemoteAddr)

			// Use JSONEq for proper JSON comparison
			assert.JSONEq(t, expectedJSON, logOutput)
		})
	}
}
