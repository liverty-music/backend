package pocketsign_test

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
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/status"

	infrapocketsign "github.com/liverty-music/backend/internal/infrastructure/pocketsign"
)

// ────────────────────────────────────────────────────────────────────
// Fake Stamp SessionService
// ────────────────────────────────────────────────────────────────────

// fakeSessionService implements stampv1connect.SessionServiceHandler.
// Fields control what CreateSession and FinalizeSession return.
type fakeSessionService struct {
	stampv1connect.UnimplementedSessionServiceHandler

	// CreateSession response fields.
	createResp *stampv1.CreateSessionResponse
	createErr  *connect.Error

	// lastCreateReq captures the last CreateSession request for assertion.
	lastCreateReq *stampv1.CreateSessionRequest

	// FinalizeSession response fields.
	finalizeResp *stampv1.FinalizeSessionResponse
	finalizeErr  *connect.Error
}

func (f *fakeSessionService) CreateSession(_ context.Context, req *connect.Request[stampv1.CreateSessionRequest]) (*connect.Response[stampv1.CreateSessionResponse], error) {
	f.lastCreateReq = req.Msg
	if f.createErr != nil {
		return nil, f.createErr
	}
	return connect.NewResponse(f.createResp), nil
}

func (f *fakeSessionService) FinalizeSession(_ context.Context, _ *connect.Request[stampv1.FinalizeSessionRequest]) (*connect.Response[stampv1.FinalizeSessionResponse], error) {
	if f.finalizeErr != nil {
		return nil, f.finalizeErr
	}
	return connect.NewResponse(f.finalizeResp), nil
}

// ────────────────────────────────────────────────────────────────────
// Fake verify.v2 UserService (for Recheck)
// ────────────────────────────────────────────────────────────────────

// fakeUserService implements verifyv2connect.UserServiceHandler for Recheck tests.
type fakeUserService struct {
	verifyv2connect.UnimplementedUserServiceHandler
	checkStatusResp *verifyv2.CheckUserStatusResponse
	checkStatusErr  *connect.Error
}

func (f *fakeUserService) CheckUserStatus(_ context.Context, _ *connect.Request[verifyv2.CheckUserStatusRequest]) (*connect.Response[verifyv2.CheckUserStatusResponse], error) {
	if f.checkStatusErr != nil {
		return nil, f.checkStatusErr
	}
	return connect.NewResponse(f.checkStatusResp), nil
}

// ────────────────────────────────────────────────────────────────────
// Test server + client helpers
// ────────────────────────────────────────────────────────────────────

// startFakeServer starts an httptest.Server with the provided fake service
// handlers. The server is shut down via t.Cleanup.
func startFakeServer(t *testing.T, sessionSvc *fakeSessionService, userSvc *fakeUserService) string {
	t.Helper()

	mux := http.NewServeMux()
	mux.Handle(stampv1connect.NewSessionServiceHandler(sessionSvc))
	mux.Handle(verifyv2connect.NewUserServiceHandler(userSvc))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// newTestClient constructs a StampClient pointed at the given fake server URL.
func newTestClient(t *testing.T, baseURL string) *infrapocketsign.StampClient {
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

// buildCreateSessionResponse builds a minimal CreateSessionResponse.
func buildCreateSessionResponse(id, redirectURL string) *stampv1.CreateSessionResponse {
	return stampv1.CreateSessionResponse_builder{
		Id:          &id,
		RedirectUrl: &redirectURL,
	}.Build()
}

// buildFinalizeResponseOK builds a FinalizeSessionResponse representing a
// successful userAuthentication result. The nonce is echoed in metadata so the
// client's nonce validation passes.
//
// 利用者ID field path (confirmed via go doc):
//
//	FinalizeSessionResponse.Results[0]
//	  .GetUserAuthentication()        → *Result_UserAuthentication
//	  .GetResult()                    → *verifyv2.VerifyResponse
//	  .GetUser()                      → *verifyv2.User
//	  .GetId()                        → string  ← PocketSignUserID
func buildFinalizeResponseOK(t *testing.T, pocketSignUserID, nonce string, certType verifyv2.Certificate_Type) *stampv1.FinalizeSessionResponse {
	t.Helper()

	uid := pocketSignUserID
	user := verifyv2.User_builder{Id: &uid}.Build()
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

// buildFinalizeResponseError builds a FinalizeSessionResponse where the
// userAuthentication result carries a gRPC Status error (cert validation failure
// from the Pocket Sign Verify API side).
func buildFinalizeResponseError(t *testing.T, nonce, msg string) *stampv1.FinalizeSessionResponse {
	t.Helper()

	errStatus := &status.Status{Message: msg}
	userAuthResult := stampv1.Result_UserAuthentication_builder{
		Error: errStatus,
	}.Build()

	result := stampv1.Result_builder{
		UserAuthentication: userAuthResult,
	}.Build()

	return stampv1.FinalizeSessionResponse_builder{
		Results:  []*stampv1.Result{result},
		Metadata: map[string]string{"nonce": nonce},
	}.Build()
}

// buildCheckStatusResponse builds a CheckUserStatusResponse with the given state.
func buildCheckStatusResponse(state verifyv2.Certificate_State) *verifyv2.CheckUserStatusResponse {
	return verifyv2.CheckUserStatusResponse_builder{
		CertificateState: &state,
	}.Build()
}

// startVerifyAndGetNonce calls StartVerify and returns the nonce that was
// embedded in the CreateSession request metadata, so FinalizeSession tests can
// echo the correct nonce back.
func startVerifyAndGetNonce(t *testing.T, client *infrapocketsign.StampClient, sessionSvc *fakeSessionService, userID string) (sessionID string, nonce string) {
	t.Helper()
	sid, _, err := client.StartVerify(context.Background(), userID, 0)
	require.NoError(t, err)
	require.NotEmpty(t, sid)
	return sid, sessionSvc.lastCreateReq.GetMetadata()["nonce"]
}

// ────────────────────────────────────────────────────────────────────
// StartVerify tests
// ────────────────────────────────────────────────────────────────────

func TestStampClient_StartVerify(t *testing.T) {
	t.Parallel()

	const (
		wantSessionID   = "session-xyz"
		wantRedirectURL = "https://stamp.p8n.app/session/session-xyz"
		userID          = "user-1"
	)

	sessionSvc := &fakeSessionService{
		createResp: buildCreateSessionResponse(wantSessionID, wantRedirectURL),
	}
	baseURL := startFakeServer(t, sessionSvc, &fakeUserService{})
	client := newTestClient(t, baseURL)

	gotSession, gotURL, err := client.StartVerify(context.Background(), userID, 0)

	require.NoError(t, err)
	assert.Equal(t, wantSessionID, gotSession, "session id must match response")
	assert.Equal(t, wantRedirectURL, gotURL, "redirect url must match response")

	// Assert the request shape sent to the fake server.
	req := sessionSvc.lastCreateReq
	require.NotNil(t, req, "CreateSession must have been called")
	assert.Equal(t, "https://fan.example.app/verify/callback", req.GetCallbackUrl(),
		"callback url must be the configured value")

	require.Len(t, req.GetRequests(), 1, "exactly one request item expected")
	item := req.GetRequests()[0]
	assert.True(t, item.GetRequired(), "request item must have required=true")
	assert.NotNil(t, item.GetUserAuthentication(), "request must be userAuthentication type")
	assert.True(t, item.GetUserAuthentication().GetIdentifyUser(),
		"identifyUser must be true for 当人認証 → stable 利用者ID")

	nonce := req.GetMetadata()["nonce"]
	assert.NotEmpty(t, nonce, "metadata must carry a non-empty nonce for security binding")
}

func TestStampClient_StartVerify_UniqueNonces(t *testing.T) {
	t.Parallel()

	sessionSvc := &fakeSessionService{
		createResp: buildCreateSessionResponse("s1", "https://p8n.app/redirect/s1"),
	}
	baseURL := startFakeServer(t, sessionSvc, &fakeUserService{})
	client := newTestClient(t, baseURL)
	ctx := context.Background()

	_, _, err := client.StartVerify(ctx, "user-1", 0)
	require.NoError(t, err)
	nonce1 := sessionSvc.lastCreateReq.GetMetadata()["nonce"]

	sessionSvc.createResp = buildCreateSessionResponse("s2", "https://p8n.app/redirect/s2")
	_, _, err = client.StartVerify(ctx, "user-1", 0)
	require.NoError(t, err)
	nonce2 := sessionSvc.lastCreateReq.GetMetadata()["nonce"]

	assert.NotEqual(t, nonce1, nonce2, "consecutive nonces must be distinct")
}

func TestStampClient_StartVerify_VendorUnauthenticated_MapsToInternal(t *testing.T) {
	t.Parallel()

	sessionSvc := &fakeSessionService{
		createErr: connect.NewError(connect.CodeUnauthenticated, nil),
	}
	baseURL := startFakeServer(t, sessionSvc, &fakeUserService{})
	client := newTestClient(t, baseURL)

	_, _, err := client.StartVerify(context.Background(), "user-1", 0)
	assert.ErrorIs(t, err, apperr.ErrInternal,
		"Unauthenticated from vendor maps to Internal (our token is wrong, non-retryable)")
}

// ────────────────────────────────────────────────────────────────────
// CompleteVerify tests
// ────────────────────────────────────────────────────────────────────

func TestStampClient_CompleteVerify_ExtractsPocketSignUserID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const userID = "user-1"

	tests := []struct {
		name              string
		certType          verifyv2.Certificate_Type
		wantPocketSignUID string
	}{
		{
			name:              "extract 利用者ID from JPKI_CARD_USER_AUTHENTICATION",
			certType:          verifyv2.Certificate_TYPE_JPKI_CARD_USER_AUTHENTICATION,
			wantPocketSignUID: "pocketsign-user-001",
		},
		{
			name:              "extract 利用者ID from JPKI_MOBILE_USER_AUTHENTICATION",
			certType:          verifyv2.Certificate_TYPE_JPKI_MOBILE_USER_AUTHENTICATION,
			wantPocketSignUID: "pocketsign-user-002",
		},
		{
			name:              "extract 利用者ID from JPKI_CARD_DIGITAL_SIGNATURE",
			certType:          verifyv2.Certificate_TYPE_JPKI_CARD_DIGITAL_SIGNATURE,
			wantPocketSignUID: "pocketsign-user-003",
		},
		{
			name:              "extract 利用者ID from JPKI_MOBILE_DIGITAL_SIGNATURE",
			certType:          verifyv2.Certificate_TYPE_JPKI_MOBILE_DIGITAL_SIGNATURE,
			wantPocketSignUID: "pocketsign-user-004",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sessionSvc := &fakeSessionService{
				createResp: buildCreateSessionResponse("session-ok", "https://p8n.app/redirect"),
			}
			baseURL := startFakeServer(t, sessionSvc, &fakeUserService{})
			client := newTestClient(t, baseURL)

			sessionID, nonce := startVerifyAndGetNonce(t, client, sessionSvc, userID)
			sessionSvc.finalizeResp = buildFinalizeResponseOK(t, tt.wantPocketSignUID, nonce, tt.certType)

			got, err := client.CompleteVerify(ctx, userID, sessionID)

			require.NoError(t, err)
			require.NotNil(t, got)
			// The 利用者ID is extracted via:
			//   result.GetUserAuthentication().GetResult().GetUser().GetId()
			assert.Equal(t, tt.wantPocketSignUID, got.PocketSignUserID)
		})
	}
}

func TestStampClient_CompleteVerify_NonceMismatch_ReturnsPermissionDenied(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const userID = "user-1"

	sessionSvc := &fakeSessionService{
		createResp: buildCreateSessionResponse("sess-tamper", "https://p8n.app/redirect"),
	}
	baseURL := startFakeServer(t, sessionSvc, &fakeUserService{})
	client := newTestClient(t, baseURL)

	sessionID, _ := startVerifyAndGetNonce(t, client, sessionSvc, userID)

	// FinalizeSession echoes a wrong nonce — simulates a session-swap / tamper.
	sessionSvc.finalizeResp = buildFinalizeResponseOK(t, "ps-user-999", "wrong-nonce-injected",
		verifyv2.Certificate_TYPE_JPKI_CARD_USER_AUTHENTICATION)

	_, err := client.CompleteVerify(ctx, userID, sessionID)
	assert.ErrorIs(t, err, apperr.ErrPermissionDenied,
		"nonce mismatch must return PermissionDenied")
}

func TestStampClient_CompleteVerify_CertValidationFailure_ReturnsFailedPrecondition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const userID = "user-1"

	sessionSvc := &fakeSessionService{
		createResp: buildCreateSessionResponse("sess-certfail", "https://p8n.app/redirect"),
	}
	baseURL := startFakeServer(t, sessionSvc, &fakeUserService{})
	client := newTestClient(t, baseURL)

	sessionID, nonce := startVerifyAndGetNonce(t, client, sessionSvc, userID)
	// FinalizeSession result carries a status Error (vendor-side cert failure).
	sessionSvc.finalizeResp = buildFinalizeResponseError(t, nonce, "CERTIFICATE_REVOKED")

	_, err := client.CompleteVerify(ctx, userID, sessionID)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition,
		"cert validation failure in result must return FailedPrecondition")
}

func TestStampClient_CompleteVerify_SessionNotCompleted_ReturnsFailedPreconditionAndIsRetryable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const userID = "user-1"

	sessionSvc := &fakeSessionService{
		createResp: buildCreateSessionResponse("sess-notdone", "https://p8n.app/redirect"),
		// FinalizeSession returns FailedPrecondition — fan has not finished yet.
		finalizeErr: connect.NewError(connect.CodeFailedPrecondition, nil),
	}
	baseURL := startFakeServer(t, sessionSvc, &fakeUserService{})
	client := newTestClient(t, baseURL)

	sessionID, nonce := startVerifyAndGetNonce(t, client, sessionSvc, userID)

	// First call: session not completed yet.
	_, err := client.CompleteVerify(ctx, userID, sessionID)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition,
		"session not yet completed must return FailedPrecondition")

	// The nonce must be restored so the caller can retry once the fan finishes.
	sessionSvc.finalizeErr = nil
	sessionSvc.finalizeResp = buildFinalizeResponseOK(t, "ps-user-retry", nonce,
		verifyv2.Certificate_TYPE_JPKI_CARD_USER_AUTHENTICATION)

	got, retryErr := client.CompleteVerify(ctx, userID, sessionID)
	require.NoError(t, retryErr, "retry after SESSION_NOT_COMPLETED must succeed")
	assert.Equal(t, "ps-user-retry", got.PocketSignUserID)
}

func TestStampClient_CompleteVerify_UnknownSession_ReturnsFailedPrecondition(t *testing.T) {
	t.Parallel()

	// Call CompleteVerify without a preceding StartVerify so the nonce is absent.
	baseURL := startFakeServer(t, &fakeSessionService{}, &fakeUserService{})
	client := newTestClient(t, baseURL)

	_, err := client.CompleteVerify(context.Background(), "user-1", "no-such-session")
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition,
		"unknown/expired session must return FailedPrecondition")
}

func TestStampClient_CompleteVerify_SessionConsumedAfterSuccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const userID = "user-1"

	sessionSvc := &fakeSessionService{
		createResp: buildCreateSessionResponse("sess-consume", "https://p8n.app/redirect"),
	}
	baseURL := startFakeServer(t, sessionSvc, &fakeUserService{})
	client := newTestClient(t, baseURL)

	sessionID, nonce := startVerifyAndGetNonce(t, client, sessionSvc, userID)
	sessionSvc.finalizeResp = buildFinalizeResponseOK(t, "ps-user-999", nonce,
		verifyv2.Certificate_TYPE_JPKI_CARD_USER_AUTHENTICATION)

	// First call must succeed.
	_, err := client.CompleteVerify(ctx, userID, sessionID)
	require.NoError(t, err)

	// Second call with the same session id must be rejected — nonce consumed.
	_, err = client.CompleteVerify(ctx, userID, sessionID)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition,
		"replayed session id must be rejected after successful completion")
}

// ────────────────────────────────────────────────────────────────────
// Recheck tests
// ────────────────────────────────────────────────────────────────────

func TestStampClient_Recheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		args             struct{ pocketSignUserID string }
		fakeSvcResp      *verifyv2.CheckUserStatusResponse
		fakeSvcErr       *connect.Error
		wantNeedsRecheck bool
		wantErr          error
	}{
		{
			name:             "return NeedsReverification=false when certificate state is GOOD",
			args:             struct{ pocketSignUserID string }{pocketSignUserID: "user-good"},
			fakeSvcResp:      buildCheckStatusResponse(verifyv2.Certificate_STATE_GOOD),
			wantNeedsRecheck: false,
		},
		{
			name:             "return NeedsReverification=true when certificate state is REVOKED",
			args:             struct{ pocketSignUserID string }{pocketSignUserID: "user-revoked"},
			fakeSvcResp:      buildCheckStatusResponse(verifyv2.Certificate_STATE_REVOKED),
			wantNeedsRecheck: true,
		},
		{
			name:             "return NeedsReverification=true when certificate state is EXPIRED",
			args:             struct{ pocketSignUserID string }{pocketSignUserID: "user-expired"},
			fakeSvcResp:      buildCheckStatusResponse(verifyv2.Certificate_STATE_EXPIRED),
			wantNeedsRecheck: true,
		},
		{
			name:             "return NeedsReverification=false when certificate state is UNKNOWN (indeterminate, not confirmed-bad)",
			args:             struct{ pocketSignUserID string }{pocketSignUserID: "user-unknown"},
			fakeSvcResp:      buildCheckStatusResponse(verifyv2.Certificate_STATE_UNKNOWN),
			wantNeedsRecheck: false,
		},
		{
			name:       "return Internal when vendor returns Unauthenticated (our token misconfigured, non-retryable)",
			args:       struct{ pocketSignUserID string }{pocketSignUserID: "user-xyz"},
			fakeSvcErr: connect.NewError(connect.CodeUnauthenticated, nil),
			wantErr:    apperr.ErrInternal,
		},
		{
			name:       "return Unavailable when vendor is unreachable",
			args:       struct{ pocketSignUserID string }{pocketSignUserID: "user-xyz"},
			fakeSvcErr: connect.NewError(connect.CodeUnavailable, nil),
			wantErr:    apperr.ErrUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			userSvc := &fakeUserService{
				checkStatusResp: tt.fakeSvcResp,
				checkStatusErr:  tt.fakeSvcErr,
			}
			baseURL := startFakeServer(t, &fakeSessionService{}, userSvc)
			client := newTestClient(t, baseURL)

			got, err := client.Recheck(context.Background(), tt.args.pocketSignUserID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			assert.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tt.wantNeedsRecheck, got.NeedsReverification)
		})
	}
}

// ────────────────────────────────────────────────────────────────────
// StampClientConfig.IsConfigured tests
// ────────────────────────────────────────────────────────────────────

func TestStampClientConfig_IsConfigured(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args infrapocketsign.StampClientConfig
		want bool
	}{
		{
			name: "return true when all four fields are set",
			args: infrapocketsign.StampClientConfig{
				BaseURL:     "https://verify.test.p8n.app",
				Token:       "tok",
				TenantID:    "tenant-1",
				CallbackURL: "https://fan.example.app/verify/callback",
			},
			want: true,
		},
		{
			name: "return false when BaseURL is empty",
			args: infrapocketsign.StampClientConfig{
				Token: "tok", TenantID: "t", CallbackURL: "https://x.example.com/cb",
			},
			want: false,
		},
		{
			name: "return false when Token is empty",
			args: infrapocketsign.StampClientConfig{
				BaseURL: "https://verify.test.p8n.app", TenantID: "t", CallbackURL: "https://x.example.com/cb",
			},
			want: false,
		},
		{
			name: "return false when TenantID is empty",
			args: infrapocketsign.StampClientConfig{
				BaseURL: "https://verify.test.p8n.app", Token: "tok", CallbackURL: "https://x.example.com/cb",
			},
			want: false,
		},
		{
			name: "return false when CallbackURL is empty",
			args: infrapocketsign.StampClientConfig{
				BaseURL: "https://verify.test.p8n.app", Token: "tok", TenantID: "t",
			},
			want: false,
		},
		{
			name: "return false when all are empty",
			args: infrapocketsign.StampClientConfig{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.args.IsConfigured())
		})
	}
}
