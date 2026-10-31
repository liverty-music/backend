package di

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	adminartistconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/admin/artist/v1/artistv1connect"
	adminconcertconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/admin/concert/v1/concertv1connect"
	adminconcertcandidateconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/admin/concert_candidate/v1/concert_candidatev1connect"
	adminnotificationconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/admin/notification/v1/notificationv1connect"
	adminorderconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/admin/order/v1/orderv1connect"
	adminorganizerconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/admin/organizer/v1/organizerv1connect"
	adminuserconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/admin/user/v1/userv1connect"
	artistconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/artist/v1/artistv1connect"
	checkoutconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/checkout/v1/checkoutv1connect"
	concertconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/concert/v1/concertv1connect"
	followconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/follow/v1/followv1connect"
	identityverificationconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/identity_verification/v1/identity_verificationv1connect"
	lotteryentryconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/lottery_entry/v1/lottery_entryv1connect"
	notificationconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/notification/v1/notificationv1connect"
	officialsiteconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/official_site/v1/official_sitev1connect"
	orderconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/order/v1/orderv1connect"
	organizerartistconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/organizer/artist/v1/artistv1connect"
	payoutaccountconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/organizer/payout_account/v1/payout_accountv1connect"
	organizerscannerconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/organizer/scanner/v1/scannerv1connect"
	organizerseattypeconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/organizer/seat_type/v1/seat_typev1connect"
	organizerseriesconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/organizer/series/v1/seriesv1connect"
	organizerseriesimageconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/organizer/series_image/v1/series_imagev1connect"
	organizerticketsaleconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/organizer/ticket_sale/v1/ticket_salev1connect"
	organizerticketstockconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/organizer/ticket_stock/v1/ticket_stockv1connect"
	organizerconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/organizer/v1/organizerv1connect"
	organizervenueconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/organizer/venue/v1/venuev1connect"
	passkeyconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/passkey/v1/passkeyv1connect"
	pushsubscriptionconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/push_subscription/v1/push_subscriptionv1connect"
	resalelistingconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/resale_listing/v1/resale_listingv1connect"
	scannerconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/scanner/v1/scannerv1connect"
	ticketconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/ticket/v1/ticketv1connect"
	ticketjourneyconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/ticket_journey/v1/ticket_journeyv1connect"
	ticketsalev1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/ticket_sale/v1/ticket_salev1connect"
	userconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/user/v1/userv1connect"
	walletpublickeyconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/wallet_public_key/v1/wallet_public_keyv1connect"
	"connectrpc.com/connect/v2"
	"connectrpc.com/grpchealth/v2"
	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/liverty-music/backend/internal/adapter/linkpreview"
	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/adapter/webhook"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	gcsstorage "github.com/liverty-music/backend/internal/infrastructure/gcp/storage"
	"github.com/liverty-music/backend/internal/infrastructure/geo"
	"github.com/liverty-music/backend/internal/infrastructure/identity"
	"github.com/liverty-music/backend/internal/infrastructure/mail"
	googlemaps "github.com/liverty-music/backend/internal/infrastructure/maps/google"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/infrastructure/music/lastfm"
	"github.com/liverty-music/backend/internal/infrastructure/music/musicbrainz"
	infrapayment "github.com/liverty-music/backend/internal/infrastructure/payment"
	infrapocketsign "github.com/liverty-music/backend/internal/infrastructure/pocketsign"
	"github.com/liverty-music/backend/internal/infrastructure/repository"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/liverty-music/backend/internal/infrastructure/server/ratelimit"
	infratelemetry "github.com/liverty-music/backend/internal/infrastructure/telemetry"
	infrawebpush "github.com/liverty-music/backend/internal/infrastructure/webpush"
	infrazitadel "github.com/liverty-music/backend/internal/infrastructure/zitadel"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/cache"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/httpx"
	"github.com/liverty-music/backend/pkg/shutdown"
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

	// Repositories
	userRepo := rdb.NewUserRepository(db)
	artistRepo := rdb.NewArtistRepository(db, newOutbox(logger))
	followRepo := rdb.NewFollowRepository(db, newOutbox(logger))
	concertRepo := rdb.NewConcertRepository(db, newOutbox(logger))
	venueRepo := rdb.NewVenueRepository(db)
	seriesRepo := rdb.NewSeriesRepository(db, newOutbox(logger))
	concertSearchRepo := rdb.NewConcertSearchRepository(db)
	concertCandidateRepo := rdb.NewConcertCandidateRepository(db)
	rejectedConcertRepo := rdb.NewRejectedConcertRepository(db)
	pushSubRepo := rdb.NewPushSubscriptionRepository(db, newOutbox(logger))
	ticketJourneyRepo := rdb.NewTicketJourneyRepository(db, newOutbox(logger))
	organizerRepo := rdb.NewOrganizerRepository(db, newOutbox(logger))
	identityVerificationRepo := rdb.NewIdentityVerificationRepository(db)
	lotteryEntryRepo := rdb.NewLotteryEntryRepository(db, newOutbox(logger))
	eventPublishState := rdb.NewEventPublishStateRepository(db)
	// The Order, Settlement and PayoutAccount repositories combine their rows
	// with their payment provider operations.
	payments := newPaymentProviders(cfg.Stripe.SecretKey, logger)
	orderRepo := repository.NewOrderRepository(rdb.NewOrderRepository(db), payments.orders)
	ticketRepo := rdb.NewTicketRepository(db, newOutbox(logger))
	issuanceRepo := rdb.NewIssuanceRepository(db, newOutbox(logger))
	settlementRepo := repository.NewSettlementRepository(rdb.NewSettlementRepository(db), payments.settlements)
	refundRepo := rdb.NewRefundRepository(db)
	payoutAccountRepo := repository.NewPayoutAccountRepository(rdb.NewPayoutAccountRepository(db), payments.payoutAccounts)
	eventStartTimeRepo := rdb.NewEventStartTimeRepository(db)
	eventOrganizerRepo := rdb.NewEventOrganizerRepository(db)
	ticketSaleRepo := rdb.NewTicketSaleRepository(db, newOutbox(logger))
	seatTypeRepo := rdb.NewSeatTypeRepository(db)
	ticketStockRepo := rdb.NewTicketStockRepository(db)
	checkoutRepo := rdb.NewCheckoutRepository(db, newOutbox(logger))
	eventRepo := rdb.NewEventRepository(db)
	resaleListingRepo := rdb.NewResaleListingRepository(db)
	walletPublicKeyRepo := rdb.NewWalletPublicKeyRepository(db)
	scannerRepo := rdb.NewScannerRepository(db)
	admissionRepo := rdb.NewAdmissionRepository(db)
	rejectedScanRepo := rdb.NewRejectedScanRepository(db)
	processedWebhookEventRepo := rdb.NewProcessedWebhookEventRepository(db)

	// Infrastructure - Gemini (optional)
	var geminiSearcher entity.ConcertSearcher
	if cfg.GCP.GeminiSearchAPIKey != "" {
		geminiHTTPClient := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
		searcher, err := gemini.NewConcertSearcher(ctx, gemini.Config{
			APIKey:          cfg.GCP.GeminiSearchAPIKey,
			Model:           cfg.GCP.ConcertSearchModel(),
			ThinkingLevel:   cfg.GCP.ConcertSearchThinkingLevel(),
			OmitTemperature: true,

			IncludeServerSideToolInvocations: true,
		}, geminiHTTPClient, gemini.NewOfficialPageClient(), logger)
		if err != nil {
			return nil, err
		}
		geminiSearcher = searcher
	}

	// Infrastructure - Music
	musicHTTPClient := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	lastfmClient := lastfm.NewClient(cfg.LastFMAPIKey, musicHTTPClient, logger)
	musicbrainzClient := musicbrainz.NewClient(musicHTTPClient, logger)

	// Cache - Artist discovery results (search, similar, top) for usecase.ArtistCacheTTL (6 h)
	artistCache := cache.NewMemoryCache(usecase.ArtistCacheTTL)

	// Initialize the shutdown package for phased resource teardown.
	shutdown.Init(logger)

	// Infrastructure - Messaging Publisher
	wmLogger := watermill.NewSlogLogger(logger.Slog())
	var goChannel *gochannel.GoChannel
	if cfg.NATS.URL == "" {
		goChannel = gochannel.NewGoChannel(gochannel.Config{
			OutputChannelBuffer: 256,
		}, wmLogger)
	}
	publisher, err := messaging.NewPublisher(cfg.NATS, wmLogger, goChannel, logger)
	if err != nil {
		return nil, fmt.Errorf("create messaging publisher: %w", err)
	}

	// Infrastructure - fan identity removal (admin and fan workloads) and
	// email verification (fan workload) through the identity service; nil
	// elsewhere and in local dev without the identity service.
	identityRemover, emailCodes := provideIdentityClients(ctx, cfg, logger)

	// Business metrics
	businessMetrics := infratelemetry.NewBusinessMetrics()

	// Use Cases
	eventPublisher := messaging.NewEventPublisher(publisher)

	userUC := usecase.NewUserUseCase(userRepo, usecase.DeletionBlockerReads{
		Tickets:        ticketRepo,
		Events:         eventRepo,
		ResaleListings: resaleListingRepo,
		LotteryEntries: lotteryEntryRepo,
		Checkouts:      checkoutRepo,
		Orders:         orderRepo,
		Now:            time.Now,
	}, eventPublisher, nil, emailCodes, identityRemover, usecase.PasskeyMailers{}, logger)
	centroidResolver := geo.NewCentroidResolver()
	concertUC := usecase.NewConcertUseCase(artistRepo, concertRepo, venueRepo, seriesRepo, organizerRepo, concertSearchRepo, concertCandidateRepo, rejectedConcertRepo, geminiSearcher, centroidResolver, eventPublisher, businessMetrics, nil, cfg.GCP.SearchCacheTTL(), cfg.GCP.SearchDiscoveryWindow(), logger)
	artistUC := usecase.NewArtistUseCase(artistRepo, organizerRepo, lastfmClient, musicbrainzClient, eventPublisher, artistCache, logger)
	organizationRepo := provideOrganizationRepository(cfg, logger)
	// GCS image storer for organizer series images. Local dev has no GCP credentials,
	// so it runs without one and signed-URL issuance returns Internal; outside
	// local a storer that cannot be created stops the server.
	var imageStorer usecase.ImageStorer
	if !cfg.IsLocal() {
		storer, err := gcsstorage.NewGCSStorer(ctx, logger)
		if err != nil {
			return nil, fmt.Errorf("create GCS image storer: %w", err)
		}
		imageStorer = storer
		shutdown.AddExternalPhase(storer)
	}
	signInLinkSender, err := newSignInLinkSender(cfg, logger)
	if err != nil {
		return nil, err
	}
	organizerUC := usecase.NewOrganizerUseCase(organizerRepo, signInLinkSender, artistRepo, organizationRepo, rdb.NewSeriesImageRepository(db, newOutbox(logger)), imageStorer,
		usecase.StorageBuckets{Internal: cfg.StorageInternalBucket, Served: cfg.StorageBucket},
		eventPublisher, businessMetrics, logger)
	// Run the provisioning reconciler only on the workload that provisions
	// organizers (the isolated admin workload); it completes any organizer left
	// in the provisioning state after a partial failure.
	if cfg.ProvisionsOrganizers() {
		startOrganizerReconciler(ctx, organizerUC, logger)
	}
	// Map catalog (Google Maps Places API) that members pick event
	// venues from.
	mapCatalog, err := newVenueMapCatalog(ctx, cfg.GCP.ProjectID, cfg.IsLocal(), logger)
	if err != nil {
		return nil, err
	}
	seriesUC := usecase.NewSeriesUseCase(seriesRepo, rdb.NewSeriesImageRepository(db, newOutbox(logger)), venueRepo, mapCatalog, organizerRepo, ticketSaleRepo,
		imageStorer, usecase.StorageBuckets{Internal: cfg.StorageInternalBucket, Served: cfg.StorageBucket}, eventPublisher, logger, time.Now)

	// Identity verification — select the real Pocket Sign Stamp client when all four
	// POCKET_SIGN_* fields (base URL, token, tenant id, callback URL) are
	// configured; fall back to StubVerifier (returns UNAVAILABLE) when
	// unconfigured so local dev and pre-onboarding environments stay safe and inert.
	var pocketSignVerifier usecase.IdentityVerifier
	psConfig := infrapocketsign.StampClientConfig{
		BaseURL:     cfg.PocketSign.BaseURL,
		Token:       cfg.PocketSign.Token,
		TenantID:    cfg.PocketSign.TenantID,
		CallbackURL: cfg.PocketSign.CallbackURL,
	}
	if psConfig.IsConfigured() {
		// Bound every vendor call so a hung Pocket Sign API cannot block a
		// handler goroutine indefinitely when the request context lacks a
		// deadline.
		psHTTPClient := &http.Client{
			Timeout:   30 * time.Second,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		}
		stampClient := infrapocketsign.NewStampClient(psConfig, psHTTPClient, logger)
		pocketSignVerifier = stampClient
		// Register the client's nonce-cache cleanup goroutine for graceful
		// shutdown, like the other MemoryCache instances (cf. artistCache).
		shutdown.AddDrainPhase(stampClient)
		logger.Info(ctx, "pocket sign verify client initialized",
			slog.String("base_url", cfg.PocketSign.BaseURL),
		)
	} else {
		pocketSignVerifier = infrapocketsign.NewStubVerifier()
		logger.Info(ctx, "pocket sign verify client not configured; using stub (identity verification unavailable)")
	}
	identityVerificationUC := usecase.NewIdentityVerificationUseCase(identityVerificationRepo, userRepo, pocketSignVerifier, logger)
	// SeriesImageUseCase.Process is never called from the RPC server (only the dedicated
	// series-image-consumer job in series_image_consumer.go invokes it), so no SeriesImageProcessor is
	// wired here.
	seriesImageUC := usecase.NewSeriesImageUseCase(seriesRepo, rdb.NewSeriesImageRepository(db, newOutbox(logger)), imageStorer, nil, eventPublisher, logger)
	// SeriesImageURLBuilder composes organizer series-image CDN URLs for the
	// organizer-facing ConcertService pb. The CDN base is sourced from
	// config (CDN_BASE_URL) rather than read directly by entity.
	seriesImageURLBuilder := pb.NewSeriesImageURLBuilder(cfg.CDNBaseURL)

	// Card authorizations: use the real Stripe adapters when a
	// secret key is configured, otherwise no-ops that return Unavailable so local
	// development starts cleanly without a Stripe account (outside local the key
	// is required, see config.ServerConfig.Validate). The secret key value is
	// provisioned externally via GCP Secret Manager / ESO and injected as
	// STRIPE_SECRET_KEY; see the cloud-provisioning repo for the ESO resource.
	// A lottery entry's and a checkout's card authorizations differ only in
	// their idempotency keys (CardAuthorizationPolicy).
	paymentConfigured := cfg.Stripe.SecretKey != ""
	var (
		lotteryCardAuthorizations  entity.CardAuthorizationRepository
		checkoutCardAuthorizations entity.CardAuthorizationRepository
	)
	if paymentConfigured {
		lotteryCardAuthorizations = infrapayment.NewStripeCardAuthorizationRepository(cfg.Stripe.SecretKey, infrapayment.LotteryCardAuthorizationPolicy, logger)
		checkoutCardAuthorizations = infrapayment.NewStripeCardAuthorizationRepository(cfg.Stripe.SecretKey, infrapayment.CheckoutCardAuthorizationPolicy, logger)
	} else {
		noop := infrapayment.NewNoopCardAuthorizationRepository(logger)
		lotteryCardAuthorizations, checkoutCardAuthorizations = noop, noop
	}
	lotteryEntryUC := usecase.NewLotteryEntryUseCase(usecase.LotteryEntryDeps{
		SaleRepo:                 ticketSaleRepo,
		StockRepo:                ticketStockRepo,
		EntryRepo:                lotteryEntryRepo,
		OrderRepo:                orderRepo,
		EventRepo:                eventRepo,
		IdentityVerificationRepo: identityVerificationRepo,
		UserRepo:                 userRepo,
		CardAuthorizations:       lotteryCardAuthorizations,
		Publisher:                eventPublisher,
		Clock:                    time.Now,
		Logger:                   logger,
	})

	// Seat types and ticket stocks, the sales (Lottery and FirstCome) that
	// sell the stocks of their events, and the fan's 15-minute first-come
	// checkout.
	seatTypeUC := usecase.NewSeatTypeUseCase(seatTypeRepo, seriesRepo)
	ticketStockUC := usecase.NewTicketStockUseCase(ticketStockRepo, seatTypeRepo, eventRepo, seriesRepo, eventOrganizerRepo, time.Now)
	ticketSaleUC := usecase.NewTicketSaleUseCase(ticketSaleRepo, ticketStockRepo, lotteryEntryRepo, orderRepo, seriesRepo, organizerRepo, eventRepo, eventPublishState, eventStartTimeRepo, time.Now)
	checkoutUC := usecase.NewCheckoutUseCase(usecase.CheckoutDeps{
		CheckoutRepo:             checkoutRepo,
		SaleRepo:                 ticketSaleRepo,
		StockRepo:                ticketStockRepo,
		EntryRepo:                lotteryEntryRepo,
		OrderRepo:                orderRepo,
		UserRepo:                 userRepo,
		IdentityVerificationRepo: identityVerificationRepo,
		EventState:               eventPublishState,
		EventRepo:                eventRepo,
		CardAuthorizations:       checkoutCardAuthorizations,
		Publisher:                eventPublisher,
		Clock:                    time.Now,
		Logger:                   logger,
	})
	ticketUC := usecase.NewTicketUseCase(orderRepo, ticketRepo, scannerRepo, eventRepo, walletPublicKeyRepo, admissionRepo, rejectedScanRepo, eventPublisher, logger)

	// ⑥ ticket wallet and venue admission (Scanners).
	walletPublicKeyUC := usecase.NewWalletPublicKeyUseCase(walletPublicKeyRepo, logger)
	scannerUC := usecase.NewScannerUseCase(scannerRepo, eventRepo, eventOrganizerRepo, eventPublishState, logger)

	// Orders: the fan's and the admin's reads, the issuance pipeline that turns
	// Won-captured lottery entries and committed first-come checkouts into
	// Orders + tickets, and the refund policy (refundRepo provides the single
	// atomic DB commit; the idempotent Stripe calls run first).
	orderUC := usecase.NewOrderUseCase(usecase.OrderDeps{
		IssuanceRepo:             issuanceRepo,
		OrderRepo:                orderRepo,
		EntryRepo:                lotteryEntryRepo,
		EventOrganizerRepo:       eventOrganizerRepo,
		OrganizerRepo:            organizerRepo,
		IdentityVerificationRepo: identityVerificationRepo,
		CheckoutRepo:             checkoutRepo,
		TicketSaleRepo:           ticketSaleRepo,
		TicketRepo:               ticketRepo,
		EventState:               eventPublishState,
		EventRepo:                eventRepo,
		CardAuthorizations:       checkoutCardAuthorizations,
		Publisher:                eventPublisher,
		Clock:                    time.Now,
		RefundRepo:               refundRepo,
		SettlementRepo:           settlementRepo,
		Logger:                   logger,
	})

	// Official resale: a fan lists a ticket of a sold-out ticket stock; the
	// purchase of listings comes with resell-tickets-by-lottery. The refund
	// job pays the listing fan back within a minute and announces it; the
	// event consumer sends the sold email.
	resaleListingUC := usecase.NewResaleListingUseCase(usecase.ResaleListingDeps{
		Listings:   resaleListingRepo,
		Tickets:    ticketRepo,
		Orders:     orderRepo,
		Stocks:     ticketStockRepo,
		EventState: eventPublishState,
		Events:     eventRepo,
		Publisher:  eventPublisher,
		Clock:      time.Now,
		Logger:     logger,
	})

	// Settlement task 4.3: Stripe webhook ingest — idempotent dispatch.
	// settlementRepo removed from constructor: it was previously injected but
	// never used (the dead orderID field made the dispute path unreachable).
	// Resolution is now via orderRepo.GetByProviderPaymentID.
	stripeWebhookSvc := usecase.NewStripeWebhookService(
		processedWebhookEventRepo,
		orderRepo,
		orderUC,
		orderUC,
		logger,
	)

	// Settlement / payout pipeline (ticket-settlement-and-payout).
	disputeBuffer := time.Duration(cfg.Stripe.SettlementDisputeBufferDays) * 24 * time.Hour
	payoutSweeperUC := usecase.NewSettlementUseCase(
		settlementRepo,
		orderRepo,
		payoutAccountRepo,
		eventStartTimeRepo,
		disputeBuffer,
		time.Now,
		logger,
	)
	payoutAccountUC := usecase.NewPayoutAccountUseCase(
		payoutAccountRepo,
		organizerRepo,
		cfg.Stripe.OnboardingReturnURL,
		logger,
	)

	// Start the periodic draw sweeper only on the workload that runs the payment
	// sweepers: the fan workload outside local, or local with a Stripe key (see
	// config.ServerConfig.RunsPaymentSweepers). The draw captures winners /
	// releases losers, so it MUST run where Stripe is reachable; a Stripe-less
	// pod winning the race would demote EVERY winner to a loser while leaving
	// their card authorizations unreleased. The gate used to be the presence of the key, which
	// kept it off the consoles only because they lacked one; it is now explicit,
	// so the consoles can carry the key for refunds and payout onboarding. The
	// draw remains idempotent across fan-api's own pods.
	if cfg.RunsPaymentSweepers() {
		startLotteryDrawSweeper(ctx, lotteryEntryUC, logger)
		// The issuance sweeper reads ④'s captured payments, so it too requires a
		// real payment provider and runs on the same (fan-api) workload. Both are
		// idempotent across pods.
		startIssuanceSweeper(ctx, orderUC, logger)
		// The settlement sweeper releases held funds after the event + dispute
		// buffer. It also requires a real Stripe key (Transfer creation). It runs
		// on the same (fan-api) workload and is idempotent across pods.
		startSettlementSweeper(ctx, payoutSweeperUC, logger)
		// The checkout sweepers release lapsed holds and card authorizations,
		// finish stalled commits and refund resold listings; all reach Stripe,
		// so they run beside the others.
		startCheckoutSweepers(ctx, checkoutUC, orderUC, resaleListingUC, logger)
	} else {
		logger.Info(ctx, "lottery draw + issuance + settlement sweepers disabled on this workload",
			slog.String("workload", cfg.Workload))
	}

	followUC := usecase.NewFollowUseCase(followRepo, artistRepo, musicbrainzClient, businessMetrics, logger)
	ticketJourneyUC := usecase.NewTicketJourneyUseCase(ticketJourneyRepo, eventPublisher, logger)
	webpushSender := infrawebpush.NewSender(cfg.VAPID.PublicKey, cfg.VAPID.PrivateKey, cfg.VAPID.Contact)
	notificationRepo := rdb.NewNotificationRepository(db, newOutbox(logger))
	// The API never sends order confirmations (the consumer does), so the
	// order-confirmation dependencies stay empty here.
	notificationUC := usecase.NewNotificationUseCase(notificationRepo, pushSubRepo, webpushSender, eventPublisher, businessMetrics, logger, usecase.OrderConfirmationDeps{},
		usecase.NewConcertsDeps{ArtistRepo: artistRepo, ConcertRepo: concertRepo, FollowRepo: followRepo})
	pushSubscriptionUC := usecase.NewPushSubscriptionUseCase(pushSubRepo, logger)
	// Auth - JWT Validator and Interceptor
	jwtValidator, err := auth.NewJWTValidator(
		cfg.JWT.Issuer,
		cfg.JWT.Issuer+"/oauth/v2/keys",
		cfg.JWT.JWKSRefreshInterval,
	)
	if err != nil {
		return nil, err
	}

	// Apply additional accepted issuers for multi-provider support (Option C migration).
	if len(cfg.JWT.AcceptedIssuers) > 0 {
		all := append([]string{cfg.JWT.Issuer}, cfg.JWT.AcceptedIssuers...)
		jwtValidator = jwtValidator.WithAcceptedIssuers(all)
	}

	publicProcedures := auth.FanPublicProcedures()

	authFunc := auth.NewAuthFunc(jwtValidator, publicProcedures)

	// The admin server gets its own AuthFunc with NO public procedures, ever.
	// It must NOT reuse the consumer authFunc above: the admin server also
	// mounts the consumer ArtistService handler (see adminHandlers below), so
	// procedures like "/ArtistService/ListTop" resolve to the same name on
	// both servers. Sharing authFunc would let publicProcedures' fan-only
	// entries for ListTop/ListSimilar/Search leak onto the admin host and let
	// an unauthenticated caller reach RequireRoleInterceptor instead of being
	// rejected with Unauthenticated by the authn middleware first (backend#481).
	// Both AuthFuncs share the same jwtValidator, so token validation itself
	// is identical; only the public-procedure allowlist differs.
	adminAuthFunc := auth.NewAuthFunc(jwtValidator, nil)

	// The organizer server gets its own AuthFunc too. Its only public
	// procedure is OrganizerService.SendSignInLinks; every other organizer
	// call is a signed-in member's.
	organizerAuthFunc := auth.NewAuthFunc(jwtValidator, auth.OrganizerPublicProcedures())

	// The reception server's AuthFunc makes only the scanner device's procedures
	// public (no sign-in; link token + device signature). Nothing else is
	// mounted there.
	receptionAuthFunc := auth.NewAuthFunc(jwtValidator, auth.ReceptionPublicProcedures())

	// Health check handler (public, outside authn middleware).
	// Keep a reference so App.Shutdown can call SetShuttingDown.
	healthChecker := rpc.NewHealthCheckHandler(db.Pool, logger)
	healthHandler := func(s *connect.Server) {
		grpchealth.Register(s, healthChecker)
	}

	// Admin RPC handlers — served ONLY by the dedicated admin server, whose
	// server-wide RequireRoleInterceptor gates every procedure on the "admin"
	// role. The consumer server below does NOT register these, so the admin
	// surface cannot be reached via the consumer host.
	adminHandlers := []server.RPCHandlerFunc{
		func(s *connect.Server) {
			adminconcertconnect.RegisterConcertServiceHandler(s, rpc.NewAdminConcertHandler(concertUC, seriesImageURLBuilder, logger))
		},
		func(s *connect.Server) {
			adminorganizerconnect.RegisterOrganizerServiceHandler(s, rpc.NewAdminOrganizerHandler(organizerUC, logger))
		},
		// Mount the consumer ArtistService on the admin server so the admin
		// console can reuse ArtistService.Search (to pick an artist to
		// associate) via the admin host + admin token, instead of a
		// cross-origin call to the consumer API. Per design D2, the
		// higher-privilege admin server may additionally mount consumer
		// handlers as needed; the server-wide RequireRoleInterceptor(admin)
		// gates every method here.
		func(s *connect.Server) {
			artistconnect.RegisterArtistServiceHandler(s, rpc.NewArtistHandler(artistUC, logger))
		},
		// Admin UserService: Delete (admin-delete-test-data). Removes a fan
		// User and its Zitadel identity; never mounted on the consumer server.
		func(s *connect.Server) {
			adminuserconnect.RegisterUserServiceHandler(s, rpc.NewAdminUserHandler(userUC, logger))
		},
		// Admin OrderService: Refund (⑤ task 4.1).
		func(s *connect.Server) {
			adminorderconnect.RegisterOrderServiceHandler(s, rpc.NewAdminOrderHandler(orderUC, logger))
		},
		// Admin ConcertCandidateService: review of concert candidates.
		func(s *connect.Server) {
			adminconcertcandidateconnect.RegisterConcertCandidateServiceHandler(s, rpc.NewAdminConcertCandidateHandler(usecase.NewConcertCandidateUseCase(concertUC), logger))
		},
		// Admin ArtistService: ListByOrganizer.
		func(s *connect.Server) {
			adminartistconnect.RegisterArtistServiceHandler(s, rpc.NewAdminArtistHandler(artistUC, logger))
		},
		// Admin NotificationService: NotifyNewConcerts.
		func(s *connect.Server) {
			adminnotificationconnect.RegisterNotificationServiceHandler(s, rpc.NewAdminNotificationHandler(notificationUC, cfg.BaseConfig, logger))
		},
	}

	// Consumer RPC handlers (protected by authn middleware)
	handlers := []server.RPCHandlerFunc{
		func(s *connect.Server) {
			userconnect.RegisterUserServiceHandler(s, rpc.NewUserHandler(userUC, jwtValidator.NewFreshSignInVerifier(cfg.FanClientID, time.Now), logger))
		},
		func(s *connect.Server) {
			artistconnect.RegisterArtistServiceHandler(s, rpc.NewArtistHandler(artistUC, logger))
		},
		func(s *connect.Server) {
			followconnect.RegisterFollowServiceHandler(s, rpc.NewFollowHandler(followUC, userRepo, logger))
		},
		func(s *connect.Server) {
			pushsubscriptionconnect.RegisterPushSubscriptionServiceHandler(s, rpc.NewPushSubscriptionHandler(pushSubscriptionUC, userRepo, logger))
		},
		func(s *connect.Server) {
			notificationconnect.RegisterNotificationServiceHandler(s, rpc.NewNotificationHandler(notificationUC, userRepo, logger))
		},
		func(s *connect.Server) {
			ticketjourneyconnect.RegisterTicketJourneyServiceHandler(s, rpc.NewTicketJourneyHandler(ticketJourneyUC, userRepo, logger))
		},
		func(s *connect.Server) {
			ticketconnect.RegisterTicketServiceHandler(s, rpc.NewTicketHandler(ticketUC, userUC, logger))
		},
		func(s *connect.Server) {
			orderconnect.RegisterOrderServiceHandler(s, rpc.NewOrderHandler(orderUC, userUC, logger))
		},
		func(s *connect.Server) {
			officialsiteconnect.RegisterOfficialSiteServiceHandler(s, rpc.NewOfficialSiteHandler(usecase.NewOfficialSiteUseCase(artistRepo), logger))
		},
		// Fan-facing WalletPublicKeyService: Register, Get (signed-in fan only).
		func(s *connect.Server) {
			walletpublickeyconnect.RegisterWalletPublicKeyServiceHandler(s, rpc.NewWalletPublicKeyHandler(walletPublicKeyUC, userRepo, logger))
		},
		func(s *connect.Server) {
			identityverificationconnect.RegisterIdentityVerificationServiceHandler(s, rpc.NewIdentityVerificationHandler(identityVerificationUC, userUC, logger))
		},
		// Fan-facing PasskeyService: List (the signed-in fan's recorded passkeys).
		func(s *connect.Server) {
			passkeyconnect.RegisterPasskeyServiceHandler(s, rpc.NewPasskeyHandler(
				usecase.NewPasskeyUseCase(rdb.NewPasskeyRepository(db), userRepo, time.Now, logger), userUC, logger))
		},
	}

	// Fan-facing ticket sales: TicketSaleService (public reads), the signed-in
	// fan's first-come CheckoutService, LotteryEntryService and official
	// ResaleListingService.
	handlers = append(handlers,
		func(s *connect.Server) {
			ticketsalev1connect.RegisterTicketSaleServiceHandler(s, rpc.NewTicketSaleHandler(ticketSaleUC, logger))
		},
		func(s *connect.Server) {
			checkoutconnect.RegisterCheckoutServiceHandler(s, rpc.NewCheckoutHandler(checkoutUC, orderUC, userUC, logger))
		},
		func(s *connect.Server) {
			lotteryentryconnect.RegisterLotteryEntryServiceHandler(s, rpc.NewLotteryEntryHandler(lotteryEntryUC, userUC, logger))
		},
		func(s *connect.Server) {
			resalelistingconnect.RegisterResaleListingServiceHandler(s, rpc.NewResaleListingHandler(resaleListingUC, userUC, logger))
		},
	)

	handlers = append(handlers, func(s *connect.Server) {
		concertconnect.RegisterConcertServiceHandler(s, rpc.NewConcertHandler(concertUC, userRepo, seriesImageURLBuilder, logger))
	})

	rateLimiter := ratelimit.NewLimiter(ratelimit.Config{
		AuthRPS:   cfg.Server.RateLimit.AuthRPS,
		AuthBurst: cfg.Server.RateLimit.AuthBurst,
		AnonRPS:   cfg.Server.RateLimit.AnonRPS,
		AnonBurst: cfg.Server.RateLimit.AnonBurst,
	}, time.Minute)

	// Consumer Connect server — all consumer services, no admin service; its
	// only extra interceptor is the per-client limit below.
	// Link preview tags for the fan web's /events/{id} HTML (fetched by Caddy
	// templates). Public and outside the RPC rate limit, since every crawler
	// request reaches fan-api from Caddy's Pod IP.
	linkPreviewHandler := linkpreview.NewHandler(concertUC, linkpreview.NewTagBuilder(cfg.FanWebBaseURL, seriesImageURLBuilder), logger)
	publicRoutes := []server.PublicHTTPRoute{
		{Pattern: linkpreview.Pattern, Handler: linkPreviewHandler},
	}

	// The fan server's public writes are limited per client, each on its own
	// count: UserService.RequestPasskeyRecovery passkeyRecoveryClientLimit
	// times per passkeyRecoveryClientWindow, and UserService.VerifyEmail
	// verifyEmailClientLimit times per verifyEmailClientWindow.
	fanInterceptors := []connect.ServerInterceptor{
		ratelimit.NewClientQuotaInterceptor(
			ratelimit.NewClientThrottle(passkeyRecoveryClientLimit, passkeyRecoveryClientWindow, time.Now),
			auth.FanClientLimitedProcedures(),
		),
		ratelimit.NewClientQuotaInterceptor(
			ratelimit.NewClientThrottle(verifyEmailClientLimit, verifyEmailClientWindow, time.Now),
			auth.FanEmailVerificationProcedures(),
		),
	}
	srv := server.NewConnectServer(cfg.Server, logger, authFunc, rateLimiter, healthHandler, fanInterceptors, publicRoutes, handlers...)

	// Admin Connect server — a second listener in the same binary on its own
	// port and CORS allowlist, serving ONLY admin services. Its server-wide
	// RequireRoleInterceptor (admin role) is the sole, structural authorization
	// gate (handlers carry no per-method role check). It uses its own
	// adminAuthFunc (no public procedures — see above) rather than the
	// consumer server's authFunc, and shares the rate limiter and health
	// checker with the consumer server.
	adminServerCfg := cfg.Server
	adminServerCfg.Port = cfg.Server.AdminPort
	adminServerCfg.AllowedOrigins = cfg.Server.AdminAllowedOrigins
	adminInterceptors := []connect.ServerInterceptor{auth.NewRequireRoleInterceptor("admin")}
	adminSrv := server.NewConnectServer(adminServerCfg, logger, adminAuthFunc, rateLimiter, healthHandler, adminInterceptors, nil, adminHandlers...)

	// Organizer Connect server — a third listener in the same binary on its
	// own port and CORS allowlist, serving ONLY the organizer-facing
	// OrganizerService. Its server-wide OrgScopedInterceptor enforces token
	// audience, login-scope org derivation, and role cross-check before any
	// handler runs, except for its one public procedure, SendSignInLinks,
	// which a client may call signInLinksClientLimit times per
	// signInLinksClientWindow. No fan, admin or reception services are
	// registered here.
	organizerServerCfg := cfg.Server
	organizerServerCfg.Port = cfg.Server.OrganizerPort
	organizerServerCfg.AllowedOrigins = cfg.Server.OrganizerAllowedOrigins
	organizerInterceptors := []connect.ServerInterceptor{
		ratelimit.NewClientQuotaInterceptor(
			ratelimit.NewClientThrottle(signInLinksClientLimit, signInLinksClientWindow, time.Now),
			auth.OrganizerPublicProcedures(),
		),
		auth.NewOrgScopedInterceptor(cfg.OrganizerConsoleProjectID, auth.OrganizerPublicProcedures()),
	}
	organizerHandlers := []server.RPCHandlerFunc{
		func(s *connect.Server) {
			organizerconnect.RegisterOrganizerServiceHandler(s, rpc.NewOrganizerHandler(organizerUC, logger))
		},
		func(s *connect.Server) {
			organizerseriesconnect.RegisterSeriesServiceHandler(s, rpc.NewOrganizerSeriesHandler(seriesUC, organizerUC, seriesImageURLBuilder, logger))
		},
		// Organizer-facing VenueService: Search (map catalog venue lookup).
		func(s *connect.Server) {
			organizervenueconnect.RegisterVenueServiceHandler(s, rpc.NewOrganizerVenueHandler(usecase.NewVenueUseCase(mapCatalog), organizerUC))
		},
		// Organizer-facing SeriesImageService: CreateUploadUrl.
		func(s *connect.Server) {
			organizerseriesimageconnect.RegisterSeriesImageServiceHandler(s, rpc.NewOrganizerSeriesImageHandler(seriesImageUC, organizerUC))
		},
		// Organizer-facing ArtistService: List (the Organizer's own Artists).
		func(s *connect.Server) {
			organizerartistconnect.RegisterArtistServiceHandler(s, rpc.NewOrganizerArtistHandler(artistUC, organizerUC, logger))
		},
		// Organizer-facing PayoutAccountService: Get.
		func(s *connect.Server) {
			payoutaccountconnect.RegisterPayoutAccountServiceHandler(s, rpc.NewPayoutAccountHandler(payoutAccountUC, organizerUC, logger))
		},
		// Organizer-facing ScannerService: Create, ListByEvent, Revoke.
		func(s *connect.Server) {
			organizerscannerconnect.RegisterScannerServiceHandler(s, rpc.NewOrganizerScannerHandler(scannerUC, organizerUC, logger))
		},
		// Organizer-facing TicketSaleService: the Event's Lottery and FirstCome sales.
		func(s *connect.Server) {
			organizerticketsaleconnect.RegisterTicketSaleServiceHandler(s, rpc.NewOrganizerTicketSaleHandler(ticketSaleUC, organizerUC, logger))
		},
		// Organizer-facing SeatTypeService: a Series' seat types.
		func(s *connect.Server) {
			organizerseattypeconnect.RegisterSeatTypeServiceHandler(s, rpc.NewOrganizerSeatTypeHandler(seatTypeUC, organizerUC, logger))
		},
		// Organizer-facing TicketStockService: an event's tickets per seat type.
		func(s *connect.Server) {
			organizerticketstockconnect.RegisterTicketStockServiceHandler(s, rpc.NewOrganizerTicketStockHandler(ticketStockUC, organizerUC, logger))
		},
	}
	organizerSrv := server.NewConnectServer(organizerServerCfg, logger, organizerAuthFunc, rateLimiter, healthHandler, organizerInterceptors, nil, organizerHandlers...)

	// Reception Connect server — a fourth listener in the same binary on its
	// own port and CORS allowlist, serving ONLY the device-facing
	// ScannerService (rpc.scanner.v1: Open, Admit), called by a venue staff
	// device without a sign-in. Its caller is a link token and the bound
	// device's signature, checked by the use cases. It is exposed only by the
	// reception-api workload, apart from the organizer console's workload and
	// database role.
	receptionServerCfg := cfg.Server
	receptionServerCfg.Port = cfg.Server.ReceptionPort
	receptionServerCfg.AllowedOrigins = cfg.Server.ReceptionAllowedOrigins
	receptionInterceptors := []connect.ServerInterceptor{
		// Clients that make receptionUnknownTokenLimit scanner calls with
		// unknown link tokens within receptionUnknownTokenWindow are refused
		// with ResourceExhausted until the window passes.
		ratelimit.NewUnknownTokenInterceptor(
			ratelimit.NewClientThrottle(receptionUnknownTokenLimit, receptionUnknownTokenWindow, time.Now),
			auth.ReceptionPublicProcedures(),
			func(err error) bool { return errors.Is(err, usecase.ErrUnknownScannerLinkToken) },
		),
	}
	receptionHandlers := []server.RPCHandlerFunc{
		func(s *connect.Server) {
			scannerconnect.RegisterScannerServiceHandler(s, rpc.NewScannerHandler(scannerUC, ticketUC, logger))
		},
	}
	receptionSrv := server.NewConnectServer(receptionServerCfg, logger, receptionAuthFunc, rateLimiter, healthHandler, receptionInterceptors, nil, receptionHandlers...)

	// Zitadel Actions v2 webhook listener — runs on a separate port so the
	// webhook paths are unreachable via the public GKE Gateway. Validators
	// share the JWKS cache with `jwtValidator` so there is exactly one
	// refresh goroutine for all JWT verification.
	preAccessTokenHandler := webhook.NewPreAccessTokenHandler(
		jwtValidator.NewWebhookValidator(cfg.Webhook.PreAccessTokenAudience),
		logger,
	)
	// Login-event Actions v2 handler — bound to the event execution on
	// session.user.checked; emits account.login once per interactive login,
	// never on token refresh or a machine grant. The Target is PAYLOAD_TYPE_JSON,
	// so the handler verifies the HMAC ZITADEL-Signature with the Target's signing
	// key. Publishes a best-effort ACCOUNT.login event and never fails login.
	loginEventHandler := webhook.NewLoginEventHandler(
		cfg.Webhook.LoginEventSigningKey,
		userUC,
		eventPublisher,
		logger,
	)
	// Settlement task 4.3: Stripe webhook handler.
	// NOTE: the public HTTPS endpoint (GKE Gateway HTTPRoute → server-webhook-svc
	// on port 9090), Stripe dashboard registration, and the signing secret in
	// GCP Secret Manager are cloud-provisioning task 5.1. The handler is mounted
	// here and rejects requests with 503 when STRIPE_WEBHOOK_SIGNING_SECRET is
	// not yet configured, so the route is safe to expose before task 5.1 lands.
	stripeWebhookHandler := webhook.NewStripeWebhookHandler(
		cfg.Stripe.WebhookSigningSecret,
		stripeWebhookSvc,
		lotteryEntryUC,
		logger,
	)

	// Passkey-event Actions v2 handler — bound to the event executions on
	// user.human.passwordless.token.verified and token.removed; announces
	// USER.passkey_changed for the product org's identities (the change email
	// and the Passkey record are the consumer's).
	passkeyEventHandler := webhook.NewPasskeyEventHandler(
		cfg.Webhook.PasskeyEventSigningKey,
		cfg.Webhook.ProductOrgID,
		eventPublisher,
		logger,
	)

	// Password-setup-refusal Actions v2 handler — bound to the request
	// executions on the API methods that set the caller's own password; it
	// never answers 2xx, so Zitadel refuses them.
	passwordSetupRefusalHandler := webhook.NewPasswordSetupRefusalHandler(
		cfg.Webhook.PasswordSetupRefusalSigningKey,
		logger,
	)

	webhookSrv := server.NewWebhookServer(cfg.Webhook, logger, map[string]http.Handler{
		"/pre-access-token":      preAccessTokenHandler,
		"/account-login-event":   loginEventHandler,
		"/passkey-event":         passkeyEventHandler,
		"/refuse-password-setup": passwordSetupRefusalHandler,
		"/stripe-webhook":        stripeWebhookHandler,
	})

	// Register shutdown phases.
	// Drain: health → NOT_SERVING, then servers drain in-flight requests,
	// then cache cleanup goroutine stops.
	shutdown.AddDrainPhase(healthChecker, srv, adminSrv, organizerSrv, receptionSrv, webhookSrv, rateLimiter, artistCache)
	shutdown.AddFlushPhase(publisher)
	// jwtValidator owns the JWKS cache's refresh goroutines.
	shutdown.AddExternalPhase(lastfmClient, musicbrainzClient, jwtValidator)
	shutdown.AddObservePhase(telemetryCloser)
	shutdown.AddDatastorePhase(db)

	// Goroutine-leak detection (Go 1.27): internal pprof listener + sampler.
	startGoroutineLeakDetection(ctx, cfg.GoroutineLeak, cfg.Telemetry.ServiceName, logger)

	return &App{
		Server:          srv,
		AdminServer:     adminSrv,
		OrganizerServer: organizerSrv,
		ReceptionServer: receptionSrv,
		WebhookServer:   webhookSrv,
		Logger:          logger,
		ShutdownTimeout: cfg.ShutdownTimeout,
	}, nil
}
