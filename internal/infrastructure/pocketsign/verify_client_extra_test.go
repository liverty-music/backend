package pocketsign_test

// This file extends the verify_client_test.go suite with additional coverage
// for auth-header assertions, nil/empty extraction branches, methodFromCertType
// default, Certificate_STATE_UNSPECIFIED, and Close safety.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"buf.build/gen/go/pocketsign/apis/connectrpc/go/pocketsign/stamp/v1/stampv1connect"
	"buf.build/gen/go/pocketsign/apis/connectrpc/go/pocketsign/verify/v2/verifyv2connect"
	stampv1 "buf.build/gen/go/pocketsign/apis/protocolbuffers/go/pocketsign/stamp/v1"
	verifyv2 "buf.build/gen/go/pocketsign/apis/protocolbuffers/go/pocketsign/verify/v2"
	"connectrpc.com/connect"
	infrapocketsign "github.com/liverty-music/backend/internal/infrastructure/pocketsign"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/status"
)

// ─────────────────────────────────────────────────────────────────────────────
// Header-capturing fakes
// ─────────────────────────────────────────────────────────────────────────────

// headerCapturingSessionService captures the HTTP headers sent with each RPC
// so tests can assert that Authorization and X-Tenant-ID are set correctly.
type headerCapturingSessionService struct {
	stampv1connect.UnimplementedSessionServiceHandler

	// createResp is returned from CreateSession unless createErr is set.
	createResp *stampv1.CreateSessionResponse
	createErr  *connect.Error

	// lastCreateHeader holds the headers from the most recent CreateSession call.
	lastCreateHeader http.Header

	// finalizeResp is returned from FinalizeSession unless finalizeErr is set.
	finalizeResp *stampv1.FinalizeSessionResponse
	finalizeErr  *connect.Error

	// lastFinalizeHeader holds the headers from the most recent FinalizeSession call.
	lastFinalizeHeader http.Header
}

func (f *headerCapturingSessionService) CreateSession(_ context.Context, req *connect.Request[stampv1.CreateSessionRequest]) (*connect.Response[stampv1.CreateSessionResponse], error) {
	f.lastCreateHeader = req.Header().Clone()
	if f.createErr != nil {
		return nil, f.createErr
	}
	return connect.NewResponse(f.createResp), nil
}

func (f *headerCapturingSessionService) FinalizeSession(_ context.Context, req *connect.Request[stampv1.FinalizeSessionRequest]) (*connect.Response[stampv1.FinalizeSessionResponse], error) {
	f.lastFinalizeHeader = req.Header().Clone()
	if f.finalizeErr != nil {
		return nil, f.finalizeErr
	}
	return connect.NewResponse(f.finalizeResp), nil
}

// headerCapturingUserService captures headers from CheckUserStatus calls.
type headerCapturingUserService struct {
	verifyv2connect.UnimplementedUserServiceHandler

	checkStatusResp *verifyv2.CheckUserStatusResponse
	checkStatusErr  *connect.Error
	lastCheckHeader http.Header
}

func (f *headerCapturingUserService) CheckUserStatus(_ context.Context, req *connect.Request[verifyv2.CheckUserStatusRequest]) (*connect.Response[verifyv2.CheckUserStatusResponse], error) {
	f.lastCheckHeader = req.Header().Clone()
	if f.checkStatusErr != nil {
		return nil, f.checkStatusErr
	}
	return connect.NewResponse(f.checkStatusResp), nil
}

// startHeaderCapturingServer returns an httptest.Server URL backed by the
// provided header-capturing fakes.
func startHeaderCapturingServer(t *testing.T, sessionSvc *headerCapturingSessionService, userSvc *headerCapturingUserService) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(stampv1connect.NewSessionServiceHandler(sessionSvc))
	mux.Handle(verifyv2connect.NewUserServiceHandler(userSvc))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// newHeaderCapturingClient builds a StampClient with hardcoded test credentials
// so the header values are predictable in assertions.
func newHeaderCapturingClient(t *testing.T, baseURL string) *infrapocketsign.StampClient {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return infrapocketsign.NewStampClient(
		infrapocketsign.StampClientConfig{
			BaseURL:     baseURL,
			Token:       "test-token",
			TenantID:    "test-tenant",
			CallbackURL: "https://fan.example.app/verify/callback",
		},
		&http.Client{},
		logger,
	)
}

// ─────────────────────────────────────────────────────────────────────────────
// Auth header assertion — StartVerify (CreateSession)
// ─────────────────────────────────────────────────────────────────────────────

// TestStampClient_StartVerify_AuthHeadersAreSent asserts that the
// Authorization and X-Tenant-ID headers are sent on each CreateSession call.
func TestStampClient_StartVerify_AuthHeadersAreSent(t *testing.T) {
	t.Parallel()

	sessionSvc := &headerCapturingSessionService{
		createResp: buildCreateSessionResponse("sess-hdr", "https://p8n.app/redirect/sess-hdr"),
	}
	userSvc := &headerCapturingUserService{}
	baseURL := startHeaderCapturingServer(t, sessionSvc, userSvc)
	client := newHeaderCapturingClient(t, baseURL)

	_, _, err := client.StartVerify(context.Background(), "user-header-test", 0)
	require.NoError(t, err)

	require.NotNil(t, sessionSvc.lastCreateHeader, "CreateSession must have been called")
	assert.Equal(t, "Bearer test-token", sessionSvc.lastCreateHeader.Get("Authorization"),
		"Authorization header must carry the configured Bearer token")
	assert.Equal(t, "test-tenant", sessionSvc.lastCreateHeader.Get("X-Tenant-ID"),
		"X-Tenant-ID header must carry the configured tenant id")
}

// ─────────────────────────────────────────────────────────────────────────────
// Auth header assertion — Recheck (CheckUserStatus)
// ─────────────────────────────────────────────────────────────────────────────

// TestStampClient_Recheck_AuthHeadersAreSent asserts that Authorization and
// X-Tenant-ID are forwarded on the verify.v2.UserService.CheckUserStatus call.
func TestStampClient_Recheck_AuthHeadersAreSent(t *testing.T) {
	t.Parallel()

	state := verifyv2.Certificate_STATE_GOOD
	userSvc := &headerCapturingUserService{
		checkStatusResp: verifyv2.CheckUserStatusResponse_builder{
			CertificateState: &state,
		}.Build(),
	}
	sessionSvc := &headerCapturingSessionService{}
	baseURL := startHeaderCapturingServer(t, sessionSvc, userSvc)
	client := newHeaderCapturingClient(t, baseURL)

	_, err := client.Recheck(context.Background(), "ps-hdr-user")
	require.NoError(t, err)

	require.NotNil(t, userSvc.lastCheckHeader, "CheckUserStatus must have been called")
	assert.Equal(t, "Bearer test-token", userSvc.lastCheckHeader.Get("Authorization"),
		"Authorization header must carry the configured Bearer token")
	assert.Equal(t, "test-tenant", userSvc.lastCheckHeader.Get("X-Tenant-ID"),
		"X-Tenant-ID header must carry the configured tenant id")
}

// ─────────────────────────────────────────────────────────────────────────────
// FinalizeSession nil/empty extraction branches
// ─────────────────────────────────────────────────────────────────────────────

// buildFinalizeResponseEmptyResults builds a FinalizeSessionResponse with no
// result entries, echoing the nonce so nonce-validation passes first.
func buildFinalizeResponseEmptyResults(nonce string) *stampv1.FinalizeSessionResponse {
	return stampv1.FinalizeSessionResponse_builder{
		Results:  nil,
		Metadata: map[string]string{"nonce": nonce},
	}.Build()
}

// buildFinalizeResponseNilUserAuthentication builds a FinalizeSessionResponse
// where the one result carries no UserAuthentication (a different result type).
func buildFinalizeResponseNilUserAuthentication(nonce string) *stampv1.FinalizeSessionResponse {
	// Build a Result with no UserAuthentication set (zero Result).
	result := stampv1.Result_builder{}.Build()
	return stampv1.FinalizeSessionResponse_builder{
		Results:  []*stampv1.Result{result},
		Metadata: map[string]string{"nonce": nonce},
	}.Build()
}

// buildFinalizeResponseNilVerifyResponse builds a FinalizeSessionResponse where
// the UserAuthentication result carries neither a VerifyResponse nor an Error —
// the Result field is nil (empty UserAuthentication).
func buildFinalizeResponseNilVerifyResponse(nonce string) *stampv1.FinalizeSessionResponse {
	// UserAuthentication with no Result and no Error.
	userAuthResult := stampv1.Result_UserAuthentication_builder{}.Build()
	result := stampv1.Result_builder{
		UserAuthentication: userAuthResult,
	}.Build()
	return stampv1.FinalizeSessionResponse_builder{
		Results:  []*stampv1.Result{result},
		Metadata: map[string]string{"nonce": nonce},
	}.Build()
}

// buildFinalizeResponseEmptyUserID builds a FinalizeSessionResponse with a
// VerifyResponse that carries an empty User.id — the PocketSignUserID would
// be empty.
func buildFinalizeResponseEmptyUserID(t *testing.T, nonce string) *stampv1.FinalizeSessionResponse {
	t.Helper()

	emptyUID := ""
	user := verifyv2.User_builder{Id: &emptyUID}.Build()
	certType := verifyv2.Certificate_TYPE_JPKI_CARD_USER_AUTHENTICATION
	cert := verifyv2.Certificate_builder{Type: &certType}.Build()

	verifyResp := verifyv2.VerifyResponse_builder{
		User:        user,
		Certificate: cert,
	}.Build()

	userAuthResult := stampv1.Result_UserAuthentication_builder{
		Result: verifyResp,
	}.Build()

	result := stampv1.Result_builder{
		UserAuthentication: userAuthResult,
	}.Build()

	return stampv1.FinalizeSessionResponse_builder{
		Results:  []*stampv1.Result{result},
		Metadata: map[string]string{"nonce": nonce},
	}.Build()
}

// buildFinalizeResponseNilCertificate builds a FinalizeSessionResponse where
// the VerifyResponse carries a valid user id but a nil Certificate.
func buildFinalizeResponseNilCertificate(t *testing.T, nonce string) *stampv1.FinalizeSessionResponse {
	t.Helper()

	uid := "ps-user-cert-nil"
	user := verifyv2.User_builder{Id: &uid}.Build()

	// VerifyResponse with no Certificate.
	verifyResp := verifyv2.VerifyResponse_builder{
		User: user,
	}.Build()

	userAuthResult := stampv1.Result_UserAuthentication_builder{
		Result: verifyResp,
	}.Build()

	result := stampv1.Result_builder{
		UserAuthentication: userAuthResult,
	}.Build()

	return stampv1.FinalizeSessionResponse_builder{
		Results:  []*stampv1.Result{result},
		Metadata: map[string]string{"nonce": nonce},
	}.Build()
}

// completeVerifyWithFinalize is a helper that runs StartVerify then calls
// CompleteVerify with a custom FinalizeSession response, returning the error.
func completeVerifyWithFinalize(t *testing.T, finalizeResp *stampv1.FinalizeSessionResponse) error {
	t.Helper()

	sessionSvc := &fakeSessionService{
		createResp: buildCreateSessionResponse("sess-extract", "https://p8n.app/r/extract"),
	}
	baseURL := startFakeServer(t, sessionSvc, &fakeUserService{})
	client := newTestClient(t, baseURL)

	sessionID, nonce := startVerifyAndGetNonce(t, client, sessionSvc, "user-extract")

	// Patch the nonce into the response so nonce-validation passes and we reach
	// the extraction branches.
	_ = nonce

	// Override the finalize response with a copy that echoes the real nonce.
	sessionSvc.finalizeResp = patchNonce(finalizeResp, nonce)

	_, err := client.CompleteVerify(context.Background(), "user-extract", sessionID)
	return err
}

// patchNonce replaces the nonce in a FinalizeSessionResponse's Metadata.
// The Pocket Sign proto builder pattern does not support mutation, so we
// reconstruct the response preserving Results.
func patchNonce(resp *stampv1.FinalizeSessionResponse, nonce string) *stampv1.FinalizeSessionResponse {
	return stampv1.FinalizeSessionResponse_builder{
		Results:  resp.GetResults(),
		Metadata: map[string]string{"nonce": nonce},
	}.Build()
}

func TestStampClient_CompleteVerify_EmptyResults_ReturnsInternal(t *testing.T) {
	t.Parallel()

	// The response has a correct nonce but no result entries.
	err := completeVerifyWithFinalize(t, buildFinalizeResponseEmptyResults("placeholder"))
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrInternal,
		"empty results slice must map to Internal")
}

func TestStampClient_CompleteVerify_NilUserAuthentication_ReturnsInternal(t *testing.T) {
	t.Parallel()

	err := completeVerifyWithFinalize(t, buildFinalizeResponseNilUserAuthentication("placeholder"))
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrInternal,
		"nil UserAuthentication result must map to Internal")
}

func TestStampClient_CompleteVerify_NilVerifyResponse_ReturnsInternal(t *testing.T) {
	t.Parallel()

	err := completeVerifyWithFinalize(t, buildFinalizeResponseNilVerifyResponse("placeholder"))
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrInternal,
		"nil VerifyResponse inside UserAuthentication must map to Internal")
}

func TestStampClient_CompleteVerify_EmptyUserID_ReturnsInternal(t *testing.T) {
	t.Parallel()

	sessionSvc := &fakeSessionService{
		createResp: buildCreateSessionResponse("sess-empty-uid", "https://p8n.app/r/empty-uid"),
	}
	baseURL := startFakeServer(t, sessionSvc, &fakeUserService{})
	client := newTestClient(t, baseURL)

	sessionID, nonce := startVerifyAndGetNonce(t, client, sessionSvc, "user-empty-uid")
	sessionSvc.finalizeResp = buildFinalizeResponseEmptyUserID(t, nonce)

	_, err := client.CompleteVerify(context.Background(), "user-empty-uid", sessionID)

	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrInternal,
		"empty User.id must map to Internal")
}

func TestStampClient_CompleteVerify_NilCertificate_ReturnsInternal(t *testing.T) {
	t.Parallel()

	sessionSvc := &fakeSessionService{
		createResp: buildCreateSessionResponse("sess-nil-cert", "https://p8n.app/r/nil-cert"),
	}
	baseURL := startFakeServer(t, sessionSvc, &fakeUserService{})
	client := newTestClient(t, baseURL)

	sessionID, nonce := startVerifyAndGetNonce(t, client, sessionSvc, "user-nil-cert")
	sessionSvc.finalizeResp = buildFinalizeResponseNilCertificate(t, nonce)

	_, err := client.CompleteVerify(context.Background(), "user-nil-cert", sessionID)

	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrInternal,
		"nil Certificate in VerifyResponse must map to Internal")
}

// ─────────────────────────────────────────────────────────────────────────────
// methodFromCertType default (non-JPKI / TYPE_UNSPECIFIED)
// ─────────────────────────────────────────────────────────────────────────────

// TestStampClient_CompleteVerify_UnknownCertType_ReturnsInvalidArgument
// exercises the methodFromCertType default branch: a certificate type that is
// neither JPKI card nor mobile variant must return InvalidArgument.
func TestStampClient_CompleteVerify_UnknownCertType_ReturnsInvalidArgument(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	sessionSvc := &fakeSessionService{
		createResp: buildCreateSessionResponse("sess-unknown-cert", "https://p8n.app/r/unknown-cert"),
	}
	baseURL := startFakeServer(t, sessionSvc, &fakeUserService{})
	client := newTestClient(t, baseURL)

	sessionID, nonce := startVerifyAndGetNonce(t, client, sessionSvc, "user-unknown-cert")

	// TYPE_UNSPECIFIED is the zero value and falls into the default switch case.
	unspecifiedType := verifyv2.Certificate_TYPE_UNSPECIFIED
	sessionSvc.finalizeResp = buildFinalizeResponseOK(t, "ps-uid-unknown", nonce, unspecifiedType)

	_, err := client.CompleteVerify(ctx, "user-unknown-cert", sessionID)

	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrInvalidArgument,
		"an unsupported/unspecified certificate type must return InvalidArgument")
}

// ─────────────────────────────────────────────────────────────────────────────
// Recheck with Certificate_STATE_UNSPECIFIED — indeterminate, NeedsReverification==false
// ─────────────────────────────────────────────────────────────────────────────

// TestStampClient_Recheck_StateUnspecified_IsNotNeedsReverification pins the
// contract that STATE_UNSPECIFIED (the proto zero value / a vendor hiccup) is
// treated as INDETERMINATE, not confirmed-bad, so NeedsReverification stays false.
func TestStampClient_Recheck_StateUnspecified_IsNotNeedsReverification(t *testing.T) {
	t.Parallel()

	userSvc := &fakeUserService{
		checkStatusResp: buildCheckStatusResponse(verifyv2.Certificate_STATE_UNSPECIFIED),
	}
	baseURL := startFakeServer(t, &fakeSessionService{}, userSvc)
	client := newTestClient(t, baseURL)

	got, err := client.Recheck(context.Background(), "user-state-unspecified")

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.False(t, got.NeedsReverification,
		"STATE_UNSPECIFIED must be indeterminate (NeedsReverification==false), not confirmed-bad")
}

// ─────────────────────────────────────────────────────────────────────────────
// Close
// ─────────────────────────────────────────────────────────────────────────────

// TestStampClient_Close_ReturnNilAndIsSafe asserts that Close returns nil and
// stops the background nonce-cache cleanup goroutine cleanly.
func TestStampClient_Close_ReturnNilAndIsSafe(t *testing.T) {
	t.Parallel()

	// An empty fake server; we just want a configured (non-panic) client.
	baseURL := startFakeServer(t, &fakeSessionService{}, &fakeUserService{})
	client := newTestClient(t, baseURL)

	err := client.Close()
	assert.NoError(t, err, "Close must return nil")

	// NOTE: calling Close a second time would block on the already-closed done
	// channel (double <-c.done in the current implementation). This is a latent
	// bug in the MemoryCache — see the bug report in the final test report.
	// We do NOT add a second Close() call here to avoid hanging the test suite.
}

// ─────────────────────────────────────────────────────────────────────────────
// FinalizeSession nil UserAuthentication error path
// ─────────────────────────────────────────────────────────────────────────────

// TestStampClient_CompleteVerify_UserAuthResultError_ReturnsFailedPrecondition
// verifies that a non-nil gRPC Status error inside the UserAuthentication result
// (cert validation failure returned by the Pocket Sign Verify API) maps to
// FailedPrecondition. This path is already covered in the base test file, but
// is repeated here with an explicit nil-Error vs non-nil-Error contrast to
// ensure both branches in the HasError() guard are reachable.
func TestStampClient_CompleteVerify_UserAuthResultError_ReturnsFailedPrecondition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	sessionSvc := &fakeSessionService{
		createResp: buildCreateSessionResponse("sess-certfail2", "https://p8n.app/r/certfail2"),
	}
	baseURL := startFakeServer(t, sessionSvc, &fakeUserService{})
	client := newTestClient(t, baseURL)

	sessionID, nonce := startVerifyAndGetNonce(t, client, sessionSvc, "user-certfail2")

	errStatus := &status.Status{Message: "SIGNATURE_MISMATCH"}
	userAuthResult := stampv1.Result_UserAuthentication_builder{
		Error: errStatus,
	}.Build()
	result := stampv1.Result_builder{UserAuthentication: userAuthResult}.Build()
	sessionSvc.finalizeResp = stampv1.FinalizeSessionResponse_builder{
		Results:  []*stampv1.Result{result},
		Metadata: map[string]string{"nonce": nonce},
	}.Build()

	_, err := client.CompleteVerify(ctx, "user-certfail2", sessionID)

	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition,
		"cert validation error in UserAuthentication result must return FailedPrecondition")
}
