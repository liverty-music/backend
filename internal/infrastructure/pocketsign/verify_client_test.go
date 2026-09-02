package pocketsign_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"buf.build/gen/go/pocketsign/apis/connectrpc/go/pocketsign/verify/v2/verifyv2connect"
	verifyv2 "buf.build/gen/go/pocketsign/apis/protocolbuffers/go/pocketsign/verify/v2"
	"connectrpc.com/connect"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	infrapocketsign "github.com/liverty-music/backend/internal/infrastructure/pocketsign"
	"github.com/liverty-music/backend/internal/usecase"
)

// fakeVerificationService implements verifyv2connect.VerificationServiceHandler
// for use in httptest.Server-based unit tests.
type fakeVerificationService struct {
	verifyForDigitalIDAppResp *verifyv2.VerifyForDigitalIdentificationAppResponse
	verifyForDigitalIDAppErr  *connect.Error
}

func (f *fakeVerificationService) Verify(_ context.Context, _ *connect.Request[verifyv2.VerifyRequest]) (*connect.Response[verifyv2.VerifyResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (f *fakeVerificationService) VerifyForDigitalIdentificationApp(_ context.Context, _ *connect.Request[verifyv2.VerifyForDigitalIdentificationAppRequest]) (*connect.Response[verifyv2.VerifyForDigitalIdentificationAppResponse], error) {
	if f.verifyForDigitalIDAppErr != nil {
		return nil, f.verifyForDigitalIDAppErr
	}
	return connect.NewResponse(f.verifyForDigitalIDAppResp), nil
}

func (f *fakeVerificationService) GetVerification(_ context.Context, _ *connect.Request[verifyv2.GetVerificationRequest]) (*connect.Response[verifyv2.GetVerificationResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (f *fakeVerificationService) ListVerifications(_ context.Context, _ *connect.Request[verifyv2.ListVerificationsRequest]) (*connect.Response[verifyv2.ListVerificationsResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (f *fakeVerificationService) DeleteVerification(_ context.Context, _ *connect.Request[verifyv2.DeleteVerificationRequest]) (*connect.Response[verifyv2.DeleteVerificationResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

// fakeUserService implements verifyv2connect.UserServiceHandler.
type fakeUserService struct {
	checkStatusResp *verifyv2.CheckUserStatusResponse
	checkStatusErr  *connect.Error
}

func (f *fakeUserService) GetUser(_ context.Context, _ *connect.Request[verifyv2.GetUserRequest]) (*connect.Response[verifyv2.GetUserResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (f *fakeUserService) GetUserInNamespace(_ context.Context, _ *connect.Request[verifyv2.GetUserInNamespaceRequest]) (*connect.Response[verifyv2.GetUserInNamespaceResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (f *fakeUserService) ListUsers(_ context.Context, _ *connect.Request[verifyv2.ListUsersRequest]) (*connect.Response[verifyv2.ListUsersResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (f *fakeUserService) DeleteUser(_ context.Context, _ *connect.Request[verifyv2.DeleteUserRequest]) (*connect.Response[verifyv2.DeleteUserResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (f *fakeUserService) GetUserCertificateSerialNumberMAC(_ context.Context, _ *connect.Request[verifyv2.GetUserCertificateSerialNumberMACRequest]) (*connect.Response[verifyv2.GetUserCertificateSerialNumberMACResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (f *fakeUserService) CheckUserStatus(_ context.Context, _ *connect.Request[verifyv2.CheckUserStatusRequest]) (*connect.Response[verifyv2.CheckUserStatusResponse], error) {
	if f.checkStatusErr != nil {
		return nil, f.checkStatusErr
	}
	return connect.NewResponse(f.checkStatusResp), nil
}

func (f *fakeUserService) BatchCheckUserStatus(_ context.Context, _ *connect.Request[verifyv2.BatchCheckUserStatusRequest]) (*connect.Response[verifyv2.BatchCheckUserStatusResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (f *fakeUserService) FetchLatestUserCertificateContent(_ context.Context, _ *connect.Request[verifyv2.FetchLatestUserCertificateContentRequest]) (*connect.Response[verifyv2.FetchLatestUserCertificateContentResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

// startFakeServer starts an httptest.Server with the provided fake service
// handlers. The server is shut down via t.Cleanup.
func startFakeServer(t *testing.T, verifySvc *fakeVerificationService, userSvc *fakeUserService) string {
	t.Helper()

	mux := http.NewServeMux()
	mux.Handle(verifyv2connect.NewVerificationServiceHandler(verifySvc))
	mux.Handle(verifyv2connect.NewUserServiceHandler(userSvc))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// newTestClient constructs a VerifyClient pointed at the given fake server URL.
func newTestClient(t *testing.T, baseURL string) *infrapocketsign.VerifyClient {
	t.Helper()

	logger, err := logging.New()
	require.NoError(t, err)

	return infrapocketsign.NewVerifyClient(
		infrapocketsign.VerifyClientConfig{
			BaseURL: baseURL,
			Token:   "test-token",
		},
		&http.Client{},
		logger,
	)
}

// buildDigitalIDAppResponse constructs a VerifyForDigitalIdentificationAppResponse
// with the given user id, certificate type, and is_new_user flag.
func buildDigitalIDAppResponse(t *testing.T, userID string, certType verifyv2.Certificate_Type, isNewUser bool) *verifyv2.VerifyForDigitalIdentificationAppResponse {
	t.Helper()

	uid := userID
	user := verifyv2.User_builder{Id: &uid}.Build()
	cert := verifyv2.Certificate_builder{Type: &certType}.Build()

	return verifyv2.VerifyForDigitalIdentificationAppResponse_builder{
		User:        user,
		Certificate: cert,
		IsNewUser:   &isNewUser,
	}.Build()
}

// buildCheckStatusResponse builds a CheckUserStatusResponse with the given state.
func buildCheckStatusResponse(state verifyv2.Certificate_State) *verifyv2.CheckUserStatusResponse {
	return verifyv2.CheckUserStatusResponse_builder{
		CertificateState: &state,
	}.Build()
}

// issueAndGetSessionID is a helper that calls IssueChallenge and returns the
// session id so ValidateResponse tests can pass a known session.
func issueAndGetSessionID(t *testing.T, client *infrapocketsign.VerifyClient) string {
	t.Helper()
	challenge, err := client.IssueChallenge(context.Background(), entity.VerificationMethodJPKI)
	require.NoError(t, err)
	require.NotEmpty(t, challenge.SessionID)
	return challenge.SessionID
}

// --- IssueChallenge tests ---

func TestVerifyClient_IssueChallenge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    struct{ method entity.VerificationMethod }
		wantErr error
	}{
		{
			name:    "return non-empty session id and challenge for JPKI method",
			args:    struct{ method entity.VerificationMethod }{method: entity.VerificationMethodJPKI},
			wantErr: nil,
		},
		{
			name:    "return non-empty session id and challenge for unspecified method",
			args:    struct{ method entity.VerificationMethod }{method: entity.VerificationMethodUnspecified},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := newTestClient(t, startFakeServer(t, &fakeVerificationService{}, &fakeUserService{}))

			got, err := client.IssueChallenge(context.Background(), tt.args.method)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			assert.NoError(t, err)
			require.NotNil(t, got)
			assert.NotEmpty(t, got.SessionID, "SessionID must not be empty")
			assert.NotEmpty(t, got.Challenge, "Challenge must not be empty")
			assert.Len(t, got.Challenge, 32, "nonce must be 32 bytes")
		})
	}
}

func TestVerifyClient_IssueChallenge_UniqueSessionIDs(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, startFakeServer(t, &fakeVerificationService{}, &fakeUserService{}))

	first, err := client.IssueChallenge(context.Background(), entity.VerificationMethodJPKI)
	require.NoError(t, err)

	second, err := client.IssueChallenge(context.Background(), entity.VerificationMethodJPKI)
	require.NoError(t, err)

	assert.NotEqual(t, first.SessionID, second.SessionID, "consecutive session ids must be distinct")
	assert.NotEqual(t, first.Challenge, second.Challenge, "consecutive nonces must be distinct")
}

// --- ValidateResponse tests ---

func TestVerifyClient_ValidateResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		certType   verifyv2.Certificate_Type
		fakeSvcErr *connect.Error
		want       *usecase.PocketSignResult
		wantErr    error
	}{
		{
			name:     "map JPKI_CARD_DIGITAL_SIGNATURE to VerificationMethodJPKI",
			certType: verifyv2.Certificate_TYPE_JPKI_CARD_DIGITAL_SIGNATURE,
			want: &usecase.PocketSignResult{
				PocketSignUserID: "user-001",
				Method:           entity.VerificationMethodJPKI,
			},
		},
		{
			name:     "map JPKI_CARD_USER_AUTHENTICATION to VerificationMethodJPKI",
			certType: verifyv2.Certificate_TYPE_JPKI_CARD_USER_AUTHENTICATION,
			want: &usecase.PocketSignResult{
				PocketSignUserID: "user-001",
				Method:           entity.VerificationMethodJPKI,
			},
		},
		{
			name:     "map JPKI_MOBILE_DIGITAL_SIGNATURE to VerificationMethodJPKI",
			certType: verifyv2.Certificate_TYPE_JPKI_MOBILE_DIGITAL_SIGNATURE,
			want: &usecase.PocketSignResult{
				PocketSignUserID: "user-001",
				Method:           entity.VerificationMethodJPKI,
			},
		},
		{
			name:     "map JPKI_MOBILE_USER_AUTHENTICATION to VerificationMethodJPKI",
			certType: verifyv2.Certificate_TYPE_JPKI_MOBILE_USER_AUTHENTICATION,
			want: &usecase.PocketSignResult{
				PocketSignUserID: "user-001",
				Method:           entity.VerificationMethodJPKI,
			},
		},
		{
			name:       "return InvalidArgument when vendor returns InvalidArgument",
			certType:   verifyv2.Certificate_TYPE_JPKI_CARD_DIGITAL_SIGNATURE,
			fakeSvcErr: connect.NewError(connect.CodeInvalidArgument, nil),
			wantErr:    apperr.ErrInvalidArgument,
		},
		{
			name:       "return FailedPrecondition when vendor returns FailedPrecondition",
			certType:   verifyv2.Certificate_TYPE_JPKI_CARD_DIGITAL_SIGNATURE,
			fakeSvcErr: connect.NewError(connect.CodeFailedPrecondition, nil),
			wantErr:    apperr.ErrFailedPrecondition,
		},
		{
			name:       "return Unavailable when vendor returns Unauthenticated",
			certType:   verifyv2.Certificate_TYPE_JPKI_CARD_DIGITAL_SIGNATURE,
			fakeSvcErr: connect.NewError(connect.CodeUnauthenticated, nil),
			wantErr:    apperr.ErrUnavailable,
		},
		{
			name:       "return Unavailable when vendor is unreachable",
			certType:   verifyv2.Certificate_TYPE_JPKI_CARD_DIGITAL_SIGNATURE,
			fakeSvcErr: connect.NewError(connect.CodeUnavailable, nil),
			wantErr:    apperr.ErrUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var svcResp *verifyv2.VerifyForDigitalIdentificationAppResponse
			if tt.fakeSvcErr == nil {
				svcResp = buildDigitalIDAppResponse(t, "user-001", tt.certType, false)
			}

			verifySvc := &fakeVerificationService{
				verifyForDigitalIDAppResp: svcResp,
				verifyForDigitalIDAppErr:  tt.fakeSvcErr,
			}
			baseURL := startFakeServer(t, verifySvc, &fakeUserService{})
			client := newTestClient(t, baseURL)

			// Always issue a challenge first so the nonce is stored in cache.
			sessionID := issueAndGetSessionID(t, client)

			got, err := client.ValidateResponse(context.Background(), sessionID, []byte("fake-jwe-blob"))

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			assert.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tt.want.PocketSignUserID, got.PocketSignUserID)
			assert.Equal(t, tt.want.Method, got.Method)
		})
	}
}

func TestVerifyClient_ValidateResponse_UnknownSessionID(t *testing.T) {
	t.Parallel()

	baseURL := startFakeServer(t, &fakeVerificationService{}, &fakeUserService{})
	client := newTestClient(t, baseURL)

	// Call ValidateResponse WITHOUT a preceding IssueChallenge.
	_, err := client.ValidateResponse(context.Background(), "no-such-session", []byte("jwe"))

	assert.ErrorIs(t, err, apperr.ErrInvalidArgument,
		"unknown session id must return InvalidArgument")
}

func TestVerifyClient_ValidateResponse_SessionConsumedOnUse(t *testing.T) {
	t.Parallel()

	// A session id must be consumed after the first ValidateResponse call
	// to prevent replay attacks.
	svcResp := buildDigitalIDAppResponse(t, "user-999", verifyv2.Certificate_TYPE_JPKI_CARD_DIGITAL_SIGNATURE, false)
	verifySvc := &fakeVerificationService{verifyForDigitalIDAppResp: svcResp}
	baseURL := startFakeServer(t, verifySvc, &fakeUserService{})
	client := newTestClient(t, baseURL)

	sessionID := issueAndGetSessionID(t, client)

	// First call must succeed.
	_, err := client.ValidateResponse(context.Background(), sessionID, []byte("jwe"))
	require.NoError(t, err)

	// Second call with the same session id must fail with InvalidArgument.
	_, err = client.ValidateResponse(context.Background(), sessionID, []byte("jwe"))
	assert.ErrorIs(t, err, apperr.ErrInvalidArgument,
		"replayed session id must be rejected")
}

// --- Recheck tests ---

func TestVerifyClient_Recheck(t *testing.T) {
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
			name:             "return NeedsReverification=true when certificate state is UNKNOWN",
			args:             struct{ pocketSignUserID string }{pocketSignUserID: "user-unknown"},
			fakeSvcResp:      buildCheckStatusResponse(verifyv2.Certificate_STATE_UNKNOWN),
			wantNeedsRecheck: true,
		},
		{
			name:       "return Unavailable when vendor returns Unauthenticated",
			args:       struct{ pocketSignUserID string }{pocketSignUserID: "user-xyz"},
			fakeSvcErr: connect.NewError(connect.CodeUnauthenticated, nil),
			wantErr:    apperr.ErrUnavailable,
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
			baseURL := startFakeServer(t, &fakeVerificationService{}, userSvc)
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

// --- VerifyClientConfig.IsConfigured tests ---

func TestVerifyClientConfig_IsConfigured(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args infrapocketsign.VerifyClientConfig
		want bool
	}{
		{
			name: "return true when both BaseURL and Token are set",
			args: infrapocketsign.VerifyClientConfig{BaseURL: "https://verify.test.p8n.app", Token: "tok"},
			want: true,
		},
		{
			name: "return false when BaseURL is empty",
			args: infrapocketsign.VerifyClientConfig{BaseURL: "", Token: "tok"},
			want: false,
		},
		{
			name: "return false when Token is empty",
			args: infrapocketsign.VerifyClientConfig{BaseURL: "https://verify.test.p8n.app", Token: ""},
			want: false,
		},
		{
			name: "return false when both are empty",
			args: infrapocketsign.VerifyClientConfig{},
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

// --- apperr code assertions ---

// TestVerifyClient_ValidateResponse_ErrorCodes asserts that Connect-RPC error
// codes are mapped to the expected apperr codes.
func TestVerifyClient_ValidateResponse_ErrorCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		connectErr connect.Code
		wantCode   codes.Code
	}{
		{
			name:       "CodeInvalidArgument maps to codes.InvalidArgument",
			connectErr: connect.CodeInvalidArgument,
			wantCode:   codes.InvalidArgument,
		},
		{
			name:       "CodeFailedPrecondition maps to codes.FailedPrecondition",
			connectErr: connect.CodeFailedPrecondition,
			wantCode:   codes.FailedPrecondition,
		},
		{
			name:       "CodeUnauthenticated maps to codes.Unavailable",
			connectErr: connect.CodeUnauthenticated,
			wantCode:   codes.Unavailable,
		},
		{
			name:       "CodeUnavailable maps to codes.Unavailable",
			connectErr: connect.CodeUnavailable,
			wantCode:   codes.Unavailable,
		},
		{
			name:       "CodeInternal maps to codes.Unavailable",
			connectErr: connect.CodeInternal,
			wantCode:   codes.Unavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			verifySvc := &fakeVerificationService{
				verifyForDigitalIDAppErr: connect.NewError(tt.connectErr, nil),
			}
			baseURL := startFakeServer(t, verifySvc, &fakeUserService{})
			client := newTestClient(t, baseURL)

			// Issue a challenge so the session is valid; we want to test the
			// error path triggered by the Pocket Sign API, not missing session.
			sessionID := issueAndGetSessionID(t, client)

			_, err := client.ValidateResponse(context.Background(), sessionID, []byte("jwe"))
			require.Error(t, err)

			var appErr *apperr.AppErr
			require.ErrorAs(t, err, &appErr, "error must be an *apperr.AppErr")
			assert.Equal(t, tt.wantCode, appErr.Code)
		})
	}
}
