package di

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/identity"
	"github.com/liverty-music/backend/internal/infrastructure/mail"
	googlemaps "github.com/liverty-music/backend/internal/infrastructure/maps/google"
	infrazitadel "github.com/liverty-music/backend/internal/infrastructure/zitadel"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/httpx"
	"github.com/liverty-music/backend/pkg/telemetry"
	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	// receptionUnknownTokenLimit is how many calls with unknown link tokens
	// a client may make within receptionUnknownTokenWindow before
	// further scanner calls are refused with ResourceExhausted.
	receptionUnknownTokenLimit = 10
	// receptionUnknownTokenWindow is the window over which unknown link
	// tokens are counted per client.
	receptionUnknownTokenWindow = 10 * time.Minute
	// signInLinksClientLimit is how many OrganizerService.SendSignInLinks
	// calls a client may make within signInLinksClientWindow before further
	// calls are refused with ResourceExhausted.
	signInLinksClientLimit = 10
	// signInLinksClientWindow is the window over which SendSignInLinks calls
	// are counted per client.
	signInLinksClientWindow = 60 * time.Minute
	// passkeyRecoveryClientLimit is how many UserService.RequestPasskeyRecovery
	// calls a client may make within passkeyRecoveryClientWindow before
	// further calls are refused with ResourceExhausted.
	passkeyRecoveryClientLimit = 10
	// passkeyRecoveryClientWindow is the window over which
	// RequestPasskeyRecovery calls are counted per client.
	passkeyRecoveryClientWindow = 60 * time.Minute
	// verifyEmailClientLimit is how many UserService.VerifyEmail calls a client
	// may make within verifyEmailClientWindow before further calls are refused
	// with ResourceExhausted.
	verifyEmailClientLimit = 10
	// verifyEmailClientWindow is the window over which VerifyEmail calls are
	// counted per client.
	verifyEmailClientWindow = 60 * time.Minute
)

// newSignInLinkSender returns the sender of the organizer sign-in link email:
// Postmark when its server token is set, else (local development) a sender
// that fails every call. The links open the organizer console, whose origin
// is derived from the issuer like the provisioner's redirect.
func newSignInLinkSender(cfg *config.ServerConfig, logger *logging.Logger) (entity.SignInLinkSender, error) {
	if cfg.Postmark.ServerToken == "" {
		return mail.NewNoopSignInLinkSender(logger), nil
	}
	consoleBaseURL, err := infrazitadel.ConsoleBaseURLFromIssuer(cfg.JWT.Issuer)
	if err != nil {
		return nil, fmt.Errorf("derive organizer console base url: %w", err)
	}
	return mail.NewPostmarkSignInLinkSender(consoleBaseURL, cfg.Postmark.ServerToken,
		cfg.Postmark.FromAddress, cfg.Postmark.MessageStream, logger), nil
}

// provideIdentityClients builds the identity-service clients this workload
// uses: the fan identity remover on the admin and fan workloads and the email
// code verifier on the fan workload, each nil elsewhere. The organizer and
// reception workloads reach no identity-provider operation. The API builds no
// email sender: verification emails are sent by the event consumer. It logs
// which clients were built so their absence is visible.
func provideIdentityClients(ctx context.Context, cfg *config.ServerConfig, logger *logging.Logger) (entity.IdentityRemover, entity.EmailCodeVerifier) {
	var (
		remover    entity.IdentityRemover
		emailCodes entity.EmailCodeVerifier
		built      []string
	)
	if cfg.RemovesIdentities() {
		remover = identity.NewUserClient(cfg.IdentityServiceURL)
		built = append(built, "identity_remover")
	}
	if cfg.VerifiesEmails() {
		emailCodes = identity.NewUserClient(cfg.IdentityServiceURL)
		built = append(built, "email_code_verifier")
	}
	if len(built) == 0 {
		built = append(built, "none")
	}
	logger.Info(ctx, "identity-provider clients built", slog.String("clients", strings.Join(built, ",")))
	return remover, emailCodes
}

// provideOrganizationRepository returns the Organization repository. On the
// workload that provisions Organizers (the isolated admin workload) it is a
// client of the identity service. Other workloads outside local refuse every
// call, and local dev without the identity service uses a no-op (no live
// Zitadel).
func provideOrganizationRepository(cfg *config.ServerConfig, logger *logging.Logger) entity.OrganizationRepository {
	switch {
	case cfg.ProvisionsOrganizers():
		return identity.NewOrganizationClient(cfg.IdentityServiceURL)
	case cfg.IsLocal():
		return infrazitadel.NewNoopOrganizationRepository(logger)
	default:
		return infrazitadel.NewUnavailableOrganizationRepository(cfg.Workload)
	}
}

// newVenueMapCatalog builds the Google Maps Places client that serves the
// organizer venue search and reads a picked place. It authenticates with
// Application Default Credentials (Workload Identity in GKE) and bills the
// given project. Outside local, missing credentials stop the server; local dev
// without credentials gets a catalog whose every call fails with Unavailable.
func newVenueMapCatalog(ctx context.Context, projectID string, isLocal bool, logger *logging.Logger) (*googlemaps.Client, error) {
	ts, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		if !isLocal {
			return nil, fmt.Errorf("obtain google maps token source: %w", err)
		}
		logger.Warn(ctx, "no application default credentials: venue search and new venues are unavailable", slog.Any("error", err))
		ts = unavailableTokenSource{err: err}
	}
	httpClient := &http.Client{
		Transport: otelhttp.NewTransport(httpx.NewRetryTransport(nil)),
		Timeout:   10 * time.Second,
	}
	return googlemaps.NewClient(ts, projectID, httpClient, logger), nil
}

// unavailableTokenSource fails every token request, so a map catalog built on
// it answers Unavailable. Used only in local dev without credentials.
type unavailableTokenSource struct {
	err error
}

// Token implements oauth2.TokenSource.
func (s unavailableTokenSource) Token() (*oauth2.Token, error) {
	return nil, s.err
}

// InitializeApp creates a new App with all dependencies wired up manually.
func InitializeApp(ctx context.Context) (*App, error) {
	cfg, err := config.Load[config.ServerConfig]()
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

	if len(cfg.Server.AllowedOrigins) == 0 {
		logger.Warn(ctx, "⚠️  CORS not configured, browser requests will fail")
	}

	db, err := rdb.New(ctx, cfg.Database, cfg.IsLocal(), logger)
	if err != nil {
		return nil, err
	}

	telemetryCloser, err := telemetry.SetupTelemetry(ctx, cfg.Telemetry, cfg.Environment, cfg.ShutdownTimeout)
	if err != nil {
		return nil, err
	}

	b, err := newAPIWiring(ctx, cfg, db, logger)
	if err != nil {
		return nil, err
	}
	return b.assemble(telemetryCloser), nil
}
