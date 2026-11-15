package zitadel_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	objectpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/object/v2"
	userpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user/v2"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/liverty-music/backend/internal/entity"
	infrazitadel "github.com/liverty-music/backend/internal/infrastructure/zitadel"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubPasskeyLinkClient answers GetUserByID with user (or getErr) and
// CreatePasskeyRegistrationLink with a fixed code (or createErr).
type stubPasskeyLinkClient struct {
	user      *userpb.User
	getErr    error
	createErr error
	created   []*userpb.CreatePasskeyRegistrationLinkRequest
}

func (s *stubPasskeyLinkClient) GetUserByID(context.Context, *userpb.GetUserByIDRequest, ...grpc.CallOption) (*userpb.GetUserByIDResponse, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return &userpb.GetUserByIDResponse{User: s.user}, nil
}

func (s *stubPasskeyLinkClient) CreatePasskeyRegistrationLink(_ context.Context, in *userpb.CreatePasskeyRegistrationLinkRequest, _ ...grpc.CallOption) (*userpb.CreatePasskeyRegistrationLinkResponse, error) {
	s.created = append(s.created, in)
	if s.createErr != nil {
		return nil, s.createErr
	}
	return &userpb.CreatePasskeyRegistrationLinkResponse{Code: &userpb.PasskeyRegistrationCode{Id: "code-id", Code: "code"}}, nil
}

func humanUser(verified bool) *userpb.User {
	return &userpb.User{
		UserId:  "zitadel-fan-1",
		Details: &objectpb.Details{ResourceOwner: "product-org-1"},
		Type: &userpb.User_Human{Human: &userpb.HumanUser{
			Email: &userpb.HumanEmail{Email: "fan@example.com", IsVerified: verified},
		}},
	}
}

func newIssuer(t *testing.T, client *stubPasskeyLinkClient) *infrazitadel.PasskeyRegistrationLinkIssuer {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	issuer, err := infrazitadel.NewPasskeyRegistrationLinkIssuerWithClient(client, "https://auth.liverty-music.app", logger)
	require.NoError(t, err)
	return issuer
}

func TestPasskeyRegistrationLinkIssuer_GetIdentityEmail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("returns the address, whether it is verified, and the identity's organization", func(t *testing.T) {
		t.Parallel()
		for _, verified := range []bool{true, false} {
			client := &stubPasskeyLinkClient{user: humanUser(verified)}

			got, err := newIssuer(t, client).GetIdentityEmail(ctx, "zitadel-fan-1")

			require.NoError(t, err)
			assert.Equal(t, &entity.IdentityEmail{Address: "fan@example.com", Verified: verified, Organization: "product-org-1"}, got)
			assert.Empty(t, client.created, "reading issues no code")
		}
	})

	t.Run("fails with NotFound for an identity that does not exist", func(t *testing.T) {
		t.Parallel()
		client := &stubPasskeyLinkClient{getErr: grpcstatus.Error(grpccodes.NotFound, "not found")}

		_, err := newIssuer(t, client).GetIdentityEmail(ctx, "zitadel-fan-1")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("fails with NotFound for a machine identity", func(t *testing.T) {
		t.Parallel()
		client := &stubPasskeyLinkClient{user: &userpb.User{UserId: "machine-1", Type: &userpb.User_Machine{}}}

		_, err := newIssuer(t, client).GetIdentityEmail(ctx, "machine-1")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("fails with Internal for an identity with no organization", func(t *testing.T) {
		t.Parallel()
		user := humanUser(true)
		user.Details = nil
		client := &stubPasskeyLinkClient{user: user}

		_, err := newIssuer(t, client).GetIdentityEmail(ctx, "zitadel-fan-1")

		assert.ErrorIs(t, err, apperr.ErrInternal)
	})
}

func TestPasskeyRegistrationLinkIssuer_CreatePasskeyRegistrationLink(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("returns a code-based /passkey/set link of the given organization", func(t *testing.T) {
		t.Parallel()
		client := &stubPasskeyLinkClient{}

		link, err := newIssuer(t, client).CreatePasskeyRegistrationLink(ctx, "zitadel-fan-1", "product-org-1")

		require.NoError(t, err)
		u, err := url.Parse(link)
		require.NoError(t, err)
		assert.Equal(t, "https://auth.liverty-music.app/ui/v2/login/passkey/set", u.Scheme+"://"+u.Host+u.Path)
		assert.Equal(t, url.Values{"userId": {"zitadel-fan-1"}, "code": {"code"}, "codeId": {"code-id"}, "organization": {"product-org-1"}}, u.Query())
		require.Len(t, client.created, 1)
		assert.NotNil(t, client.created[0].GetReturnCode(), "the code is returned, not emailed by Zitadel")
	})

	t.Run("fails with NotFound for an identity that does not exist", func(t *testing.T) {
		t.Parallel()
		client := &stubPasskeyLinkClient{createErr: grpcstatus.Error(grpccodes.NotFound, "not found")}

		_, err := newIssuer(t, client).CreatePasskeyRegistrationLink(ctx, "zitadel-fan-1", "product-org-1")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("fails with Internal when the code cannot be issued, without the code in the error", func(t *testing.T) {
		t.Parallel()
		client := &stubPasskeyLinkClient{createErr: grpcstatus.Error(grpccodes.PermissionDenied, "no user.passkey.write")}

		_, err := newIssuer(t, client).CreatePasskeyRegistrationLink(ctx, "zitadel-fan-1", "product-org-1")

		assert.ErrorIs(t, err, apperr.ErrInternal)
	})
}

func TestFanWebBaseURLFromIssuer(t *testing.T) {
	t.Parallel()

	for issuer, want := range map[string]string{
		"https://auth.liverty-music.app":     "https://liverty-music.app",
		"https://auth.dev.liverty-music.app": "https://dev.liverty-music.app",
	} {
		got, err := infrazitadel.FanWebBaseURLFromIssuer(issuer)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	_, err := infrazitadel.FanWebBaseURLFromIssuer("https://login.example.com")
	assert.True(t, err != nil && strings.Contains(err.Error(), "auth."))
}
