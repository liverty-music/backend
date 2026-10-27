package di

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	backendorganizationv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/backend/organization/v1/organizationv1connect"
	backenduserv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/backend/user/v1/userv1connect"
	"connectrpc.com/connect/v2"
	"connectrpc.com/grpchealth/v2"

	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	infrazitadel "github.com/liverty-music/backend/internal/infrastructure/zitadel"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
)

// IdentityApp is the identity service: the only backend process that holds
// identity-provider credentials. It has no database and no messaging. Each
// calling workload has its own listener, which serves only the procedures
// that workload uses, and another listener serves the health check.
type IdentityApp struct {
	// EventConsumerServer serves the event consumer's procedures.
	EventConsumerServer *server.ConnectServer
	// AdminConsoleAPIServer serves the admin console API's procedures.
	AdminConsoleAPIServer *server.ConnectServer
	// FanAPIServer serves the fan API's procedures.
	FanAPIServer *server.ConnectServer
	// HealthServer serves the gRPC health check for the kubelet probes.
	HealthServer *server.ConnectServer
	// Health reports readiness; the entry point sets it ready once every
	// listener is bound.
	Health          *rpc.ProcessHealthCheckHandler
	Logger          *logging.Logger
	ShutdownTimeout time.Duration
}

// EventConsumerProcedures are the identity-service procedures the event
// consumer may call: verification codes and passkey registration links.
var EventConsumerProcedures = []string{
	backenduserv1connect.UserServiceSendEmailVerificationProcedure,
	backenduserv1connect.UserServiceResendEmailVerificationProcedure,
	backenduserv1connect.UserServiceIssuePasskeyRegistrationLinkProcedure,
}

// AdminConsoleAPIProcedures are the identity-service procedures the admin
// console API may call: fan identity removal and Organizers' Organizations.
var AdminConsoleAPIProcedures = []string{
	backenduserv1connect.UserServiceDeleteIdentityProcedure,
	backendorganizationv1connect.OrganizationServiceCreateProcedure,
	backendorganizationv1connect.OrganizationServiceGetProcedure,
	backendorganizationv1connect.OrganizationServiceProvisionProcedure,
	backendorganizationv1connect.OrganizationServiceDeactivateIdentitiesProcedure,
	backendorganizationv1connect.OrganizationServiceDeleteProcedure,
}

// FanAPIProcedures are the identity-service procedures the fan API may call:
// confirming an email address with the code from its verification email, and
// removing the identity of a fan who deletes their own account. The fan API
// checks that fan's fresh sign-in before it calls; the identity service does
// not repeat the check, since only the fan API reaches this listener.
var FanAPIProcedures = []string{
	backenduserv1connect.UserServiceVerifyEmailProcedure,
	backenduserv1connect.UserServiceDeleteIdentityProcedure,
}

// InitializeIdentityApp creates the identity service with its
// identity-provider clients. Building the clients makes no network request,
// so the service starts while the identity provider is down.
func InitializeIdentityApp(ctx context.Context) (*IdentityApp, error) {
	cfg, err := config.Load[config.IdentityConfig]()
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	logger, err := provideLogger(cfg.Logging)
	if err != nil {
		return nil, err
	}
	slog.SetDefault(logger.Slog())
	shutdown.Init(logger)

	telemetryCloser, err := telemetry.SetupTelemetry(ctx, cfg.Telemetry, cfg.Environment, cfg.ShutdownTimeout)
	if err != nil {
		return nil, err
	}

	emailVerifier, err := infrazitadel.NewEmailVerifier(ctx, cfg.IssuerURL, cfg.ZitadelMachineKeyForBackendAppPath, logger)
	if err != nil {
		return nil, fmt.Errorf("create zitadel email verifier: %w", err)
	}
	identityRemover, err := infrazitadel.NewIdentityRemover(ctx, cfg.IssuerURL, cfg.ZitadelMachineKeyForBackendAppPath, logger)
	if err != nil {
		return nil, fmt.Errorf("create zitadel identity remover: %w", err)
	}
	passkeyLinks, err := infrazitadel.NewPasskeyRegistrationLinkIssuer(ctx, cfg.IssuerURL, cfg.ZitadelMachineKeyForFanPasskeyRecoveryPath, logger)
	if err != nil {
		return nil, fmt.Errorf("create zitadel passkey registration link issuer: %w", err)
	}
	organizations, err := infrazitadel.NewOrganizationRepository(ctx, cfg.IssuerURL, cfg.ZitadelMachineKeyForOrganizerProvisionerPath, cfg.OrganizerConsoleProjectID, logger)
	if err != nil {
		return nil, fmt.Errorf("create organization repository: %w", err)
	}

	// Both services are mounted on every caller's listener; each listener's allowlist
	// decides what its caller reaches, so an operation outside the caller's
	// set is refused with PermissionDenied instead of looking unimplemented.
	handlers := []server.RPCHandlerFunc{
		func(s *connect.Server) {
			backenduserv1connect.RegisterUserServiceHandler(s, rpc.NewBackendUserHandler(emailVerifier, identityRemover, passkeyLinks))
		},
		func(s *connect.Server) {
			backendorganizationv1connect.RegisterOrganizationServiceHandler(s, rpc.NewBackendOrganizationHandler(organizations))
		},
	}
	listener := func(port int) server.BackendServerSettings {
		return server.BackendServerSettings{Host: cfg.Listeners.Host, Port: port, HandlerTimeout: cfg.Listeners.HandlerTimeout}
	}
	eventConsumerSrv := server.NewBackendServer(listener(cfg.Listeners.EventConsumerPort), logger, EventConsumerProcedures, handlers...)
	adminConsoleAPISrv := server.NewBackendServer(listener(cfg.Listeners.AdminConsoleAPIPort), logger, AdminConsoleAPIProcedures, handlers...)
	fanAPISrv := server.NewBackendServer(listener(cfg.Listeners.FanAPIPort), logger, FanAPIProcedures, handlers...)

	health := rpc.NewProcessHealthCheckHandler()
	healthSrv := server.NewHealthCheckServer(listener(cfg.Listeners.HealthPort), logger, func(s *connect.Server) {
		grpchealth.Register(s, health)
	})

	shutdown.AddDrainPhase(health, eventConsumerSrv, adminConsoleAPISrv, fanAPISrv, healthSrv)
	shutdown.AddObservePhase(telemetryCloser)

	logger.Info(ctx, "identity service initialized",
		slog.Int("event_consumer_port", cfg.Listeners.EventConsumerPort),
		slog.Int("admin_console_api_port", cfg.Listeners.AdminConsoleAPIPort),
		slog.Int("fan_api_port", cfg.Listeners.FanAPIPort),
		slog.Int("health_port", cfg.Listeners.HealthPort),
	)

	return &IdentityApp{
		EventConsumerServer:   eventConsumerSrv,
		AdminConsoleAPIServer: adminConsoleAPISrv,
		FanAPIServer:          fanAPISrv,
		HealthServer:          healthSrv,
		Health:                health,
		Logger:                logger,
		ShutdownTimeout:       cfg.ShutdownTimeout,
	}, nil
}
