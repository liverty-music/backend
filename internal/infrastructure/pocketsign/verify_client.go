// Package pocketsign provides the Pocket Sign Verify API integration for the
// identity eKYC flow.
//
// The package contains two implementations of usecase.PocketSignVerifier:
//
//   - [VerifyClient]: the real HTTP client that calls the Pocket Sign Verify API
//     (selected when POCKET_SIGN_BASE_URL and POCKET_SIGN_TOKEN are configured).
//   - [StubVerifier]: a no-op placeholder that returns UNAVAILABLE on every call
//     (selected when the configuration is absent — the default for local dev).
//
// # Pocket Sign flow model (デジタル認証アプリ variant)
//
// The backend uses the "デジタル認証アプリ" signing flow:
//
//  1. IssueChallenge — generates a local opaque session id + random nonce.
//     The nonce is stored in a short-TTL in-memory cache keyed by session id.
//     No Pocket Sign API call is made here.
//
//  2. ValidateResponse — the client returns the SDK-produced JWE blob
//     (sign_certificate_jwe) and the original session id. The backend looks up
//     the stored nonce, computes data = base64(sha256(nonce)), and calls
//     VerificationService.VerifyForDigitalIdentificationApp.
//
//  3. Recheck — calls UserService.CheckUserStatus for 現況確認.
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
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"buf.build/gen/go/pocketsign/apis/connectrpc/go/pocketsign/verify/v2/verifyv2connect"
	verifyv2 "buf.build/gen/go/pocketsign/apis/protocolbuffers/go/pocketsign/verify/v2"
	"connectrpc.com/connect"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/cache"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time assertion that VerifyClient satisfies usecase.PocketSignVerifier.
var _ usecase.PocketSignVerifier = (*VerifyClient)(nil)

// challengeTTL is how long a pending nonce is retained. The デジタル認証アプリ
// signing flow is interactive and completes within a single user session, so
// 10 minutes is generous. Entries are evicted automatically by MemoryCache.
const challengeTTL = 10 * time.Minute

// VerifyClientConfig holds the credentials and endpoint for the Pocket Sign
// Verify API. Both BaseURL and Token must be non-empty for the real client to
// be selected; when either is absent the DI layer falls back to StubVerifier.
type VerifyClientConfig struct {
	// BaseURL is the Pocket Sign Verify API base URL, e.g.
	//   - Mock:            https://verify.mock.p8n.app
	//   - Test (sandbox):  https://verify.test.p8n.app
	//   - Production:      https://verify.p8n.app
	//
	// Note: even the mock environment requires a valid Bearer token.
	BaseURL string

	// Token is the Bearer token issued by the Pocket Sign Platform Verify
	// tenant screen. Must be kept in a secret — never embed in source.
	Token string
}

// IsConfigured returns true when both BaseURL and Token are non-empty,
// indicating that the real client should be used instead of the stub.
func (c VerifyClientConfig) IsConfigured() bool {
	return c.BaseURL != "" && c.Token != ""
}

// VerifyClient is the real implementation of usecase.PocketSignVerifier that
// calls the Pocket Sign Verify API via Connect-RPC.
//
// The client uses the デジタル認証アプリ verification flow. See package-level
// documentation for the full sequence.
type VerifyClient struct {
	verificationSvc verifyv2connect.VerificationServiceClient
	userSvc         verifyv2connect.UserServiceClient
	// nonceCache maps sessionID → hex-encoded nonce ([]byte as string).
	// Entries expire after challengeTTL.
	//
	// TODO: replace with a shared store (Redis/DB) before multi-instance prod.
	nonceCache *cache.MemoryCache
	logger     *logging.Logger
}

// NewVerifyClient creates a VerifyClient that authenticates to the Pocket Sign
// Verify API using the provided Bearer token. It panics if cfg.IsConfigured()
// is false — callers should guard with that check before constructing.
func NewVerifyClient(cfg VerifyClientConfig, httpClient *http.Client, logger *logging.Logger) *VerifyClient {
	if !cfg.IsConfigured() {
		panic("pocketsign.NewVerifyClient: BaseURL and Token must both be set")
	}

	bearerInterceptor := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			req.Header().Set("Authorization", "Bearer "+cfg.Token)
			return next(ctx, req)
		}
	})
	opts := connect.WithInterceptors(bearerInterceptor)

	return &VerifyClient{
		verificationSvc: verifyv2connect.NewVerificationServiceClient(httpClient, cfg.BaseURL, opts),
		userSvc:         verifyv2connect.NewUserServiceClient(httpClient, cfg.BaseURL, opts),
		nonceCache:      cache.NewMemoryCache(challengeTTL),
		logger:          logger,
	}
}

// Close releases the client's resources — specifically it stops the nonce
// cache's background cleanup goroutine. Wire it into the shutdown drain phase
// (like the other MemoryCache instances) so graceful shutdown is complete and
// no goroutine leaks. Safe to call once.
func (c *VerifyClient) Close() error {
	return c.nonceCache.Close()
}

// IssueChallenge generates a local opaque session id and random nonce.
//
// No Pocket Sign API call is made. The nonce is stored in a short-TTL
// in-memory cache keyed by the session id so ValidateResponse can retrieve it.
//
// The returned PocketSignChallenge carries:
//   - SessionID: opaque handle the client must echo back in CompleteVerify.
//   - Challenge: random nonce bytes the デジタル認証アプリ signs over
//     (passed to the signing-transaction-start API as `data`).
//
// Possible errors:
//   - Unavailable: random source failure (extremely rare).
func (c *VerifyClient) IssueChallenge(_ context.Context, _ entity.VerificationMethod) (*usecase.PocketSignChallenge, error) {
	sessionBytes, err := randomBytes(16)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Unavailable, "failed to generate session id")
	}
	sessionID := hex.EncodeToString(sessionBytes)

	// Generate a 32-byte nonce. The client passes it to the デジタル認証アプリ
	// signing-transaction-start API as `data`; on return the signed JWE and
	// this nonce's digest are submitted together to Pocket Sign Verify.
	nonceBytes, err := randomBytes(32)
	if err != nil {
		return nil, apperr.Wrap(err, codes.Unavailable, "failed to generate nonce")
	}

	// Store the raw nonce bytes in the short-TTL cache so ValidateResponse can
	// look them up (TTL = challengeTTL, 10 min). Stored as []byte directly —
	// MemoryCache holds `any`, so no encode/decode round-trip is needed.
	c.nonceCache.Set(sessionID, nonceBytes)

	return &usecase.PocketSignChallenge{
		SessionID: sessionID,
		Challenge: nonceBytes,
	}, nil
}

// ValidateResponse calls VerificationService.VerifyForDigitalIdentificationApp
// using:
//   - sign_certificate_jwe = string(signedResponse) — the JWE the デジタル認証アプリ
//     returns via the signing-transaction-result API.
//   - data = base64(sha256(nonce)) — where nonce was issued by IssueChallenge for
//     the given sessionID and stored in the in-memory cache.
//
// On success it returns the Pocket Sign user id and the verification method
// derived from the certificate type.
//
// Possible errors:
//   - InvalidArgument: sessionID unknown/expired, JWE malformed, signature invalid,
//     certificate unsupported, or response contains no user id.
//   - FailedPrecondition: certificate revoked or expired.
//   - Unavailable: Pocket Sign API unreachable, authentication failed, or
//     any other vendor-side error.
func (c *VerifyClient) ValidateResponse(ctx context.Context, sessionID string, signedResponse []byte) (*usecase.PocketSignResult, error) {
	// Atomically look up AND consume the stored nonce in a single locked op so
	// two concurrent calls with the same session id cannot both proceed (a
	// separate Get+Delete would leave a replay race window). Unknown/expired
	// (or already-consumed) sessions are rejected.
	rawNonce := c.nonceCache.GetAndDelete(sessionID)
	if rawNonce == nil {
		return nil, apperr.New(codes.InvalidArgument,
			"unknown or expired session id; the challenge may have timed out (10 min limit)")
	}
	nonceBytes, ok := rawNonce.([]byte)
	if !ok {
		return nil, apperr.New(codes.Internal, "internal nonce cache type mismatch")
	}

	// data is the value the デジタル認証アプリ signing-transaction-start API was
	// given (the signed-over material). It MUST equal what the app actually
	// signed, or VerifyForDigitalIdentificationApp rejects every request.
	//
	// WARNING (unverified): the exact form — base64(sha256(nonce)) vs the raw
	// nonce, standard vs URL base64 — is NOT yet confirmed against the live
	// signing-transaction-start contract (the frontend flow is unbuilt). This
	// MUST be validated against verify.test.p8n.app with a real signature
	// before trusting; the frontend and this transform must agree exactly.
	digest := sha256.Sum256(nonceBytes)
	data := base64.StdEncoding.EncodeToString(digest[:])

	jwe := string(signedResponse)
	checkMethod := verifyv2.CertificateStatus_CHECK_METHOD_CRL
	identifyUser := true

	req := connect.NewRequest(verifyv2.VerifyForDigitalIdentificationAppRequest_builder{
		SignCertificateJwe: &jwe,
		Data:               &data,
		CheckMethod:        &checkMethod,
		IdentifyUser:       &identifyUser,
	}.Build())

	resp, err := c.verificationSvc.VerifyForDigitalIdentificationApp(ctx, req)
	if err != nil {
		return nil, mapConnectError(err, "verify pocket sign response")
	}

	body := resp.Msg

	// Extract the Pocket Sign User.id. identify_user=true guarantees it is
	// populated for any successful response with a JPKI certificate.
	user := body.GetUser()
	if user == nil || user.GetId() == "" {
		return nil, apperr.New(codes.InvalidArgument,
			"pocket sign verify response contained no user id; "+
				"ensure identify_user=true and that the certificate was issued more than 90 minutes ago")
	}

	// GetCertificate is nil-safe (a nil *Certificate would yield TYPE_UNSPECIFIED
	// and a misleading "unsupported certificate type" error), so surface a
	// missing certificate explicitly instead.
	cert := body.GetCertificate()
	if cert == nil {
		return nil, apperr.New(codes.InvalidArgument,
			"pocket sign verify response contained no certificate")
	}
	certType := cert.GetType()
	method, err := methodFromCertType(certType)
	if err != nil {
		return nil, err
	}

	c.logger.Info(ctx, "pocket sign verification successful",
		slog.String("session_id", sessionID),
		slog.String("pocket_sign_user_id", user.GetId()),
		slog.Bool("is_new_user", body.GetIsNewUser()),
		slog.String("cert_type", certType.String()),
	)

	return &usecase.PocketSignResult{
		PocketSignUserID: user.GetId(),
		Method:           method,
	}, nil
}

// Recheck calls UserService.CheckUserStatus to perform 現況確認 for the given
// Pocket Sign user id. NeedsReverification is true when the certificate is
// anything other than STATE_GOOD (revoked, expired, or unknown state).
//
// Possible errors:
//   - Unavailable: Pocket Sign API unreachable, authentication failed, or
//     any other vendor-side error.
func (c *VerifyClient) Recheck(ctx context.Context, pocketSignUserID string) (*usecase.PocketSignRecheckResult, error) {
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
	// re-verification. STATE_UNKNOWN (not revoked, but the validity window could
	// not be confirmed via CRL) and STATE_UNSPECIFIED (unset/zero — a possible
	// vendor hiccup) are INDETERMINATE, not confirmed-bad: flagging them would
	// force spurious re-KYC on an otherwise-valid user for a transient
	// lookup failure. Leave those as not-needing-reverification and log.
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

// mapConnectError translates a Connect-RPC error from the Pocket Sign API into
// an apperr with an appropriate code:
//
//   - CodeInvalidArgument → codes.InvalidArgument (bad JWE, unsupported cert, …)
//   - CodeFailedPrecondition → codes.FailedPrecondition (revoked/expired cert)
//   - CodeUnauthenticated → codes.Unavailable (wrong Bearer token — config error)
//   - all other codes → codes.Unavailable (treat vendor errors as transient)
func mapConnectError(err error, op string) error {
	code := connect.CodeOf(err)
	switch code {
	case connect.CodeInvalidArgument:
		return apperr.Wrap(err, codes.InvalidArgument, op+": invalid or unsupported request")
	case connect.CodeFailedPrecondition:
		return apperr.Wrap(err, codes.FailedPrecondition, op+": certificate revoked or expired")
	case connect.CodeUnauthenticated:
		// 401 from Pocket Sign means OUR Bearer token is wrong/expired/rotated —
		// a server-side misconfiguration, not an end-user auth problem and NOT
		// transient. Map to Internal (non-retryable, HTTP 500) so retry/circuit
		// logic and alerting treat it as a config error to fix, not a blip to
		// retry forever. The end-user still sees a generic error, never "401".
		return apperr.Wrap(err, codes.Internal, op+": pocket sign API authentication failed; check POCKET_SIGN_TOKEN")
	default:
		return apperr.Wrap(err, codes.Unavailable, op+": pocket sign API unavailable")
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
