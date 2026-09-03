// Package pocketsign provides the Pocket Sign Stamp API integration for the
// identity eKYC flow.
//
// The package contains two implementations of usecase.PocketSignVerifier:
//
//   - [StampClient]: the real HTTP client that calls the Pocket Sign Stamp API
//     (selected when POCKET_SIGN_BASE_URL, POCKET_SIGN_TOKEN, POCKET_SIGN_TENANT_ID,
//     and POCKET_SIGN_CALLBACK_URL are all configured).
//   - [StubVerifier]: a no-op placeholder that returns UNAVAILABLE on every call
//     (selected when the configuration is absent — the default for local dev).
//
// # Pocket Sign Stamp flow model
//
// Our client is a PWA, so it uses PocketSign Stamp rather than an embedded Verify
// SDK. The Stamp flow delegates card reading to the separately-installed PocketSign
// app:
//
//  1. StartVerify — calls SessionService.CreateSession with a userAuthentication
//     request (identifyUser=true, required=true) and a random nonce embedded in
//     metadata. The nonce is stored server-side keyed by (userID, sessionID) in a
//     short-TTL in-memory cache. Returns (sessionID, redirectURL).
//     The client opens redirectURL in the PocketSign app; the fan reads their
//     card there, and the app redirects back to the callback URL with
//     ?session_id=<id>.
//
//  2. CompleteVerify — the backend receives the callback (session_id), calls
//     SessionService.FinalizeSession(id). Before a successful fan session,
//     FinalizeSession returns FailedPrecondition with
//     ERROR_REASON_SESSION_NOT_COMPLETED. On success:
//     a. Compares metadata.nonce in the response against the cached nonce;
//     mismatch → PermissionDenied, delete the cache entry.
//     b. Checks the userAuthentication result's VerifyResponse; a non-OK cert
//     (SIGNATURE_MISMATCH, CERTIFICATE_REVOKED, etc.) → FailedPrecondition.
//     c. Extracts User.id from result.GetUserAuthentication().GetResult().GetUser().GetId()
//     → our PocketSignUserID.
//
//  3. Recheck — calls verify.v2.UserService.CheckUserStatus for 現況確認.
//
// # Security
//
// The metadata.nonce REPLACES the old hand-rolled nonce+sha256+`data` approach.
// The nonce is stored in the cache keyed by (userID + ":" + sessionID) and
// consumed atomically on CompleteVerify to prevent replay attacks.
//
// # TODO
//
//   - Replace the in-process MemoryCache with a shared store (Redis / DB)
//     before running multiple replicas in production. The single-process cache
//     works for dev/staging single-pod deployments.
package pocketsign

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"buf.build/gen/go/pocketsign/apis/connectrpc/go/pocketsign/stamp/v1/stampv1connect"
	"buf.build/gen/go/pocketsign/apis/connectrpc/go/pocketsign/verify/v2/verifyv2connect"
	stampv1 "buf.build/gen/go/pocketsign/apis/protocolbuffers/go/pocketsign/stamp/v1"
	verifyv2 "buf.build/gen/go/pocketsign/apis/protocolbuffers/go/pocketsign/verify/v2"
	"connectrpc.com/connect"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/api"
	"github.com/liverty-music/backend/pkg/cache"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that StampClient satisfies usecase.PocketSignVerifier.
var _ usecase.PocketSignVerifier = (*StampClient)(nil)

// sessionTTL is how long a pending nonce is retained. The Stamp flow is
// interactive and requires the fan to open the PocketSign app, read their card,
// and return to the PWA. 10 minutes is generous for this interaction.
// Entries are evicted automatically by MemoryCache.
const sessionTTL = 10 * time.Minute

// nonceCacheKey returns the cache key for the nonce stored during StartVerify.
// Keyed by both userID and sessionID so a session can only be finalized by the
// user who initiated it.
func nonceCacheKey(userID, sessionID string) string {
	return userID + ":" + sessionID
}

// StampClientConfig holds the credentials and endpoint for the Pocket Sign
// Stamp API. All four fields must be non-empty for the real client to be
// selected; when any is absent the DI layer falls back to StubVerifier.
type StampClientConfig struct {
	// BaseURL is the Pocket Sign Stamp API base URL, e.g.
	//   - Mock:            https://verify.mock.p8n.app
	//   - Test (sandbox):  https://verify.test.p8n.app
	//   - Production:      https://verify.p8n.app
	BaseURL string

	// Token is the Bearer token issued by the Pocket Sign Platform Stamp
	// tenant screen. Must be kept in a secret — never embed in source.
	Token string

	// TenantID is the tenant identifier sent as the X-Tenant-ID request header
	// on every Stamp API call.
	TenantID string

	// CallbackURL is the frontend URL the PocketSign app redirects to after the
	// fan completes card reading (e.g. https://fan.dev.liverty-music.app/verify/callback).
	// Sent to SessionService.CreateSession as callback_url.
	CallbackURL string
}

// IsConfigured returns true when all four fields are non-empty, indicating that
// the real client should be used instead of the stub.
func (c StampClientConfig) IsConfigured() bool {
	return c.BaseURL != "" && c.Token != "" && c.TenantID != "" && c.CallbackURL != ""
}

// StampClient is the real implementation of usecase.PocketSignVerifier that
// calls the Pocket Sign Stamp API via Connect-RPC.
//
// The client uses the PocketSign Stamp flow (SessionService.CreateSession →
// fan reads card in PocketSign app → SessionService.FinalizeSession). See
// package-level documentation for the full sequence.
type StampClient struct {
	sessionSvc stampv1connect.SessionServiceClient
	userSvc    verifyv2connect.UserServiceClient
	cfg        StampClientConfig
	// nonceCache maps nonceCacheKey(userID, sessionID) → hex-encoded nonce.
	// Entries expire after sessionTTL.
	//
	// TODO: replace with a shared store (Redis/DB) before multi-instance prod.
	nonceCache *cache.MemoryCache
	logger     *logging.Logger
}

// NewStampClient creates a StampClient that authenticates to the Pocket Sign
// Stamp API using the provided Bearer token and Tenant ID. It panics if
// cfg.IsConfigured() is false — callers should guard with that check before
// constructing.
func NewStampClient(cfg StampClientConfig, httpClient *http.Client, logger *logging.Logger) *StampClient {
	if !cfg.IsConfigured() {
		panic("pocketsign.NewStampClient: BaseURL, Token, TenantID, and CallbackURL must all be set")
	}

	// Both Authorization and X-Tenant-ID are required by the Stamp API on
	// every request.
	authInterceptor := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			req.Header().Set("Authorization", "Bearer "+cfg.Token)
			req.Header().Set("X-Tenant-ID", cfg.TenantID)
			return next(ctx, req)
		}
	})
	opts := connect.WithInterceptors(authInterceptor)

	return &StampClient{
		sessionSvc: stampv1connect.NewSessionServiceClient(httpClient, cfg.BaseURL, opts),
		userSvc:    verifyv2connect.NewUserServiceClient(httpClient, cfg.BaseURL, opts),
		cfg:        cfg,
		nonceCache: cache.NewMemoryCache(sessionTTL),
		logger:     logger,
	}
}

// Close releases the client's resources — specifically it stops the nonce
// cache's background cleanup goroutine. Wire it into the shutdown drain phase
// (like the other MemoryCache instances) so graceful shutdown is complete and
// no goroutine leaks. Safe to call once.
func (c *StampClient) Close() error {
	return c.nonceCache.Close()
}

// StartVerify calls SessionService.CreateSession to create a Pocket Sign Stamp
// session.
//
// A random 32-byte nonce is generated and stored in the short-TTL cache keyed
// by nonceCacheKey(userID, sessionID) so CompleteVerify can later compare it
// against the metadata returned by FinalizeSession.
//
// Possible errors:
//   - Unavailable: random source failure (extremely rare) or Pocket Sign API
//     unreachable / authentication failed.
func (c *StampClient) StartVerify(ctx context.Context, userID string, _ entity.VerificationMethod) (string, string, error) {
	// Generate a 32-byte nonce. It is embedded in session metadata so
	// FinalizeSession echoes it back, allowing the backend to detect tampering.
	nonceBytes, err := randomBytes(32)
	if err != nil {
		return "", "", apperr.Wrap(err, codes.Unavailable, "failed to generate nonce")
	}
	nonce := hex.EncodeToString(nonceBytes)

	// MVP: userAuthentication with identifyUser=true (当人認証 → stable 利用者ID).
	// DO NOT set readPersonalInfo — we never retrieve 基本4情報 in the MVP.
	identifyUser := true
	checkMethod := verifyv2.CertificateStatus_CHECK_METHOD_CRL
	required := true

	// Session-return configuration, matching the official PocketSign Stamp
	// reference implementation (github.com/pocketsign/stamp-example src/index.tsx):
	//   - requireManualReturn=true: after signing in the PocketSign app, the user
	//     manually returns to the ORIGINAL browser tab rather than relying on an
	//     app→browser auto deep-link (which is unreliable and can land in a
	//     different browser context). Our frontend persists the session id in that
	//     origin's storage, so the user must return to it for CompleteVerify.
	//   - callbackWithSessionId=false: the callback URL carries NO session_id query
	//     param; the frontend reads the session id from its own persisted storage
	//     (the reference sample uses a cookie for the same purpose). This is why the
	//     frontend MUST NOT depend on a `?session_id` query parameter.
	// Anti-replay/anti-CSRF is enforced by the metadata nonce compared at
	// FinalizeSession (see docs.p8n.app/docs/stamp/guide/identification/security).
	requireManualReturn := true
	callbackWithSessionID := false

	req := connect.NewRequest(stampv1.CreateSessionRequest_builder{
		CallbackUrl: &c.cfg.CallbackURL,
		Requests: []*stampv1.Request{
			stampv1.Request_builder{
				Required: &required,
				UserAuthentication: stampv1.Request_UserAuthentication_builder{
					IdentifyUser:      &identifyUser,
					StatusCheckMethod: &checkMethod,
				}.Build(),
			}.Build(),
		},
		Metadata: map[string]string{
			"nonce": nonce,
		},
		RequireManualReturn:   &requireManualReturn,
		CallbackWithSessionId: &callbackWithSessionID,
	}.Build())

	resp, err := c.sessionSvc.CreateSession(ctx, req)
	if err != nil {
		return "", "", mapConnectError(err, "pocket sign create session")
	}

	sessionID := resp.Msg.GetId()
	redirectURL := resp.Msg.GetRedirectUrl()

	if sessionID == "" {
		return "", "", apperr.New(codes.Internal, "pocket sign create session returned empty session id")
	}
	if redirectURL == "" {
		return "", "", apperr.New(codes.Internal, "pocket sign create session returned empty redirect url")
	}

	// Store the nonce keyed by (userID, sessionID) so CompleteVerify can
	// atomically retrieve and validate it.
	c.nonceCache.Set(nonceCacheKey(userID, sessionID), nonce)

	c.logger.Info(ctx, "pocket sign stamp session created",
		slog.String("user_id", userID),
		slog.String("session_id", sessionID),
	)

	return sessionID, redirectURL, nil
}

// CompleteVerify calls SessionService.FinalizeSession to complete the Stamp
// session and extract the verified user identity.
//
// The session must have been completed by the fan in the PocketSign app. If
// not, FinalizeSession returns FailedPrecondition with
// ERROR_REASON_SESSION_NOT_COMPLETED, which is mapped to FailedPrecondition.
//
// Security: the nonce stored at StartVerify is compared against the nonce
// echoed back in the FinalizeSession response metadata. A mismatch causes a
// PermissionDenied error and the cache entry is deleted immediately.
//
// Possible errors:
//   - FailedPrecondition: session not yet completed by the fan, or cert failed
//     validation (revoked/expired/signature mismatch).
//   - PermissionDenied: nonce mismatch (replay or tamper attempt).
//   - Unavailable: Pocket Sign Stamp API unreachable or authentication failed.
func (c *StampClient) CompleteVerify(ctx context.Context, userID, sessionID string) (*usecase.PocketSignResult, error) {
	// Atomically look up AND consume the stored nonce. Unknown/expired
	// (or already-consumed) sessions are rejected with FailedPrecondition to
	// match the "session not completed" category — the most likely cause is a
	// timeout, not a tamper attempt.
	raw := c.nonceCache.GetAndDelete(nonceCacheKey(userID, sessionID))
	if raw == nil {
		return nil, apperr.New(codes.FailedPrecondition,
			"unknown or expired session; the session may have timed out (10 min limit) or was already completed")
	}
	expectedNonce, ok := raw.(string)
	if !ok {
		return nil, apperr.New(codes.Internal, "internal nonce cache type mismatch")
	}

	req := connect.NewRequest(stampv1.FinalizeSessionRequest_builder{
		Id: &sessionID,
	}.Build())

	resp, err := c.sessionSvc.FinalizeSession(ctx, req)
	if err != nil {
		// Put the nonce back if FinalizeSession failed so the client can
		// retry (e.g. if the fan has not finished yet — the caller will
		// poll and retry). Only restore for a transient "not completed"
		// condition; other errors are final and the nonce should stay consumed.
		connectCode := connect.CodeOf(err)
		if connectCode == connect.CodeFailedPrecondition {
			// The fan has not finished in the PocketSign app yet. Restore the
			// nonce so the caller can retry after the fan completes.
			c.nonceCache.Set(nonceCacheKey(userID, sessionID), expectedNonce)
			return nil, apperr.Wrap(err, codes.FailedPrecondition,
				"pocket sign session not yet completed; the fan must finish in the PocketSign app before calling CompleteVerify")
		}
		return nil, mapConnectError(err, "pocket sign finalize session")
	}

	// Validate the nonce returned in the session metadata against the one we
	// stored at CreateSession. This prevents a replay or session-swap attack
	// where an attacker substitutes their session_id for the victim's.
	metadata := resp.Msg.GetMetadata()
	gotNonce := metadata["nonce"]
	if gotNonce != expectedNonce {
		c.logger.Warn(ctx, "pocket sign nonce mismatch; possible replay or tamper",
			slog.String("user_id", userID),
			slog.String("session_id", sessionID),
		)
		return nil, apperr.New(codes.PermissionDenied,
			"pocket sign session nonce mismatch; this request has been rejected for security")
	}

	// Extract the userAuthentication result from the FinalizeSession response.
	// MVP: we request exactly one result (userAuthentication), so results[0] is
	// our result. Validate defensively.
	results := resp.Msg.GetResults()
	if len(results) == 0 {
		return nil, apperr.New(codes.Internal, "pocket sign finalize session returned no results")
	}

	userAuthResult := results[0].GetUserAuthentication()
	if userAuthResult == nil {
		return nil, apperr.New(codes.Internal,
			"pocket sign finalize session result is not a userAuthentication result; "+
				"ensure the session was created with a UserAuthentication request")
	}

	// A non-nil Error in the result means the cert failed verification on the
	// Pocket Sign side (e.g. SIGNATURE_MISMATCH, CERTIFICATE_REVOKED,
	// CERTIFICATE_EXPIRED). Map these to FailedPrecondition.
	if userAuthResult.HasError() && userAuthResult.GetError() != nil {
		errStatus := userAuthResult.GetError()
		return nil, apperr.New(codes.FailedPrecondition,
			fmt.Sprintf("pocket sign certificate verification failed: %s", errStatus.GetMessage()))
	}

	// GetResult() returns the *verifyv2.VerifyResponse on success. The
	// PocketSignUserID is carried in User.id (verify.v2.User.id), accessed via:
	//   result.GetUserAuthentication().GetResult().GetUser().GetId()
	verifyResp := userAuthResult.GetResult()
	if verifyResp == nil {
		return nil, apperr.New(codes.Internal,
			"pocket sign finalize session returned no verify response in userAuthentication result")
	}

	user := verifyResp.GetUser()
	if user == nil || user.GetId() == "" {
		return nil, apperr.New(codes.Internal,
			"pocket sign verify response contained no user id; "+
				"ensure identifyUser=true was set on the UserAuthentication request")
	}

	cert := verifyResp.GetCertificate()
	if cert == nil {
		return nil, apperr.New(codes.Internal,
			"pocket sign verify response contained no certificate")
	}
	method, err := methodFromCertType(cert.GetType())
	if err != nil {
		return nil, err
	}

	c.logger.Info(ctx, "pocket sign stamp session finalized",
		slog.String("user_id", userID),
		slog.String("session_id", sessionID),
		slog.String("pocket_sign_user_id", user.GetId()),
		slog.Bool("is_new_user", verifyResp.GetIsNewUser()),
		slog.String("cert_type", cert.GetType().String()),
	)

	return &usecase.PocketSignResult{
		PocketSignUserID: user.GetId(),
		Method:           method,
	}, nil
}

// Recheck calls UserService.CheckUserStatus to perform 現況確認 for the given
// Pocket Sign user id. NeedsReverification is true when the certificate is
// REVOKED or EXPIRED (not for UNKNOWN/UNSPECIFIED — indeterminate stays false).
//
// Possible errors:
//   - Unavailable: Pocket Sign API unreachable, authentication failed, or
//     any other vendor-side error.
func (c *StampClient) Recheck(ctx context.Context, pocketSignUserID string) (*usecase.PocketSignRecheckResult, error) {
	checkMethod := verifyv2.CertificateStatus_CHECK_METHOD_CRL

	req := connect.NewRequest(verifyv2.CheckUserStatusRequest_builder{
		UserId:      &pocketSignUserID,
		CheckMethod: &checkMethod,
	}.Build())

	resp, err := c.userSvc.CheckUserStatus(ctx, req)
	if err != nil {
		return nil, mapConnectError(err, "pocket sign recheck")
	}

	// Only a CONFIRMED bad state (revoked / expired) flags the identity for
	// re-verification. STATE_UNKNOWN (not revoked, but the validity window
	// could not be confirmed via CRL) and STATE_UNSPECIFIED (unset/zero — a
	// possible vendor hiccup) are INDETERMINATE, not confirmed-bad: flagging
	// them would force spurious re-KYC on an otherwise-valid user for a
	// transient lookup failure. Leave those as not-needing-reverification
	// and log.
	state := resp.Msg.GetCertificateState()
	var needsReverification bool
	switch state {
	case verifyv2.Certificate_STATE_REVOKED, verifyv2.Certificate_STATE_EXPIRED:
		needsReverification = true
	case verifyv2.Certificate_STATE_GOOD:
		needsReverification = false
	default:
		// STATE_UNKNOWN / STATE_UNSPECIFIED — indeterminate.
		needsReverification = false
		c.logger.Warn(ctx, "pocket sign recheck returned indeterminate state; not flagging reverification",
			slog.String("pocket_sign_user_id", pocketSignUserID),
			slog.String("certificate_state", state.String()),
		)
	}

	c.logger.Info(ctx, "pocket sign recheck completed",
		slog.String("pocket_sign_user_id", pocketSignUserID),
		slog.String("certificate_state", state.String()),
		slog.Bool("needs_reverification", needsReverification),
	)

	return &usecase.PocketSignRecheckResult{
		NeedsReverification: needsReverification,
	}, nil
}

// methodFromCertType maps a Pocket Sign Certificate_Type to our entity's
// VerificationMethod. All four JPKI variants map to VerificationMethodJPKI.
// An unsupported or unspecified type returns InvalidArgument.
func methodFromCertType(t verifyv2.Certificate_Type) (entity.VerificationMethod, error) {
	switch t {
	case verifyv2.Certificate_TYPE_JPKI_CARD_DIGITAL_SIGNATURE,
		verifyv2.Certificate_TYPE_JPKI_CARD_USER_AUTHENTICATION,
		verifyv2.Certificate_TYPE_JPKI_MOBILE_DIGITAL_SIGNATURE,
		verifyv2.Certificate_TYPE_JPKI_MOBILE_USER_AUTHENTICATION:
		return entity.VerificationMethodJPKI, nil
	default:
		return entity.VerificationMethodUnspecified,
			apperr.New(codes.InvalidArgument,
				fmt.Sprintf("unsupported Pocket Sign certificate type %q; only JPKI (card/mobile) is supported", t))
	}
}

// mapConnectError converts a Connect-RPC error from the Pocket Sign API into a
// structured apperr, delegating the status→code classification to the shared
// [api.FromStatus] policy so Pocket Sign classifies IDENTICALLY to every other
// SDK/raw-HTTP caller (Stripe, etc.) instead of hand-rolling a divergent mapping
// that silently collapses 403/404/429 into a generic code. The Connect code is
// first converted to its canonical HTTP status.
func mapConnectError(err error, op string) error {
	if appErr := api.FromStatus(connectCodeToHTTPStatus(connect.CodeOf(err)), err, "pocket sign: "+op); appErr != nil {
		return appErr
	}
	// FromStatus only returns nil for a 2xx status, which connectCodeToHTTPStatus
	// never produces for an error code; guard defensively.
	return apperr.Wrap(err, codes.Internal, "pocket sign: "+op)
}

// connectCodeToHTTPStatus maps a Connect/gRPC status code to the HTTP status the
// shared [api.FromStatus] policy classifies. This is the canonical code→status
// table (not a business policy — that lives in api.FromStatus). Codes without a
// distinct status the policy recognises (Unavailable/Internal/Unknown/…) map to
// 500, which the policy treats as Unavailable — preserving the prior "generic
// vendor error → Unavailable" behaviour.
func connectCodeToHTTPStatus(code connect.Code) int {
	switch code {
	case connect.CodeInvalidArgument, connect.CodeOutOfRange:
		return http.StatusBadRequest
	case connect.CodeFailedPrecondition:
		return http.StatusPreconditionFailed
	case connect.CodeUnauthenticated:
		return http.StatusUnauthorized
	case connect.CodePermissionDenied:
		return http.StatusForbidden
	case connect.CodeNotFound:
		return http.StatusNotFound
	case connect.CodeAlreadyExists, connect.CodeAborted:
		return http.StatusConflict
	case connect.CodeResourceExhausted:
		return http.StatusTooManyRequests
	case connect.CodeDeadlineExceeded:
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}

// randomBytes returns n cryptographically-random bytes.
func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("read random bytes: %w", err)
	}
	return b, nil
}
