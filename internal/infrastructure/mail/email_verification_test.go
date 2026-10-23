package mail_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/mail"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeEmailVerificationCodes stands in for the identity service: it issues
// one fixed code for its address, or fails with err.
type fakeEmailVerificationCodes struct {
	address string
	code    string
	err     error
	calls   []string // "<method> <provider user id>"
}

func (f *fakeEmailVerificationCodes) IssueEmailVerificationCode(_ context.Context, providerUserID string) (string, string, error) {
	f.calls = append(f.calls, "Issue "+providerUserID)
	if f.err != nil {
		return "", "", f.err
	}
	return f.address, f.code, nil
}

func (f *fakeEmailVerificationCodes) ReissueEmailVerificationCode(_ context.Context, providerUserID string) (string, string, error) {
	f.calls = append(f.calls, "Reissue "+providerUserID)
	if f.err != nil {
		return "", "", f.err
	}
	return f.address, f.code, nil
}

func newEmailVerificationSender(t *testing.T, codes *fakeEmailVerificationCodes, stub *stubPostmark) (*mail.PostmarkEmailVerificationSender, *bytes.Buffer) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(srv.Close)
	logs := &bytes.Buffer{}
	logger, err := logging.New(logging.WithWriter(logs))
	require.NoError(t, err)
	return mail.NewPostmarkEmailVerificationSenderForTest(codes, "https://liverty-music.app", srv.URL, "server-token", logger), logs
}

func TestPostmarkEmailVerificationSender(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const userID = "019a0000-0000-7000-8000-000000000001"
	wantLink := "https://liverty-music.app/verify-email?" + url.Values{"user": {userID}, "code": {"AB12CD"}}.Encode()

	// @spec components/entity/user/send-verification "Unverified user"
	t.Run("sends an English fan an English email linking to the email verification screen", func(t *testing.T) {
		t.Parallel()
		issuer := &fakeEmailVerificationCodes{address: "fan@example.com", code: "AB12CD"}
		stub := &stubPostmark{status: http.StatusOK}
		sender, logs := newEmailVerificationSender(t, issuer, stub)

		err := sender.SendVerification(ctx, &entity.User{ID: userID, ProviderUserID: "zitadel-fan-1", PreferredLanguage: "en"})

		require.NoError(t, err)
		assert.Equal(t, []string{"Issue zitadel-fan-1"}, issuer.calls)
		require.Len(t, stub.sent, 1)
		sent := stub.sent[0]
		assert.Equal(t, "fan@example.com", sent["To"])
		assert.Equal(t, "[Liverty Music] Verify your email address", sent["Subject"])
		assert.Equal(t, "email-verification", sent["Tag"])
		body, _ := sent["TextBody"].(string)
		assert.Contains(t, body, wantLink)
		assert.Contains(t, body, "1 hour")
		assert.Contains(t, body, "If you did not sign up for Liverty Music, you can ignore this email.")
		assert.NotContains(t, logs.String(), "AB12CD", "neither the code nor the link is logged")
	})

	t.Run("writes in Japanese when the fan has no preferred language", func(t *testing.T) {
		t.Parallel()
		issuer := &fakeEmailVerificationCodes{address: "fan@example.com", code: "AB12CD"}
		stub := &stubPostmark{status: http.StatusOK}
		sender, _ := newEmailVerificationSender(t, issuer, stub)

		err := sender.SendVerification(ctx, &entity.User{ID: userID, ProviderUserID: "zitadel-fan-1"})

		require.NoError(t, err)
		require.Len(t, stub.sent, 1)
		assert.Equal(t, "【Liverty Music】メールアドレスの確認", stub.sent[0]["Subject"])
		body, _ := stub.sent[0]["TextBody"].(string)
		assert.Contains(t, body, wantLink)
		assert.Contains(t, body, "1時間")
		assert.Contains(t, body, "お心当たりがない場合は、このメールを無視してください")
		assert.NotContains(t, body, "Zitadel")
	})

	// @spec components/entity/user/send-verification "Sending fails"
	t.Run("a rejected email is Internal", func(t *testing.T) {
		t.Parallel()
		issuer := &fakeEmailVerificationCodes{address: "fan@example.com", code: "AB12CD"}
		sender, _ := newEmailVerificationSender(t, issuer, &stubPostmark{status: http.StatusInternalServerError})

		err := sender.SendVerification(ctx, &entity.User{ID: userID, ProviderUserID: "zitadel-fan-1"})

		assert.ErrorIs(t, err, apperr.ErrInternal)
	})

	t.Run("a code that cannot be issued at sign-up is Internal", func(t *testing.T) {
		t.Parallel()
		issuer := &fakeEmailVerificationCodes{err: apperr.New(codes.Unavailable, "identity service down")}
		stub := &stubPostmark{status: http.StatusOK}
		sender, _ := newEmailVerificationSender(t, issuer, stub)

		err := sender.SendVerification(ctx, &entity.User{ID: userID, ProviderUserID: "zitadel-fan-1"})

		assert.ErrorIs(t, err, apperr.ErrInternal)
		assert.Empty(t, stub.sent)
	})

	resendTests := []struct {
		name     string
		issueErr error
		wantErr  error
		wantSent int
	}{
		// @spec components/entity/user/resend-verification "Earlier code exists"
		// @spec components/entity/user/resend-verification "No earlier code"
		// @spec components/infrastructure/backend/process/identity-provider-credentials "Fan requests a verification resend"
		{name: "the resend asks the identity service for a fresh code and emails it", wantSent: 1},
		// @spec components/entity/user/resend-verification "Already verified"
		{name: "an already verified address sends nothing and is FailedPrecondition", issueErr: apperr.New(codes.FailedPrecondition, "email is already verified"), wantErr: apperr.ErrFailedPrecondition},
		// @spec components/entity/user/resend-verification "Other failure"
		{name: "any other failure is Internal", issueErr: apperr.New(codes.Unavailable, "identity service down"), wantErr: apperr.ErrInternal},
	}
	for _, tt := range resendTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			issuer := &fakeEmailVerificationCodes{address: "fan@example.com", code: "AB12CD", err: tt.issueErr}
			stub := &stubPostmark{status: http.StatusOK}
			sender, _ := newEmailVerificationSender(t, issuer, stub)

			err := sender.ResendVerification(ctx, &entity.User{ID: userID, ProviderUserID: "zitadel-fan-1"})

			assert.Equal(t, []string{"Reissue zitadel-fan-1"}, issuer.calls)
			require.Len(t, stub.sent, tt.wantSent)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			body, _ := stub.sent[0]["TextBody"].(string)
			assert.Contains(t, body, wantLink)
		})
	}
}
