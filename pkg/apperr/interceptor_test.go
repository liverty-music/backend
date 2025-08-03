package apperr_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	"github.com/liverty-music/backend/pkg/apperr"
	"github.com/liverty-music/backend/pkg/apperr/codes"
	"github.com/liverty-music/backend/pkg/logging"
)

func TestInterceptor(t *testing.T) {
	t.Parallel()

	type args struct {
		err error
	}

	type want struct {
		connectCode     codes.Code
		loggedErrString string
		metadata        map[string]string // Expected metadata in Connect error
	}

	tests := []struct {
		name string
		args args
		want want
	}{
		{
			name: "no error conversion when error is nil",
			args: args{
				err: nil,
			},
			want: want{
				connectCode:     codes.Code(0), // Will be ignored since no error expected
				loggedErrString: "",
				metadata:        nil,
			},
		},
		{
			name: "convert AppErr client error to Connect error without logging",
			args: args{
				err: apperr.New(codes.InvalidArgument, "invalid input", slog.String("field", "email")),
			},
			want: want{
				connectCode:     connect.CodeInvalidArgument,
				loggedErrString: "",
				metadata: map[string]string{
					"field": "email",
				},
			},
		},
		{
			name: "convert AppErr server error to Connect error with logging",
			args: args{
				err: apperr.New(codes.Internal, "database error", slog.String("operation", "insert")),
			},
			want: want{
				connectCode:     connect.CodeInternal,
				loggedErrString: "database error",
				metadata: map[string]string{
					"operation": "insert",
				},
			},
		},
		{
			name: "convert non-AppErr error to Unknown with logging",
			args: args{
				err: errors.New("unexpected error"),
			},
			want: want{
				connectCode:     connect.CodeUnknown,
				loggedErrString: "unexpected error",
				metadata:        map[string]string{}, // No metadata for non-AppErr errors
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create a buffer to capture log output
			logBuffer := &bytes.Buffer{}
			logger := logging.New(
				logging.WithWriter(logBuffer),
				logging.WithFormat(logging.FormatJSON), // Use JSON format for easier parsing
				logging.WithLevel(slog.LevelDebug),     // Ensure we capture all log levels
			)
			interceptor := apperr.NewInterceptor(logger)

			// Mock handler that returns the test error
			mockHandler := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
				return nil, tt.args.err
			}

			// Apply interceptor
			interceptedHandler := interceptor(mockHandler)

			// Call the intercepted handler
			_, err := interceptedHandler(context.Background(), connect.NewRequest(&struct{}{}))

			// Verify error conversion
			if tt.args.err == nil {
				// When no error is passed, interceptor should not return an error
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)

				var connectErr *connect.Error
				assert.True(t, errors.As(err, &connectErr))
				assert.Equal(t, tt.want.connectCode, connectErr.Code())

				// Verify metadata
				if tt.want.metadata != nil {
					for key, expectedValue := range tt.want.metadata {
						actualValue := connectErr.Meta().Get(key)
						assert.Equal(t, expectedValue, actualValue, "Expected metadata key '%s' to have value '%s', got '%s'", key, expectedValue, actualValue)
					}

					// Verify no extra metadata beyond what's expected
					// Count the metadata entries by iterating over HTTP headers
					metadataCount := 0
					for range connectErr.Meta() {
						metadataCount++
					}
					assert.Lenf(t, tt.want.metadata, metadataCount, "Expected %d metadata entries, got %d", len(tt.want.metadata), metadataCount)
				}
			}

			// Verify logging behavior
			logOutput := logBuffer.String()
			if tt.want.loggedErrString != "" {
				assert.Contains(t, logOutput, tt.want.loggedErrString)
			} else {
				// For client errors or nil errors, there should be no logging output
				assert.Empty(t, logOutput, "Expected no logging output for client error or nil error")
			}
		})
	}
}

func TestIsServerError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code codes.Code
		want bool
	}{
		// Server errors (5xx)
		{name: "Internal is server error", code: codes.Internal, want: true},
		{name: "Unknown is server error", code: codes.Unknown, want: true},
		{name: "DataLoss is server error", code: codes.DataLoss, want: true},
		{name: "Unavailable is server error", code: codes.Unavailable, want: true},

		// Client errors (4xx)
		{name: "InvalidArgument is client error", code: codes.InvalidArgument, want: false},
		{name: "NotFound is client error", code: codes.NotFound, want: false},
		{name: "AlreadyExists is client error", code: codes.AlreadyExists, want: false},
		{name: "PermissionDenied is client error", code: codes.PermissionDenied, want: false},
		{name: "FailedPrecondition is client error", code: codes.FailedPrecondition, want: false},
		{name: "OutOfRange is client error", code: codes.OutOfRange, want: false},
		{name: "Unimplemented is server error", code: codes.Unimplemented, want: true},
		{name: "Unauthenticated is client error", code: codes.Unauthenticated, want: false},
		{name: "Canceled is client error", code: codes.Canceled, want: false},
		{name: "DeadlineExceeded is client error", code: codes.DeadlineExceeded, want: false},
		{name: "Aborted is client error", code: codes.Aborted, want: false},
		{name: "ResourceExhausted is client error", code: codes.ResourceExhausted, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create a buffer to capture log output
			logBuffer := &bytes.Buffer{}
			logger := logging.New(
				logging.WithWriter(logBuffer),
				logging.WithFormat(logging.FormatJSON),
				logging.WithLevel(slog.LevelDebug),
			)
			interceptor := apperr.NewInterceptor(logger)

			appErr := apperr.New(tt.code, "test error")
			mockHandler := func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
				return nil, appErr
			}

			interceptedHandler := interceptor(mockHandler)
			_, err := interceptedHandler(context.Background(), connect.NewRequest(&struct{}{}))

			assert.Error(t, err)
			var connectErr *connect.Error
			assert.True(t, errors.As(err, &connectErr))
			assert.Equal(t, tt.code, connectErr.Code())

			// Verify logging behavior matches the server error classification
			logOutput := logBuffer.String()
			if tt.want {
				// Server error should be logged
				assert.NotEmpty(t, logOutput, "Expected logging output for server error")
				assert.Contains(t, logOutput, "Server error occurred")
			} else {
				// Client error should not be logged
				assert.Empty(t, logOutput, "Expected no logging output for client error")
			}
		})
	}
}
