package di

import (
	"io"

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
	resaleentryconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/resale_entry/v1/resale_entryv1connect"
	resalelistingconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/resale_listing/v1/resale_listingv1connect"
	scannerconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/scanner/v1/scannerv1connect"
	ticketconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/ticket/v1/ticketv1connect"
	ticketjourneyconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/ticket_journey/v1/ticket_journeyv1connect"
	ticketsalev1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/ticket_sale/v1/ticket_salev1connect"
	userconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/user/v1/userv1connect"
	walletpublickeyconnect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/wallet_public_key/v1/wallet_public_keyv1connect"
	"connectrpc.com/authn"
	"connectrpc.com/connect/v2"
	"connectrpc.com/grpchealth/v2"
	"context"
	"errors"
	"fmt"
	"github.com/liverty-music/backend/internal/adapter/linkpreview"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/adapter/webhook"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	gcsstorage "github.com/liverty-music/backend/internal/infrastructure/gcp/storage"
	googlemaps "github.com/liverty-music/backend/internal/infrastructure/maps/google"
	"github.com/liverty-music/backend/internal/infrastructure/music/lastfm"
	"github.com/liverty-music/backend/internal/infrastructure/music/musicbrainz"
	infrapayment "github.com/liverty-music/backend/internal/infrastructure/payment"
	infrapocketsign "github.com/liverty-music/backend/internal/infrastructure/pocketsign"
	"github.com/liverty-music/backend/internal/infrastructure/repository"
	"github.com/liverty-music/backend/internal/infrastructure/server"
	"github.com/liverty-music/backend/internal/infrastructure/server/ratelimit"
	infratelemetry "github.com/liverty-music/backend/internal/infrastructure/telemetry"
	infrawebpush "github.com/liverty-music/backend/internal/infrastructure/webpush"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/cache"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/liverty-music/backend/pkg/shutdown"
	"github.com/pannpers/go-logging/logging"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"log/slog"
	"net/http"
	"time"
)

// apiRepos are the repositories of the API server process.
type apiRepos struct {
	userRepo                  *rdb.UserRepository
	artistRepo                *rdb.ArtistRepository
	followRepo                *rdb.FollowRepository
	concertRepo               *rdb.ConcertRepository
	venueRepo                 *rdb.VenueRepository
	seriesRepo                *rdb.SeriesRepository
	concertSearchRepo         *rdb.ConcertSearchRepository
	concertCandidateRepo      *rdb.ConcertCandidateRepository
	rejectedConcertRepo       *rdb.RejectedConcertRepository
	pushSubRepo               *rdb.PushSubscriptionRepository
	ticketJourneyRepo         *rdb.TicketJourneyRepository
	organizerRepo             *rdb.OrganizerRepository
	identityVerificationRepo  *rdb.IdentityVerificationRepository
	lotteryEntryRepo          *rdb.LotteryEntryRepository
	eventPublishState         *rdb.EventPublishStateRepository
	orderRepo                 *repository.OrderRepository
	ticketRepo                *rdb.TicketRepository
	issuanceRepo              *rdb.IssuanceRepository
	settlementRepo            *repository.SettlementRepository
	refundRepo                *rdb.RefundRepository
	payoutAccountRepo         *repository.PayoutAccountRepository
	eventStartTimeRepo        *rdb.EventStartTimeRepository
	eventOrganizerRepo        *rdb.EventOrganizerRepository
	ticketSaleRepo            *rdb.TicketSaleRepository
	seatTypeRepo              *rdb.SeatTypeRepository
	ticketStockRepo           *rdb.TicketStockRepository
	checkoutRepo              *rdb.CheckoutRepository
	eventRepo                 *rdb.EventRepository
	resaleListingRepo         *rdb.ResaleListingRepository
	walletPublicKeyRepo       *rdb.WalletPublicKeyRepository
	scannerRepo               *rdb.ScannerRepository
	admissionRepo             *rdb.AdmissionRepository
	rejectedScanRepo          *rdb.RejectedScanRepository
	processedWebhookEventRepo *rdb.ProcessedWebhookEventRepository
	resaleEntryRepo           *rdb.ResaleEntryRepository
	notificationRepo          *rdb.NotificationRepository
	areaRepo                  *rdb.AreaRepository
}

// newAPIRepos builds the repositories of the API server. The Order,
// Settlement and PayoutAccount repositories combine their rows with their
// payment provider operations.
func newAPIRepos(db *rdb.Database, cfg *config.ServerConfig, logger *logging.Logger) apiRepos {
	payments := newPaymentProviders(cfg.Stripe.SecretKey, logger)
	return apiRepos{
		userRepo:                  rdb.NewUserRepository(db, newOutbox(logger)),
		areaRepo:                  rdb.NewAreaRepository(db),
		artistRepo:                rdb.NewArtistRepository(db, newOutbox(logger)),
		followRepo:                rdb.NewFollowRepository(db, newOutbox(logger)),
		concertRepo:               rdb.NewConcertRepository(db, newOutbox(logger)),
		venueRepo:                 rdb.NewVenueRepository(db),
		seriesRepo:                rdb.NewSeriesRepository(db, newOutbox(logger)),
		concertSearchRepo:         rdb.NewConcertSearchRepository(db),
		concertCandidateRepo:      rdb.NewConcertCandidateRepository(db),
		rejectedConcertRepo:       rdb.NewRejectedConcertRepository(db),
		pushSubRepo:               rdb.NewPushSubscriptionRepository(db, newOutbox(logger)),
		ticketJourneyRepo:         rdb.NewTicketJourneyRepository(db, newOutbox(logger)),
		organizerRepo:             rdb.NewOrganizerRepository(db, newOutbox(logger)),
		identityVerificationRepo:  rdb.NewIdentityVerificationRepository(db),
		lotteryEntryRepo:          rdb.NewLotteryEntryRepository(db, newOutbox(logger)),
		eventPublishState:         rdb.NewEventPublishStateRepository(db),
		orderRepo:                 repository.NewOrderRepository(rdb.NewOrderRepository(db), payments.orders),
		ticketRepo:                rdb.NewTicketRepository(db, newOutbox(logger)),
		issuanceRepo:              rdb.NewIssuanceRepository(db, newOutbox(logger)),
		settlementRepo:            repository.NewSettlementRepository(rdb.NewSettlementRepository(db), payments.settlements),
		refundRepo:                rdb.NewRefundRepository(db, newOutbox(logger)),
		payoutAccountRepo:         repository.NewPayoutAccountRepository(rdb.NewPayoutAccountRepository(db), payments.payoutAccounts),
		eventStartTimeRepo:        rdb.NewEventStartTimeRepository(db),
		eventOrganizerRepo:        rdb.NewEventOrganizerRepository(db),
		ticketSaleRepo:            rdb.NewTicketSaleRepository(db, newOutbox(logger)),
		seatTypeRepo:              rdb.NewSeatTypeRepository(db),
		ticketStockRepo:           rdb.NewTicketStockRepository(db),
		checkoutRepo:              rdb.NewCheckoutRepository(db, newOutbox(logger)),
		eventRepo:                 rdb.NewEventRepository(db),
		resaleListingRepo:         rdb.NewResaleListingRepository(db, newOutbox(logger)),
		walletPublicKeyRepo:       rdb.NewWalletPublicKeyRepository(db),
		scannerRepo:               rdb.NewScannerRepository(db),
		admissionRepo:             rdb.NewAdmissionRepository(db),
		rejectedScanRepo:          rdb.NewRejectedScanRepository(db),
		processedWebhookEventRepo: rdb.NewProcessedWebhookEventRepository(db),
		resaleEntryRepo:           rdb.NewResaleEntryRepository(db, newOutbox(logger)),
		notificationRepo:          rdb.NewNotificationRepository(db),
	}
}

// apiWiring holds what the API server's usecases, servers and webhook
// handlers are built from, and the usecases themselves.
type apiWiring struct {
	ctx    context.Context
	cfg    *config.ServerConfig
	db     *rdb.Database
	logger *logging.Logger
	apiRepos

	userUC    usecase.UserUseCase
	passkeyUC usecase.PasskeyUseCase
	concertUC usecase.ConcertUseCase
	// concertCandidateUC is built from the concrete concert usecase.
	concertCandidateUC usecase.ConcertCandidateUseCase
	// lotteryCardAuthorizations are the card authorizations of a lottery or
	// resale entry; the Stripe webhook releases them too.
	lotteryCardAuthorizations entity.CardAuthorizationRepository
	// checkoutCardAuthorizations are the card authorizations of a checkout.
	checkoutCardAuthorizations entity.CardAuthorizationRepository
	artistUC                   usecase.ArtistUseCase
	organizerUC                usecase.OrganizerUseCase
	seriesUC                   usecase.SeriesUseCase
	identityVerificationUC     usecase.IdentityVerificationUseCase
	seriesImageUC              usecase.SeriesImageUseCase
	lotteryEntryUC             usecase.LotteryEntryUseCase
	resaleEntryUC              usecase.ResaleEntryUseCase
	seatTypeUC                 usecase.SeatTypeUseCase
	ticketStockUC              usecase.TicketStockUseCase
	ticketSaleUC               usecase.TicketSaleUseCase
	checkoutUC                 usecase.CheckoutUseCase
	ticketUC                   usecase.TicketUseCase
	walletPublicKeyUC          usecase.WalletPublicKeyUseCase
	scannerUC                  usecase.ScannerUseCase
	orderUC                    usecase.OrderUseCase
	resaleListingUC            usecase.ResaleListingUseCase
	stripeWebhookSvc           usecase.StripeWebhookService
	payoutAccountUC            usecase.PayoutAccountUseCase
	followUC                   usecase.FollowUseCase
	ticketJourneyUC            usecase.TicketJourneyUseCase
	notificationUC             usecase.NotificationUseCase
	pushSubscriptionUC         usecase.PushSubscriptionUseCase
	workflows                  usecase.Workflows
	businessMetrics            *infratelemetry.BusinessMetrics
	imageStorer                usecase.ImageStorer
	mapCatalog                 *googlemaps.Client
	seriesImageURLBuilder      *gcsstorage.SeriesImageURLBuilder
	jwtValidator               *auth.JWTValidator
	authFunc                   authn.AuthFunc
	adminAuthFunc              authn.AuthFunc
	organizerAuthFunc          authn.AuthFunc
	receptionAuthFunc          authn.AuthFunc
	healthChecker              *rpc.HealthCheckHandler
	healthHandler              func(*connect.Server)
	rateLimiter                *ratelimit.Limiter
	artistCache                *cache.MemoryCache

	// externals are the clients to close in the external phase of the shutdown.
	externals []io.Closer
}

// buildCatalogue builds the usecases of fans and the catalogue: users, passkeys,
// concerts, artists and follows.
func (w *apiWiring) buildCatalogue() error {
	ctx := w.ctx
	cfg, db, logger := w.cfg, w.db, w.logger
	var err error
	// Infrastructure - Gemini (optional)
	geminiHTTPClient := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	geminiSearcher, err := provideConcertSearcher(ctx, cfg.GCP, nil, geminiHTTPClient, logger)
	if err != nil {
		return err
	}

	// Infrastructure - Music
	musicHTTPClient := &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	lastfmClient := lastfm.NewClient(cfg.LastFMAPIKey, musicHTTPClient, logger)
	musicbrainzClient := musicbrainz.NewClient(musicHTTPClient, logger)

	// Cache - Artist discovery results (search, similar, top) for usecase.ArtistCacheTTL (6 h)
	w.artistCache = cache.NewMemoryCache(usecase.ArtistCacheTTL)

	// Infrastructure - fan identity removal (admin and fan workloads) and
	// email verification (fan workload) through the identity service; nil
	// elsewhere and in local dev without the identity service.
	identityRemover, emailCodes := provideIdentityClients(ctx, cfg, logger)

	w.userUC = usecase.NewUserUseCase(w.userRepo, w.areaRepo, usecase.DeletionBlockerReads{
		Tickets:        w.ticketRepo,
		Events:         w.eventRepo,
		ResaleListings: w.resaleListingRepo,
		LotteryEntries: w.lotteryEntryRepo,
		Checkouts:      w.checkoutRepo,
		Orders:         w.orderRepo,
		Now:            time.Now,
	}, w.workflows, nil, emailCodes, identityRemover, usecase.PasskeyMailers{}, time.Now, logger)
	w.passkeyUC = usecase.NewPasskeyUseCase(rdb.NewPasskeyRepository(db, newOutbox(logger)), w.userRepo, w.workflows, time.Now, logger)

	concertUC := usecase.NewConcertUseCase(w.artistRepo, w.concertRepo, w.venueRepo, w.seriesRepo, w.organizerRepo, w.concertSearchRepo, w.concertCandidateRepo, w.rejectedConcertRepo, geminiSearcher, w.areaRepo, w.workflows, w.businessMetrics, nil, cfg.GCP.SearchCacheTTL(), cfg.GCP.SearchDiscoveryWindow(), time.Now, logger)
	w.concertUC = concertUC
	w.concertCandidateUC = usecase.NewConcertCandidateUseCase(concertUC)
	w.artistUC = usecase.NewArtistUseCase(w.artistRepo, w.organizerRepo, lastfmClient, musicbrainzClient, w.artistCache, time.Now, logger)
	w.followUC = usecase.NewFollowUseCase(w.followRepo, w.artistRepo, musicbrainzClient, w.businessMetrics, logger)
	w.externals = append(w.externals, lastfmClient, musicbrainzClient)
	return nil
}

// buildOrganizer builds the usecases of Organizers: their series, the map
// catalog, identity verification and series images.
func (w *apiWiring) buildOrganizer() error {
	ctx := w.ctx
	cfg, db, logger := w.cfg, w.db, w.logger
	var err error
	organizationRepo := provideOrganizationRepository(cfg, logger)
	// GCS image storer for organizer series images. Local dev has no GCP credentials,
	// so it runs without one and signed-URL issuance returns Internal; outside
	// local a storer that cannot be created stops the server.
	if !cfg.IsLocal() {
		storer, err := gcsstorage.NewGCSStorer(ctx, logger)
		if err != nil {
			return fmt.Errorf("create GCS image storer: %w", err)
		}
		w.imageStorer = storer
		shutdown.AddExternalPhase(storer)
	}
	signInLinkSender, err := newSignInLinkSender(cfg, logger)
	if err != nil {
		return err
	}
	w.organizerUC = usecase.NewOrganizerUseCase(w.organizerRepo, signInLinkSender, w.artistRepo, organizationRepo, rdb.NewSeriesImageRepository(db, newOutbox(logger)), w.imageStorer,
		usecase.StorageBuckets{Internal: cfg.StorageInternalBucket, Served: cfg.StorageBucket},
		w.businessMetrics, time.Now, logger)
	// Run the provisioning reconciler only on the workload that provisions
	// organizers (the isolated admin workload); it completes any organizer left
	// in the provisioning state after a partial failure.
	if cfg.ProvisionsOrganizers() {
		startOrganizerReconciler(ctx, w.organizerUC, logger)
	}
	// Map catalog (Google Maps Places API) that members pick event
	// venues from.
	w.mapCatalog, err = newVenueMapCatalog(ctx, cfg.GCP.ProjectID, cfg.IsLocal(), logger)
	if err != nil {
		return err
	}
	w.seriesUC = usecase.NewSeriesUseCase(w.seriesRepo, rdb.NewSeriesImageRepository(db, newOutbox(logger)), w.venueRepo, w.mapCatalog, w.organizerRepo, w.ticketSaleRepo,
		w.imageStorer, usecase.StorageBuckets{Internal: cfg.StorageInternalBucket, Served: cfg.StorageBucket}, logger, time.Now)

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
		// shutdown, like the other MemoryCache instances (cf. w.artistCache).
		shutdown.AddDrainPhase(stampClient)
		logger.Info(ctx, "pocket sign verify client initialized",
			slog.String("base_url", cfg.PocketSign.BaseURL),
		)
	} else {
		pocketSignVerifier = infrapocketsign.NewStubVerifier()
		logger.Info(ctx, "pocket sign verify client not configured; using stub (identity verification unavailable)")
	}
	w.identityVerificationUC = usecase.NewIdentityVerificationUseCase(w.identityVerificationRepo, w.userRepo, pocketSignVerifier, time.Now, logger)
	// SeriesImageUseCase.Process is never called from the RPC server (only the dedicated
	// series-image-consumer job in series_image_consumer.go invokes it), so no SeriesImageProcessor is
	// wired here.
	w.seriesImageUC = usecase.NewSeriesImageUseCase(w.seriesRepo, rdb.NewSeriesImageRepository(db, newOutbox(logger)), w.imageStorer, nil, logger)
	// The series-image URL builder composes the CDN URLs of Series cover
	// images for the handlers (pb.SeriesImageURLs). The CDN base is sourced
	// from config (CDN_BASE_URL).
	w.seriesImageURLBuilder = gcsstorage.NewSeriesImageURLBuilder(cfg.CDNBaseURL)
	return nil
}

// buildCardAuthorizations builds the card authorization repositories.
func (w *apiWiring) buildCardAuthorizations() error {
	cfg, db, logger := w.cfg, w.db, w.logger
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
		lotteryCardAuthorizations = rdb.NewCardAuthorizationRepository(db, infrapayment.NewStripeCardAuthorizationProvider(cfg.Stripe.SecretKey, infrapayment.LotteryCardAuthorizationPolicy, logger))
		checkoutCardAuthorizations = rdb.NewCardAuthorizationRepository(db, infrapayment.NewStripeCardAuthorizationProvider(cfg.Stripe.SecretKey, infrapayment.CheckoutCardAuthorizationPolicy, logger))
	} else {
		noop := rdb.NewCardAuthorizationRepository(db, infrapayment.NewNoopCardAuthorizationProvider(logger))
		lotteryCardAuthorizations, checkoutCardAuthorizations = noop, noop
	}
	w.lotteryCardAuthorizations = lotteryCardAuthorizations
	w.checkoutCardAuthorizations = checkoutCardAuthorizations
	return nil
}

// buildEntries builds the usecases of lottery and resale entries.
func (w *apiWiring) buildEntries() error {
	logger := w.logger
	w.lotteryEntryUC = usecase.NewLotteryEntryUseCase(usecase.LotteryEntryDeps{
		SaleRepo:                 w.ticketSaleRepo,
		StockRepo:                w.ticketStockRepo,
		EntryRepo:                w.lotteryEntryRepo,
		OrderRepo:                w.orderRepo,
		EventRepo:                w.eventRepo,
		EventState:               w.eventPublishState,
		IdentityVerificationRepo: w.identityVerificationRepo,
		UserRepo:                 w.userRepo,
		CardAuthorizations:       w.lotteryCardAuthorizations,
		Clock:                    time.Now,
		Logger:                   logger,
	})

	// Official resale by lottery: a resale entry's card authorization, like a
	// lottery entry's, sends no idempotency key, so it shares the lottery's.
	w.resaleEntryUC = usecase.NewResaleEntryUseCase(usecase.ResaleEntryDeps{
		EntryRepo:                w.resaleEntryRepo,
		StockRepo:                w.ticketStockRepo,
		SaleRepo:                 w.ticketSaleRepo,
		OrderRepo:                w.orderRepo,
		EventRepo:                w.eventRepo,
		EventState:               w.eventPublishState,
		IdentityVerificationRepo: w.identityVerificationRepo,
		UserRepo:                 w.userRepo,
		CardAuthorizations:       w.lotteryCardAuthorizations,
		Clock:                    time.Now,
		Logger:                   logger,
	})
	return nil
}

// buildTicketing builds the usecases of ticket sales: seat types, stocks, sales,
// checkouts, tickets, scanners, orders and listings.
func (w *apiWiring) buildTicketing() error {
	logger := w.logger

	// Seat types and ticket stocks, the sales (Lottery and FirstCome) that
	// sell the stocks of their events, and the fan's 15-minute first-come
	// checkout.
	w.seatTypeUC = usecase.NewSeatTypeUseCase(w.seatTypeRepo, w.seriesRepo)
	w.ticketStockUC = usecase.NewTicketStockUseCase(w.ticketStockRepo, w.seatTypeRepo, w.eventRepo, w.seriesRepo, w.eventOrganizerRepo, time.Now)
	w.ticketSaleUC = usecase.NewTicketSaleUseCase(w.ticketSaleRepo, w.ticketStockRepo, w.lotteryEntryRepo, w.orderRepo, w.seriesRepo, w.organizerRepo, w.eventRepo, w.eventPublishState, w.eventStartTimeRepo, time.Now)
	w.checkoutUC = usecase.NewCheckoutUseCase(usecase.CheckoutDeps{
		CheckoutRepo:             w.checkoutRepo,
		SaleRepo:                 w.ticketSaleRepo,
		StockRepo:                w.ticketStockRepo,
		EntryRepo:                w.lotteryEntryRepo,
		OrderRepo:                w.orderRepo,
		UserRepo:                 w.userRepo,
		IdentityVerificationRepo: w.identityVerificationRepo,
		EventState:               w.eventPublishState,
		EventRepo:                w.eventRepo,
		CardAuthorizations:       w.checkoutCardAuthorizations,
		Clock:                    time.Now,
		Logger:                   logger,
	})
	w.ticketUC = usecase.NewTicketUseCase(w.orderRepo, w.ticketRepo, w.scannerRepo, w.eventRepo, w.walletPublicKeyRepo, w.admissionRepo, w.rejectedScanRepo, logger)

	// ⑥ ticket wallet and venue admission (Scanners).
	w.walletPublicKeyUC = usecase.NewWalletPublicKeyUseCase(w.walletPublicKeyRepo, logger)
	w.scannerUC = usecase.NewScannerUseCase(w.scannerRepo, w.eventRepo, w.eventOrganizerRepo, logger)

	// Orders: the fan's and the admin's reads, the placing of a fan's
	// Checkout (its issue runs as a workflow in the workflow worker), and the
	// refund policy (w.refundRepo provides the single
	// atomic DB commit; the idempotent Stripe calls run first).
	w.orderUC = usecase.NewOrderUseCase(usecase.OrderDeps{
		IssuanceRepo:             w.issuanceRepo,
		OrderRepo:                w.orderRepo,
		EntryRepo:                w.lotteryEntryRepo,
		EventOrganizerRepo:       w.eventOrganizerRepo,
		OrganizerRepo:            w.organizerRepo,
		IdentityVerificationRepo: w.identityVerificationRepo,
		CheckoutRepo:             w.checkoutRepo,
		TicketSaleRepo:           w.ticketSaleRepo,
		StockRepo:                w.ticketStockRepo,
		TicketRepo:               w.ticketRepo,
		EventState:               w.eventPublishState,
		EventRepo:                w.eventRepo,
		CardAuthorizations:       w.checkoutCardAuthorizations,
		Clock:                    time.Now,
		RefundRepo:               w.refundRepo,
		SettlementRepo:           w.settlementRepo,
		Workflows:                w.workflows,
		Logger:                   logger,
	})

	// Official resale: a fan lists a ticket of a sold-out ticket stock; the
	// purchase of listings comes with resell-tickets-by-lottery. The refund
	// job pays the listing fan back within a minute and announces it; the
	// event consumer sends the sold email.
	w.resaleListingUC = usecase.NewResaleListingUseCase(usecase.ResaleListingDeps{
		Listings:   w.resaleListingRepo,
		Tickets:    w.ticketRepo,
		Orders:     w.orderRepo,
		Stocks:     w.ticketStockRepo,
		EventState: w.eventPublishState,
		Events:     w.eventRepo,
		Clock:      time.Now,
		Logger:     logger,
	})
	return nil
}

// buildPayments builds the Stripe webhook service and the payout usecases. The
// payout itself, releasing the held Settlements, is not run here: it is a
// schedule of the workflow worker (OpenSpec change add-prod-preview-release,
// design D10), so no API process carries a payout job.
func (w *apiWiring) buildPayments() error {
	cfg, logger := w.cfg, w.logger
	// Settlement task 4.3: Stripe webhook ingest — idempotent dispatch.
	// w.settlementRepo removed from constructor: it was previously injected but
	// never used (the dead orderID field made the dispute path unreachable).
	// Resolution is now via w.orderRepo.GetByProviderPaymentID.
	w.stripeWebhookSvc = usecase.NewStripeWebhookService(
		w.processedWebhookEventRepo,
		w.orderRepo,
		w.orderUC,
		w.lotteryCardAuthorizations,
		w.lotteryEntryUC,
		w.resaleEntryUC,
		logger,
	)

	// Settlement / payout pipeline (ticket-settlement-and-payout).
	w.payoutAccountUC = usecase.NewPayoutAccountUseCase(
		w.payoutAccountRepo,
		w.organizerRepo,
		cfg.Stripe.OnboardingReturnURL,
		logger,
	)
	return nil
}

// buildNotifications builds the usecases of ticket journeys and notifications.
func (w *apiWiring) buildNotifications() error {
	cfg, logger := w.cfg, w.logger
	w.ticketJourneyUC = usecase.NewTicketJourneyUseCase(w.ticketJourneyRepo, w.userRepo, w.concertRepo, w.followRepo, w.lotteryEntryRepo, logger)
	webpushSender := infrawebpush.NewSender(cfg.VAPID.PublicKey, cfg.VAPID.PrivateKey, cfg.VAPID.Contact)
	// The API never sends order confirmations (the consumer does), so the
	// order-confirmation dependencies stay empty here.
	w.notificationUC = usecase.NewNotificationUseCase(w.notificationRepo, w.pushSubRepo, webpushSender, w.workflows, w.businessMetrics, logger, usecase.OrderConfirmationDeps{Now: time.Now},
		usecase.NewConcertsDeps{ArtistRepo: w.artistRepo, ConcertRepo: w.concertRepo, FollowRepo: w.followRepo},
		usecase.SubjectDeps{
			Now:           time.Now,
			Users:         w.userRepo,
			Follows:       w.followRepo,
			Artists:       w.artistRepo,
			Concerts:      w.concertRepo,
			Orders:        w.orderRepo,
			TicketSales:   w.ticketSaleRepo,
			Entries:       w.lotteryEntryRepo,
			ResaleEntries: w.resaleEntryRepo,
		})
	w.pushSubscriptionUC = usecase.NewPushSubscriptionUseCase(w.pushSubRepo, logger)
	return nil
}

// buildAuth builds the JWT validator and the authentication function of each server.
func (w *apiWiring) buildAuth() error {
	cfg := w.cfg
	var err error
	// Auth - JWT Validator and Interceptor
	w.jwtValidator, err = auth.NewJWTValidator(
		cfg.JWT.Issuer,
		cfg.JWT.Issuer+"/oauth/v2/keys",
		cfg.JWT.JWKSRefreshInterval,
	)
	if err != nil {
		return err
	}

	// Apply additional accepted issuers for multi-provider support (Option C migration).
	if len(cfg.JWT.AcceptedIssuers) > 0 {
		all := append([]string{cfg.JWT.Issuer}, cfg.JWT.AcceptedIssuers...)
		w.jwtValidator = w.jwtValidator.WithAcceptedIssuers(all)
	}

	publicProcedures := auth.FanPublicProcedures()

	w.authFunc = auth.NewAuthFunc(w.jwtValidator, publicProcedures)

	// The admin server gets its own AuthFunc with NO public procedures, ever.
	// It must NOT reuse the consumer w.authFunc above: the admin server also
	// mounts the consumer ArtistService handler (see adminHandlers below), so
	// procedures like "/ArtistService/ListTop" resolve to the same name on
	// both servers. Sharing w.authFunc would let publicProcedures' fan-only
	// entries for ListTop/ListSimilar/Search leak onto the admin host and let
	// an unauthenticated caller reach RequireRoleInterceptor instead of being
	// rejected with Unauthenticated by the authn middleware first (backend#481).
	// Both AuthFuncs share the same w.jwtValidator, so token validation itself
	// is identical; only the public-procedure allowlist differs.
	w.adminAuthFunc = auth.NewAuthFunc(w.jwtValidator, nil)

	// The organizer server gets its own AuthFunc too. Its only public
	// procedure is OrganizerService.SendSignInLinks; every other organizer
	// call is a signed-in member's.
	w.organizerAuthFunc = auth.NewAuthFunc(w.jwtValidator, auth.OrganizerPublicProcedures())

	// The reception server's AuthFunc makes only the scanner device's procedures
	// public (no sign-in; link token + device signature). Nothing else is
	// mounted there.
	w.receptionAuthFunc = auth.NewAuthFunc(w.jwtValidator, auth.ReceptionPublicProcedures())
	return nil
}

// buildServing builds the health check and the rate limiter the servers share.
func (w *apiWiring) buildServing() error {
	cfg, db, logger := w.cfg, w.db, w.logger
	// Health check handler (public, outside authn middleware).
	// Keep a reference so App.Shutdown can call SetShuttingDown.
	w.healthChecker = rpc.NewHealthCheckHandler(db.Pool, logger)
	w.healthHandler = func(s *connect.Server) {
		grpchealth.Register(s, w.healthChecker)
	}

	// Admin RPC handlers — served ONLY by the dedicated admin server, whose
	// server-wide RequireRoleInterceptor gates every procedure on the "admin"
	// role. The consumer server below does NOT register these, so the admin
	// surface cannot be reached via the consumer host.
	w.rateLimiter = ratelimit.NewLimiter(ratelimit.Config{
		AuthRPS:   cfg.Server.RateLimit.AuthRPS,
		AuthBurst: cfg.Server.RateLimit.AuthBurst,
		AnonRPS:   cfg.Server.RateLimit.AnonRPS,
		AnonBurst: cfg.Server.RateLimit.AnonBurst,
	}, time.Minute, time.Now)

	// Consumer Connect server — all consumer services, no admin service; its
	// only extra interceptor is the per-client limit below.
	// Link preview tags for the fan web's /concerts/{id} HTML (fetched by Caddy
	// templates). Public and outside the RPC rate limit, since every crawler
	// request reaches fan-api from Caddy's Pod IP.
	return nil
}

// fanHandlers returns the consumer RPC handlers (protected by authn middleware).
func (w *apiWiring) fanHandlers() []server.RPCHandlerFunc {
	cfg, logger := w.cfg, w.logger
	return append([]server.RPCHandlerFunc{
		func(s *connect.Server) {
			userconnect.RegisterUserServiceHandler(s, rpc.NewUserHandler(w.userUC, w.jwtValidator.NewFreshSignInVerifier(cfg.FanClientID, time.Now), logger))
		},
		func(s *connect.Server) {
			artistconnect.RegisterArtistServiceHandler(s, rpc.NewArtistHandler(w.artistUC, logger))
		},
		func(s *connect.Server) {
			followconnect.RegisterFollowServiceHandler(s, rpc.NewFollowHandler(w.followUC, w.userUC, logger))
		},
		func(s *connect.Server) {
			pushsubscriptionconnect.RegisterPushSubscriptionServiceHandler(s, rpc.NewPushSubscriptionHandler(w.pushSubscriptionUC, w.userUC, logger))
		},
		func(s *connect.Server) {
			notificationconnect.RegisterNotificationServiceHandler(s, rpc.NewNotificationHandler(w.notificationUC, w.userUC, logger))
		},
		func(s *connect.Server) {
			ticketjourneyconnect.RegisterTicketJourneyServiceHandler(s, rpc.NewTicketJourneyHandler(w.ticketJourneyUC, w.userUC, logger))
		},
		func(s *connect.Server) {
			ticketconnect.RegisterTicketServiceHandler(s, rpc.NewTicketHandler(w.ticketUC, w.userUC, logger))
		},
		func(s *connect.Server) {
			orderconnect.RegisterOrderServiceHandler(s, rpc.NewOrderHandler(w.orderUC, w.userUC, logger))
		},
		func(s *connect.Server) {
			officialsiteconnect.RegisterOfficialSiteServiceHandler(s, rpc.NewOfficialSiteHandler(usecase.NewOfficialSiteUseCase(w.artistRepo), logger))
		},
		// Fan-facing WalletPublicKeyService: Register, Get (signed-in fan only).
		func(s *connect.Server) {
			walletpublickeyconnect.RegisterWalletPublicKeyServiceHandler(s, rpc.NewWalletPublicKeyHandler(w.walletPublicKeyUC, w.userUC, logger))
		},
		func(s *connect.Server) {
			identityverificationconnect.RegisterIdentityVerificationServiceHandler(s, rpc.NewIdentityVerificationHandler(w.identityVerificationUC, w.userUC, logger))
		},
		// Fan-facing PasskeyService: List (the signed-in fan's recorded passkeys).
		func(s *connect.Server) {
			passkeyconnect.RegisterPasskeyServiceHandler(s, rpc.NewPasskeyHandler(
				w.passkeyUC, w.userUC, logger))
		},
	}, w.fanTicketingHandlers()...)
}

// fanTicketingHandlers returns the fan-facing ticket sale handlers: TicketSaleService
// (public reads), the signed-in fan's first-come CheckoutService,
// LotteryEntryService, official ResaleListingService and ResaleEntryService, and
// the ConcertService.
func (w *apiWiring) fanTicketingHandlers() []server.RPCHandlerFunc {
	logger := w.logger
	return []server.RPCHandlerFunc{
		func(s *connect.Server) {
			ticketsalev1connect.RegisterTicketSaleServiceHandler(s, rpc.NewTicketSaleHandler(w.ticketSaleUC, logger))
		},
		func(s *connect.Server) {
			checkoutconnect.RegisterCheckoutServiceHandler(s, rpc.NewCheckoutHandler(w.checkoutUC, w.orderUC, w.userUC, logger))
		},
		func(s *connect.Server) {
			lotteryentryconnect.RegisterLotteryEntryServiceHandler(s, rpc.NewLotteryEntryHandler(w.lotteryEntryUC, w.userUC, logger))
		},
		func(s *connect.Server) {
			resalelistingconnect.RegisterResaleListingServiceHandler(s, rpc.NewResaleListingHandler(w.resaleListingUC, w.userUC, logger))
		},
		func(s *connect.Server) {
			resaleentryconnect.RegisterResaleEntryServiceHandler(s, rpc.NewResaleEntryHandler(w.resaleEntryUC, w.userUC, logger))
		},
		func(s *connect.Server) {
			concertconnect.RegisterConcertServiceHandler(s, rpc.NewConcertHandler(w.concertUC, w.userUC, w.seriesImageURLBuilder, logger))
		},
	}
}

// consumerServer builds the consumer Connect server: all consumer services, no admin
// service; its only extra interceptor is the per-client limit.
func (w *apiWiring) consumerServer() *server.ConnectServer {
	cfg, logger := w.cfg, w.logger
	handlers := w.fanHandlers()
	// Consumer Connect server — all consumer services, no admin service; its
	// only extra interceptor is the per-client limit below.
	// Link preview tags for the fan web's /concerts/{id} HTML (fetched by Caddy
	// templates). Public and outside the RPC rate limit, since every crawler
	// request reaches fan-api from Caddy's Pod IP.
	linkPreviewHandler := linkpreview.NewHandler(w.concertUC, linkpreview.NewTagBuilder(cfg.FanWebBaseURL, w.seriesImageURLBuilder), logger)
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
	srv := server.NewConnectServer(cfg.Server, logger, w.authFunc, w.rateLimiter, w.healthHandler, fanInterceptors, publicRoutes, handlers...)
	return srv
}

// adminServer builds the admin Connect server (see below).
func (w *apiWiring) adminServer() *server.ConnectServer {
	cfg, logger := w.cfg, w.logger
	// Admin RPC handlers — served ONLY by the dedicated admin server, whose
	// server-wide RequireRoleInterceptor gates every procedure on the "admin"
	// role. The consumer server below does NOT register these, so the admin
	// surface cannot be reached via the consumer host.
	adminHandlers := []server.RPCHandlerFunc{
		func(s *connect.Server) {
			adminconcertconnect.RegisterConcertServiceHandler(s, rpc.NewAdminConcertHandler(w.concertUC, w.seriesImageURLBuilder, logger))
		},
		func(s *connect.Server) {
			adminorganizerconnect.RegisterOrganizerServiceHandler(s, rpc.NewAdminOrganizerHandler(w.organizerUC, logger))
		},
		// Mount the consumer ArtistService on the admin server so the admin
		// console can reuse ArtistService.Search (to pick an artist to
		// associate) via the admin host + admin token, instead of a
		// cross-origin call to the consumer API. Per design D2, the
		// higher-privilege admin server may additionally mount consumer
		// handlers as needed; the server-wide RequireRoleInterceptor(admin)
		// gates every method here.
		func(s *connect.Server) {
			artistconnect.RegisterArtistServiceHandler(s, rpc.NewArtistHandler(w.artistUC, logger))
		},
		// Admin UserService: Delete (admin-delete-test-data). Removes a fan
		// User and its Zitadel identity; never mounted on the consumer server.
		func(s *connect.Server) {
			adminuserconnect.RegisterUserServiceHandler(s, rpc.NewAdminUserHandler(w.userUC, logger))
		},
		// Admin OrderService: Refund (⑤ task 4.1).
		func(s *connect.Server) {
			adminorderconnect.RegisterOrderServiceHandler(s, rpc.NewAdminOrderHandler(w.orderUC, logger))
		},
		// Admin ConcertCandidateService: review of concert candidates.
		func(s *connect.Server) {
			adminconcertcandidateconnect.RegisterConcertCandidateServiceHandler(s, rpc.NewAdminConcertCandidateHandler(w.concertCandidateUC, logger))
		},
		// Admin ArtistService: ListByOrganizer.
		func(s *connect.Server) {
			adminartistconnect.RegisterArtistServiceHandler(s, rpc.NewAdminArtistHandler(w.artistUC, logger))
		},
		// Admin NotificationService: NotifyNewConcerts.
		func(s *connect.Server) {
			adminnotificationconnect.RegisterNotificationServiceHandler(s, rpc.NewAdminNotificationHandler(w.notificationUC, cfg.BaseConfig, logger))
		},
	}
	// Admin Connect server — a second listener in the same binary on its own
	// port and CORS allowlist, serving ONLY admin services. Its server-wide
	// RequireRoleInterceptor (admin role) is the sole, structural authorization
	// gate (handlers carry no per-method role check). It uses its own
	// w.adminAuthFunc (no public procedures — see above) rather than the
	// consumer server's w.authFunc, and shares the rate limiter and health
	// checker with the consumer server.
	adminServerCfg := cfg.Server
	adminServerCfg.Port = cfg.Server.AdminPort
	adminServerCfg.AllowedOrigins = cfg.Server.AdminAllowedOrigins
	adminInterceptors := []connect.ServerInterceptor{auth.NewRequireRoleInterceptor("admin")}
	adminSrv := server.NewConnectServer(adminServerCfg, logger, w.adminAuthFunc, w.rateLimiter, w.healthHandler, adminInterceptors, nil, adminHandlers...)
	return adminSrv
}

// organizerHandlers returns the handlers of the organizer Connect server.
func (w *apiWiring) organizerHandlers() []server.RPCHandlerFunc {
	logger := w.logger
	return []server.RPCHandlerFunc{
		func(s *connect.Server) {
			organizerconnect.RegisterOrganizerServiceHandler(s, rpc.NewOrganizerHandler(w.organizerUC, logger))
		},
		func(s *connect.Server) {
			organizerseriesconnect.RegisterSeriesServiceHandler(s, rpc.NewOrganizerSeriesHandler(w.seriesUC, w.organizerUC, w.seriesImageURLBuilder, logger))
		},
		// Organizer-facing VenueService: Search (map catalog venue lookup).
		func(s *connect.Server) {
			organizervenueconnect.RegisterVenueServiceHandler(s, rpc.NewOrganizerVenueHandler(usecase.NewVenueUseCase(w.mapCatalog), w.organizerUC))
		},
		// Organizer-facing SeriesImageService: CreateUploadUrl.
		func(s *connect.Server) {
			organizerseriesimageconnect.RegisterSeriesImageServiceHandler(s, rpc.NewOrganizerSeriesImageHandler(w.seriesImageUC, w.organizerUC))
		},
		// Organizer-facing ArtistService: List (the Organizer's own Artists).
		func(s *connect.Server) {
			organizerartistconnect.RegisterArtistServiceHandler(s, rpc.NewOrganizerArtistHandler(w.artistUC, w.organizerUC, logger))
		},
		// Organizer-facing PayoutAccountService: Get.
		func(s *connect.Server) {
			payoutaccountconnect.RegisterPayoutAccountServiceHandler(s, rpc.NewPayoutAccountHandler(w.payoutAccountUC, w.organizerUC, logger))
		},
		// Organizer-facing ScannerService: Create, ListByEvent, Revoke.
		func(s *connect.Server) {
			organizerscannerconnect.RegisterScannerServiceHandler(s, rpc.NewOrganizerScannerHandler(w.scannerUC, w.organizerUC, logger))
		},
		// Organizer-facing TicketSaleService: the Event's Lottery and FirstCome sales.
		func(s *connect.Server) {
			organizerticketsaleconnect.RegisterTicketSaleServiceHandler(s, rpc.NewOrganizerTicketSaleHandler(w.ticketSaleUC, w.organizerUC, logger))
		},
		// Organizer-facing SeatTypeService: a Series' seat types.
		func(s *connect.Server) {
			organizerseattypeconnect.RegisterSeatTypeServiceHandler(s, rpc.NewOrganizerSeatTypeHandler(w.seatTypeUC, w.organizerUC, logger))
		},
		// Organizer-facing TicketStockService: an event's tickets per seat type.
		func(s *connect.Server) {
			organizerticketstockconnect.RegisterTicketStockServiceHandler(s, rpc.NewOrganizerTicketStockHandler(w.ticketStockUC, w.organizerUC, logger))
		},
	}
}

// organizerServer builds the organizer Connect server.
func (w *apiWiring) organizerServer() *server.ConnectServer {
	cfg, logger := w.cfg, w.logger
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
	organizerHandlers := w.organizerHandlers()
	organizerSrv := server.NewConnectServer(organizerServerCfg, logger, w.organizerAuthFunc, w.rateLimiter, w.healthHandler, organizerInterceptors, nil, organizerHandlers...)
	return organizerSrv
}

// receptionServer builds the reception Connect server.
func (w *apiWiring) receptionServer() *server.ConnectServer {
	cfg, logger := w.cfg, w.logger
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
			scannerconnect.RegisterScannerServiceHandler(s, rpc.NewScannerHandler(w.scannerUC, w.ticketUC, logger))
		},
	}
	receptionSrv := server.NewConnectServer(receptionServerCfg, logger, w.receptionAuthFunc, w.rateLimiter, w.healthHandler, receptionInterceptors, nil, receptionHandlers...)
	return receptionSrv
}

// webhookServer builds the Zitadel Actions v2 and Stripe webhook listener.
func (w *apiWiring) webhookServer() *server.WebhookServer {
	cfg, logger := w.cfg, w.logger
	// Zitadel Actions v2 webhook listener — runs on a separate port so the
	// webhook paths are unreachable via the public GKE Gateway. Validators
	// share the JWKS cache with `w.jwtValidator` so there is exactly one
	// refresh goroutine for all JWT verification.
	preAccessTokenHandler := webhook.NewPreAccessTokenHandler(
		w.jwtValidator.NewWebhookValidator(cfg.Webhook.PreAccessTokenAudience),
		logger,
	)
	// Login-event Actions v2 handler — bound to the event execution on
	// session.user.checked; records each interactive login,
	// never on token refresh or a machine grant. The Target is PAYLOAD_TYPE_JSON,
	// so the handler verifies the HMAC ZITADEL-Signature with the Target's signing
	// key. Records the login with UserUseCase.RecordLogin and never fails login.
	loginEventHandler := webhook.NewLoginEventHandler(
		cfg.Webhook.LoginEventSigningKey,
		w.userUC,
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
		w.stripeWebhookSvc,
		logger,
	)

	// Passkey-event Actions v2 handler — bound to the event executions on
	// user.human.passwordless.token.verified and token.removed; starts
	// record-passkey-change for the product org's identities, which the
	// workflow worker runs.
	passkeyEventHandler := webhook.NewPasskeyEventHandler(
		cfg.Webhook.PasskeyEventSigningKey,
		cfg.Webhook.ProductOrgID,
		w.passkeyUC,
		logger,
	)

	// Password-setup-refusal Actions v2 handler — bound to the request
	// executions on the API methods that set the caller's own password; it
	// never answers 2xx, so Zitadel refuses them.
	passwordSetupRefusalHandler := webhook.NewPasswordSetupRefusalHandler(
		cfg.Webhook.PasswordSetupRefusalSigningKey,
		logger,
	)

	return server.NewWebhookServer(cfg.Webhook, logger, map[string]http.Handler{
		"/pre-access-token":      preAccessTokenHandler,
		"/account-login-event":   loginEventHandler,
		"/passkey-event":         passkeyEventHandler,
		"/refuse-password-setup": passwordSetupRefusalHandler,
		"/stripe-webhook":        stripeWebhookHandler,
	})
}

// newAPIWiring builds the repositories, the workflow client, the business
// metrics and the usecases of the API server.
func newAPIWiring(ctx context.Context, cfg *config.ServerConfig, db *rdb.Database, logger *logging.Logger) (*apiWiring, error) {
	w := &apiWiring{ctx: ctx, cfg: cfg, db: db, logger: logger, apiRepos: newAPIRepos(db, cfg, logger)}

	// Initialize the shutdown package for phased resource teardown.
	shutdown.Init(logger)

	// Business metrics
	w.businessMetrics = infratelemetry.NewBusinessMetrics()

	// Use Cases
	var err error
	w.workflows, err = provideAPIWorkflows(ctx, cfg, db, logger)
	if err != nil {
		return nil, err
	}
	for _, build := range []func() error{
		w.buildCatalogue, w.buildOrganizer, w.buildCardAuthorizations, w.buildEntries, w.buildTicketing, w.buildPayments,
		w.buildNotifications, w.buildAuth, w.buildServing,
	} {
		if err := build(); err != nil {
			return nil, err
		}
	}
	return w, nil
}

// assemble builds the servers and the webhook listener, registers the shutdown
// phases and returns the App.
func (w *apiWiring) assemble(telemetryCloser io.Closer) *App {
	ctx, cfg, logger := w.ctx, w.cfg, w.logger
	srv := w.consumerServer()
	adminSrv := w.adminServer()
	organizerSrv := w.organizerServer()
	receptionSrv := w.receptionServer()
	webhookSrv := w.webhookServer()

	// Register shutdown phases.
	// Drain: health → NOT_SERVING, then servers drain in-flight requests,
	// then cache cleanup goroutine stops.
	shutdown.AddDrainPhase(w.healthChecker, srv, adminSrv, organizerSrv, receptionSrv, webhookSrv, w.rateLimiter, w.artistCache)
	// jwtValidator owns the JWKS cache's refresh goroutines.
	shutdown.AddExternalPhase(append(w.externals, w.jwtValidator)...)
	shutdown.AddObservePhase(telemetryCloser)
	shutdown.AddDatastorePhase(w.db)

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
	}
}
