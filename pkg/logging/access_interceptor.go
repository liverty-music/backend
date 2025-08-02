package logging

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect"
)

// NewAccessLogInterceptor creates a Connect interceptor that logs access information for all requests.
// It logs essential request information for monitoring and debugging purposes.
//
// Sample log attributes:
// - procedure: "/api.UserService/GetUser"
// - method: "POST"
// - status: "ok" or "invalid_argument"
// - duration_ms: 150 (milliseconds as integer)
// - user_agent: "connect-go/1.11.1 (go1.21.0)"
// - remote_addr: "192.168.1.100" or "10.0.0.1"
func NewAccessLogInterceptor(logger *Logger) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			start := time.Now()
			procedure := req.Spec().Procedure

			// Extract request information
			var userAgent, remoteAddr, method string

			if header := req.Header(); header != nil {
				userAgent = header.Get("User-Agent")
				remoteAddr = header.Get("X-Forwarded-For")
				if remoteAddr == "" {
					remoteAddr = header.Get("X-Real-IP")
				}
				method = header.Get("X-Http-Method")
				if method == "" {
					method = http.MethodPost // Connect uses POST by default
				}
			}

			resp, err := next(ctx, req)

			durationMs := time.Since(start).Milliseconds()

			// Determine status from error
			status := "ok"
			if err != nil {
				if connectErr, ok := err.(*connect.Error); ok {
					status = connectErr.Code().String()
				} else {
					status = "unknown"
				}
			}

			// Log essential access information
			logger.Info(ctx, "Access log",
				slog.String("procedure", procedure),
				slog.String("method", method),
				slog.String("status", status),
				slog.Int64("duration_ms", durationMs),
				slog.String("user_agent", userAgent),
				slog.String("remote_addr", remoteAddr),
			)

			return resp, err
		}
	}
}
