package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"

	"log/slog"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/pkg/apperr"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/logging"
)

// ConnectServer represents the Connect server.
type ConnectServer struct {
	server  *http.Server
	logger  *logging.Logger
	Cfg     *config.Config
	address string
}

// RPCHandlerFunc is a function that returns a path and a handler for a Connect RPC service.
type RPCHandlerFunc func(opts ...connect.HandlerOption) (string, http.Handler)

// NewConnectServer creates a new Connect server instance.
func NewConnectServer(
	cfg *config.Config,
	logger *logging.Logger,
	db *rdb.Database,
	handlerFuncs ...RPCHandlerFunc,
) *ConnectServer {
	mux := http.NewServeMux()

	// Create interceptors
	tracingInterceptor, _ := otelconnect.NewInterceptor()
	accessLogInterceptor := logging.NewAccessLogInterceptor(logger)
	errorInterceptor := apperr.NewInterceptor(logger)

	for _, handlerFunc := range handlerFuncs {
		path, handler := handlerFunc(
			newRecoverHandler(logger),
			connect.WithInterceptors(
				tracingInterceptor,
				accessLogInterceptor,
				errorInterceptor,
			),
		)
		mux.Handle(path, handler)
	}

	address := net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port))

	server := &http.Server{
		Addr:              address,
		Handler:           http.TimeoutHandler(mux, cfg.Server.HandlerTimeout, ""),
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		ReadTimeout:       cfg.Server.ReadTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
	}

	return &ConnectServer{
		server:  server,
		logger:  logger,
		Cfg:     cfg,
		address: address,
	}
}

// Start starts the Connect server.
func (s *ConnectServer) Start() error {
	s.logger.Info(context.Background(), fmt.Sprintf("Connect Server starting on %s", s.address))

	return s.server.ListenAndServe()
}

// Stop gracefully stops the Connect server.
func (s *ConnectServer) Stop() error {
	if s.server != nil {
		timeout := s.Cfg.ShutdownTimeout

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		s.logger.Info(ctx, "Shutting down Connect server gracefully...", slog.Duration("timeout", timeout))

		return s.server.Shutdown(ctx)
	}

	return nil
}

func newRecoverHandler(logger *logging.Logger) connect.HandlerOption {
	return connect.WithRecover(func(ctx context.Context, spec connect.Spec, header http.Header, p any) error {
		logger.Error(ctx, "Panic recovered in Connect handler", fmt.Errorf("panic: %v", p),
			slog.String("procedure", spec.Procedure),
		)

		return connect.NewError(connect.CodeInternal, fmt.Errorf("internal server error"))
	})
}
