package mail_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	objectpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/object/v2"
	userpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user/v2"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/mail"
	infrazitadel "github.com/liverty-music/backend/internal/infrastructure/zitadel"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeIdentityProvider is an in-memory Zitadel v2 User Service holding one
// human identity.
type fakeIdentityProvider struct {
	user *userpb.User // nil: the identity does not exist
	// issued counts the registration codes issued.
	issued int
}

func (f *fakeIdentityProvider) GetUserByID(_ context.Context, in *userpb.GetUserByIDRequest, _ ...grpc.CallOption) (*userpb.GetUserByIDResponse, error) {
	if f.user == nil || f.user.GetUserId() != in.GetUserId() {
		return nil, grpcstatus.Error(grpccodes.NotFound, "User could not be found")
	}
	return &userpb.GetUserByIDResponse{User: f.user}, nil
}

func (f *fakeIdentityProvider) CreatePasskeyRegistrationLink(_ context.Context, in *userpb.CreatePasskeyRegistrationLinkRequest, _ ...grpc.CallOption) (*userpb.CreatePasskeyRegistrationLinkResponse, error) {
	if f.user == nil || f.user.GetUserId() != in.GetUserId() {
		return nil, grpcstatus.Error(grpccodes.NotFound, "User could not be found")
	}
	if in.GetReturnCode() == nil {
		return nil, grpcstatus.Error(grpccodes.InvalidArgument, "only return_code is expected")
	}
	f.issued++
	return &userpb.CreatePasskeyRegistrationLinkResponse{
		Code: &userpb.PasskeyRegistrationCode{Id: "code-id-1", Code: "SECRET+CODE"},
	}, nil
}

// fanIdentity is a human identity of the product org with the given address.
func fanIdentity(address string, verified bool) *userpb.User {
	return &userpb.User{
		UserId:  "zitadel-fan-1",
		Details: &objectpb.Details{ResourceOwner: "product-org-1"},
		Type: &userpb.User_Human{Human: &userpb.HumanUser{
			Email: &userpb.HumanEmail{Email: address, IsVerified: verified},
		}},
	}
}

func newPasskeyLinkSender(t *testing.T, idp *fakeIdentityProvider, stub *stubPostmark) *mail.PostmarkPasskeyRegistrationLinkSender {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(srv.Close)
	logger, err := logging.New()
	require.NoError(t, err)
	issuer, err := infrazitadel.NewPasskeyRegistrationLinkIssuerWithClient(idp, "https://auth.liverty-music.app", logger)
	require.NoError(t, err)
	return mail.NewPostmarkPasskeyRegistrationLinkSenderForTest(issuer, srv.URL, "server-token", logger)
}

func TestPostmarkPasskeyRegistrationLinkSender_SendPasskeyRegistrationLink(t *testing.T) {
	t.Parallel()

	fan := &entity.User{ID: "user-1", ProviderUserID: "zitadel-fan-1", Email: "fan@example.com", PreferredLanguage: "ja"}

	// @spec components/entity/user/send-passkey-registration-link "Verified address"
	t.Run("sends one email with a registration link to the verified address", func(t *testing.T) {
		t.Parallel()
		idp := &fakeIdentityProvider{user: fanIdentity("fan@example.com", true)}
		stub := &stubPostmark{status: http.StatusOK}

		err := newPasskeyLinkSender(t, idp, stub).SendPasskeyRegistrationLink(context.Background(), fan)

		require.NoError(t, err)
		require.Len(t, stub.sent, 1, "one email")
		sent := stub.sent[0]
		assert.Equal(t, "fan@example.com", sent["To"])
		assert.Equal(t, "【Liverty Music】新しいパスキーの登録", sent["Subject"])
		assert.Equal(t, "passkey-registration-link", sent["Tag"])
		body, _ := sent["TextBody"].(string)
		wantLink := "https://auth.liverty-music.app/ui/v2/login/passkey/set?" + url.Values{
			"userId": {"zitadel-fan-1"}, "code": {"SECRET+CODE"}, "codeId": {"code-id-1"}, "organization": {"product-org-1"},
		}.Encode()
		assert.Contains(t, body, wantLink)
		assert.Contains(t, body, "1時間")
		assert.Contains(t, body, "1回")
		assert.Contains(t, body, "サインインヘルプ")
		assert.Contains(t, body, "お心当たりがない場合は、このメールを無視してください")
		assert.Equal(t, 1, idp.issued)
	})

	t.Run("sends to the address the identity verified and in English for an English-speaking fan", func(t *testing.T) {
		t.Parallel()
		idp := &fakeIdentityProvider{user: fanIdentity("Fan@Example.com", true)}
		stub := &stubPostmark{status: http.StatusOK}
		english := *fan
		english.PreferredLanguage = "en"

		err := newPasskeyLinkSender(t, idp, stub).SendPasskeyRegistrationLink(context.Background(), &english)

		require.NoError(t, err)
		require.Len(t, stub.sent, 1)
		assert.Equal(t, "Fan@Example.com", stub.sent[0]["To"])
		assert.Equal(t, "[Liverty Music] Register a new passkey", stub.sent[0]["Subject"])
		body, _ := stub.sent[0]["TextBody"].(string)
		assert.Contains(t, body, "1 hour")
		assert.Contains(t, body, "If you did not request this, you can ignore this email.")
	})

	// @spec components/entity/user/send-passkey-registration-link "Unverified address"
	t.Run("fails with FailedPrecondition and sends nothing when the address is not verified", func(t *testing.T) {
		t.Parallel()
		idp := &fakeIdentityProvider{user: fanIdentity("fan@example.com", false)}
		stub := &stubPostmark{status: http.StatusOK}

		err := newPasskeyLinkSender(t, idp, stub).SendPasskeyRegistrationLink(context.Background(), fan)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Empty(t, stub.sent)
		assert.Zero(t, idp.issued, "no code is issued")
	})

	// @spec components/entity/user/send-passkey-registration-link "Identity removed"
	t.Run("fails with NotFound when the identity no longer exists", func(t *testing.T) {
		t.Parallel()
		idp := &fakeIdentityProvider{}
		stub := &stubPostmark{status: http.StatusOK}

		err := newPasskeyLinkSender(t, idp, stub).SendPasskeyRegistrationLink(context.Background(), fan)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.Empty(t, stub.sent)
	})

	t.Run("fails with Internal when the email cannot be sent", func(t *testing.T) {
		t.Parallel()
		idp := &fakeIdentityProvider{user: fanIdentity("fan@example.com", true)}
		stub := &stubPostmark{status: http.StatusServiceUnavailable}

		err := newPasskeyLinkSender(t, idp, stub).SendPasskeyRegistrationLink(context.Background(), fan)

		assert.ErrorIs(t, err, apperr.ErrInternal)
		assert.NotContains(t, err.Error(), "SECRET", "the error never carries the code")
	})
}
