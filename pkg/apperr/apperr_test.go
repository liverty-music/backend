package apperr

import (
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/liverty-music/backend/pkg/apperr/codes"
)

// Interface method tests - verify AppErr implements error interface correctly

func TestAppErr_Error(t *testing.T) {
	tests := []struct {
		name   string
		appErr *AppErr
		want   string
	}{
		{
			name: "returns formatted message when no cause error",
			appErr: &AppErr{
				Code: codes.InvalidArgument,
				Msg:  "invalid input (invalid_argument)",
			},
			want: "invalid input (invalid_argument)",
		},
		{
			name: "returns formatted message when cause error exists",
			appErr: &AppErr{
				Cause: errors.New("database error"),
				Code:  codes.Internal,
				Msg:   "failed to process request: database error (internal)",
			},
			want: "failed to process request: database error (internal)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.appErr.Error(); got != tt.want {
				t.Errorf("AppErr.Error() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAppErr_Unwrap(t *testing.T) {
	originalErr := errors.New("original error")

	tests := []struct {
		name   string
		appErr *AppErr
		want   error
	}{
		{
			name: "returns underlying cause when present",
			appErr: &AppErr{
				Cause: originalErr,
				Code:  codes.Internal,
				Msg:   "test error: original error (Internal)",
			},
			want: originalErr,
		},
		{
			name: "returns nil when no underlying cause",
			appErr: &AppErr{
				Code: codes.InvalidArgument,
				Msg:  "test error (InvalidArgument)",
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.want == nil {
				if got := tt.appErr.Unwrap(); got != nil {
					t.Errorf("AppErr.Unwrap() = %v, want nil", got)
				}
			} else {
				if got := tt.appErr.Unwrap(); !errors.Is(got, tt.want) {
					t.Errorf("AppErr.Unwrap() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestAppErr_Is(t *testing.T) {
	originalErr := errors.New("original error")

	type args struct {
		target error
	}

	tests := []struct {
		name   string
		appErr *AppErr
		args   args
		want   bool
	}{
		{
			name: "returns true when underlying cause matches target",
			appErr: &AppErr{
				Cause: sql.ErrNoRows,
				Code:  codes.Internal,
				Msg:   "test error",
			},
			args: args{
				target: sql.ErrNoRows,
			},
			want: true,
		},
		{
			name: "returns false when underlying cause does not match target",
			appErr: &AppErr{
				Cause: sql.ErrNoRows,
				Code:  codes.Internal,
				Msg:   "test error",
			},
			args: args{
				target: sql.ErrTxDone,
			},
			want: false,
		},
		{
			name: "returns false when no underlying cause",
			appErr: &AppErr{
				Code: codes.InvalidArgument,
				Msg:  "test error",
			},
			args: args{
				target: originalErr,
			},
			want: false,
		},
		{
			name: "returns true when target is AppErr with same gRPC code",
			appErr: &AppErr{
				Cause: sql.ErrNoRows,
				Code:  codes.Internal,
				Msg:   "test error",
			},
			args: args{
				target: ErrInternal,
			},
			want: true,
		},
		{
			name: "returns false when target is AppErr with different gRPC code",
			appErr: &AppErr{
				Cause: sql.ErrNoRows,
				Code:  codes.Internal,
				Msg:   "test error",
			},
			args: args{
				target: ErrInvalidArgument,
			},
			want: false,
		},
		{
			name: "returns true when both AppErrs have same code and no cause",
			appErr: &AppErr{
				Code: codes.NotFound,
				Msg:  "test error",
			},
			args: args{
				target: ErrNotFound,
			},
			want: true,
		},
		{
			name: "returns false when both AppErrs have different codes and no cause",
			appErr: &AppErr{
				Code: codes.NotFound,
				Msg:  "test error",
			},
			args: args{
				target: ErrInternal,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.appErr.Is(tt.args.target); got != tt.want {
				t.Errorf("AppErr.Is() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAppErr_LogValue(t *testing.T) {
	originalErr := errors.New("database error")
	attrs := []slog.Attr{
		slog.String("user_id", "123"),
		slog.Int("attempt", 3),
	}

	tests := []struct {
		name      string
		appErr    *AppErr
		want      map[string]string // expected top-level key-value pairs
		wantAttrs map[string]string // expected attributes in the attrs group
	}{
		{
			name: "includes all fields when underlying cause is present",
			appErr: &AppErr{
				Cause: originalErr,
				Code:  codes.Internal,
				Msg:   "test error",
				Attrs: attrs,
			},
			want: map[string]string{
				"msg":   "test error",
				"code":  "internal",
				"cause": "database error",
			},
			wantAttrs: map[string]string{
				"user_id": "123",
				"attempt": "3",
			},
		},
		{
			name: "includes fields when no underlying cause",
			appErr: &AppErr{
				Code:  codes.InvalidArgument,
				Msg:   "test error",
				Attrs: attrs,
			},
			want: map[string]string{
				"msg":  "test error",
				"code": "invalid_argument",
			},
			wantAttrs: map[string]string{
				"user_id": "123",
				"attempt": "3",
			},
		},
		{
			name: "handles empty attributes",
			appErr: &AppErr{
				Code:  codes.NotFound,
				Msg:   "not found",
				Attrs: nil,
			},
			want: map[string]string{
				"msg":  "not found",
				"code": "not_found",
			},
			wantAttrs: map[string]string{},
		},
		{
			name: "handles AppErr as cause",
			appErr: &AppErr{
				Cause: &AppErr{
					Code: codes.InvalidArgument,
					Msg:  "invalid input",
				},
				Code:  codes.Internal,
				Msg:   "wrapped error",
				Attrs: attrs,
			},
			want: map[string]string{
				"msg":   "wrapped error",
				"code":  "internal",
				"cause": "invalid input (invalid_argument)",
			},
			wantAttrs: map[string]string{
				"user_id": "123",
				"attempt": "3",
			},
		},
		{
			name: "handles nil cause",
			appErr: &AppErr{
				Cause: nil,
				Code:  codes.Unknown,
				Msg:   "unknown error",
				Attrs: attrs,
			},
			want: map[string]string{
				"msg":  "unknown error",
				"code": "unknown",
			},
			wantAttrs: map[string]string{
				"user_id": "123",
				"attempt": "3",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logValue := tt.appErr.LogValue()
			if logValue.Kind() != slog.KindGroup {
				t.Errorf("LogValue() should return a group, got %v", logValue.Kind())
			}

			group := logValue.Group()
			if group == nil {
				t.Fatal("LogValue() group should not be nil")
			}

			fieldLen := len(tt.want)
			if len(tt.wantAttrs) > 0 {
				fieldLen++
			}

			if len(group) != fieldLen {
				t.Errorf("Expected %d attributes, got %d", fieldLen, len(group))
			}

			for _, attr := range group {
				if attr.Key == "attrs" {
					validateAttrsGroup(t, attr.Value.Group(), tt.wantAttrs)
				} else {
					if _, exists := tt.want[attr.Key]; !exists {
						t.Errorf("Unexpected attribute found: %s=%s", attr.Key, attr.Value.String())
					}
				}
			}
		})
	}
}

func validateAttrsGroup(t *testing.T, attrsGroup []slog.Attr, wantAttrs map[string]string) {
	for _, attr := range attrsGroup {
		if _, exists := wantAttrs[attr.Key]; !exists {
			t.Errorf("Unexpected attribute found in attrs group: %s=%s", attr.Key, attr.Value.String())
		}
	}
}

// Constructor tests - verify New and Wrap functions work correctly

func TestNew(t *testing.T) {
	type args struct {
		code  codes.Code
		msg   string
		attrs []slog.Attr
	}

	type want struct {
		err      error
		code     codes.Code
		attrs    []slog.Attr
		errorStr string
	}

	tests := []struct {
		name string
		args args
		want want
	}{
		{
			name: "creates AppErr with attributes and stack trace",
			args: args{
				code:  codes.InvalidArgument,
				msg:   "invalid email format",
				attrs: []slog.Attr{slog.String("field", "email"), slog.String("value", "invalid-email")},
			},
			want: want{
				err:      ErrInvalidArgument,
				code:     codes.InvalidArgument,
				attrs:    []slog.Attr{slog.String("field", "email"), slog.String("value", "invalid-email")},
				errorStr: "invalid email format (invalid_argument)",
			},
		},
		{
			name: "creates AppErr without additional attributes",
			args: args{
				code:  codes.Internal,
				msg:   "internal server error",
				attrs: nil,
			},
			want: want{
				err:      ErrInternal,
				code:     codes.Internal,
				attrs:    nil,
				errorStr: "internal server error (internal)",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := New(tt.args.code, tt.args.msg, tt.args.attrs...)

			// Assert that errors.Is(err, want.err) is true
			if !errors.Is(err, tt.want.err) {
				t.Errorf("errors.Is(err, want.err) = false, want true (err: %v, want.err: %v)", err, tt.want.err)
			}

			// Extract AppErr for testing
			var appErr *AppErr
			if !errors.As(err, &appErr) {
				t.Fatal("New() should return an error that can be converted to *AppErr")
			}

			// Test basic fields
			if appErr.Cause != nil {
				t.Errorf("New() Cause should be nil, got %v", appErr.Cause)
			}

			if appErr.Code != tt.want.code {
				t.Errorf("New() Code = %v, want %v", appErr.Code, tt.want.code)
			}

			// Test attributes
			expectedCount := len(tt.want.attrs) + 1 // +1 for stacktrace
			if len(appErr.Attrs) != expectedCount {
				t.Errorf("Expected %d attributes, got %d", expectedCount, len(appErr.Attrs))
			}

			// Validate each attribute
			for _, attr := range appErr.Attrs {
				if attr.Key == "stacktrace" {
					validateStackTrace(t, attr.Value.String())

					continue
				}

				if !containsAttr(tt.want.attrs, attr) {
					t.Errorf("Unexpected attribute found: %s = %s", attr.Key, attr.Value.String())
				}
			}

			// Test error string
			if err.Error() != tt.want.errorStr {
				t.Errorf("New() Error() = %v, want %v", err.Error(), tt.want.errorStr)
			}
		})
	}
}

func TestWrap(t *testing.T) {
	type args struct {
		err   error
		code  codes.Code
		msg   string
		attrs []slog.Attr
	}

	type want struct {
		err      error
		cause    error
		code     codes.Code
		attrs    []slog.Attr
		errorStr string
	}

	tests := []struct {
		name string
		args args
		want want
	}{
		{
			name: "wraps standard error with attributes and stack trace",
			args: args{
				err:   sql.ErrNoRows,
				code:  codes.NotFound,
				msg:   "failed to create user",
				attrs: []slog.Attr{slog.String("user_id", "123"), slog.String("operation", "create_user")},
			},
			want: want{
				err:      ErrNotFound,
				cause:    sql.ErrNoRows,
				code:     codes.NotFound,
				attrs:    []slog.Attr{slog.String("user_id", "123"), slog.String("operation", "create_user")},
				errorStr: "failed to create user: sql: no rows in result set (not_found)",
			},
		},
		{
			name: "wraps standard error without additional attributes",
			args: args{
				err:   sql.ErrTxDone,
				code:  codes.FailedPrecondition,
				msg:   "invalid input",
				attrs: nil,
			},
			want: want{
				err:      ErrFailedPrecondition,
				cause:    sql.ErrTxDone,
				code:     codes.FailedPrecondition,
				attrs:    nil,
				errorStr: "invalid input: sql: transaction has already been committed or rolled back (failed_precondition)",
			},
		},
		{
			name: "flattens AppErr created by New and concatenates messages",
			args: args{
				err:   New(codes.InvalidArgument, "invalid email format", slog.String("field", "email")),
				code:  codes.Internal,
				msg:   "failed to create user",
				attrs: []slog.Attr{slog.String("user_id", "123"), slog.String("operation", "create_user")},
			},
			want: want{
				err:      ErrInternal,
				cause:    New(codes.InvalidArgument, "invalid email format", slog.String("field", "email")), // AppErr is used if cause is nil
				code:     codes.Internal,
				attrs:    []slog.Attr{slog.String("field", "email"), slog.String("user_id", "123"), slog.String("operation", "create_user")},
				errorStr: "failed to create user (internal): invalid email format (invalid_argument)",
			},
		},
		{
			name: "flattens AppErr created by New without additional attributes",
			args: args{
				err:   New(codes.NotFound, "user not found"),
				code:  codes.Internal,
				msg:   "database operation failed",
				attrs: nil,
			},
			want: want{
				err:      ErrInternal,
				cause:    New(codes.NotFound, "user not found"), // AppErr is used if cause is nil
				code:     codes.Internal,
				attrs:    nil,
				errorStr: "database operation failed (internal): user not found (not_found)",
			},
		},
		{
			name: "flattens AppErr created by Wrap and preserves original cause",
			args: args{
				err:   Wrap(sql.ErrNoRows, codes.NotFound, "invalid input"),
				code:  codes.Internal,
				msg:   "failed to process request",
				attrs: []slog.Attr{slog.String("request_id", "abc123")},
			},
			want: want{
				err:      ErrInternal,
				cause:    sql.ErrNoRows, // Original underlying error
				code:     codes.Internal,
				attrs:    []slog.Attr{slog.String("request_id", "abc123")},
				errorStr: "failed to process request (internal): invalid input: sql: no rows in result set (not_found)",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Wrap(tt.args.err, tt.args.code, tt.args.msg, tt.args.attrs...)

			// Assert that errors.Is(err, want.err) is true
			if !errors.Is(err, tt.want.err) {
				t.Errorf("errors.Is(err, want.err) = false, want true (err: %v, want.err: %v)", err, tt.want.err)
			}

			// Extract AppErr for testing
			var appErr *AppErr
			if !errors.As(err, &appErr) {
				t.Fatal("Wrap() should return an error that can be converted to *AppErr")
			}

			// Test basic fields
			if tt.want.cause == nil {
				if appErr.Cause != nil {
					t.Errorf("Wrap() Cause should be nil, got %v", appErr.Cause)
				}
			} else {
				if !errors.Is(appErr.Cause, tt.want.cause) {
					t.Errorf("Wrap() Cause = %v, want %v", appErr.Cause, tt.want.cause)
				}
			}

			if appErr.Code != tt.want.code {
				t.Errorf("Wrap() Code = %v, want %v", appErr.Code, tt.want.code)
			}

			// Test attributes
			expectedCount := len(tt.want.attrs) + 1 // +1 for stacktrace
			if len(appErr.Attrs) != expectedCount {
				t.Errorf("Expected %d attributes, got %d", expectedCount, len(appErr.Attrs))
			}

			// Validate each attribute
			for _, attr := range appErr.Attrs {
				if attr.Key == "stacktrace" {
					validateStackTrace(t, attr.Value.String())

					continue
				}

				if !containsAttr(tt.want.attrs, attr) {
					t.Errorf("Unexpected attribute found: %s = %s", attr.Key, attr.Value.String())
				}
			}

			// Test error string
			if err.Error() != tt.want.errorStr {
				t.Errorf("Wrap() Error() = %v, want %v", err.Error(), tt.want.errorStr)
			}

			// Test As method
			var target *AppErr
			if !errors.As(err, &target) {
				t.Error("Wrap() should return true for errors.As(&AppErr)")
			}
		})
	}
}

// Helper functions for testing

// validateStackTrace validates that the stack trace is properly formatted
// and contains expected package information, and that the caller is present in the first stack frame.
func validateStackTrace(t *testing.T, stackTrace string) {
	if stackTrace == "" {
		t.Error("Stack trace should not be empty")
	}

	lines := strings.Split(stackTrace, "\n")

	if len(lines) < 2 {
		t.Errorf("Stack trace should have at least 2 lines, got %d", len(lines))
	}

	// Check that the the first stack frame is the caller of either New or Wrap
	if !strings.Contains(lines[0], "TestWrap") && !strings.Contains(lines[0], "TestNew") {
		t.Errorf("Stack trace should contain a caller (TestWrap or TestNew) at the first stack frame, got: %s", lines[0])
	}
}

// containsAttr checks if an attribute exists in the want list
// by comparing both key and value.
func containsAttr(want []slog.Attr, attr slog.Attr) bool {
	for _, e := range want {
		if e.Key == attr.Key && e.Value.String() == attr.Value.String() {
			return true
		}
	}

	return false
}
