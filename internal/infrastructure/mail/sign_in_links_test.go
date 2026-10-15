package mail_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/mail"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSignInLinkSender(t *testing.T, apiURL string) *mail.PostmarkSignInLinkSender {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return mail.NewPostmarkSignInLinkSenderForTest("https://organizer.liverty-music.app", apiURL, "server-token", logger)
}

func TestPostmarkSignInLinkSender_SendSignInLinks(t *testing.T) {
	t.Parallel()

	organizers := []*entity.Organizer{
		{ID: "org-xx", Name: "XX Records", ShortName: "xx-records"},
		{ID: "org-yy", Name: "YY Promotion", ShortName: "yy-promotion"},
	}

	// @spec components/entity/organizer/send-sign-in-links "Two Organizers"
	t.Run("sends one Japanese email naming both Organizers, each with its own sign-in link", func(t *testing.T) {
		t.Parallel()
		stub := &stubPostmark{status: http.StatusOK}
		srv := httptest.NewServer(http.HandlerFunc(stub.handler))
		t.Cleanup(srv.Close)

		err := newSignInLinkSender(t, srv.URL).SendSignInLinks(context.Background(), "band@example.com", organizers)

		require.NoError(t, err)
		require.Len(t, stub.sent, 1, "one email")
		sent := stub.sent[0]
		assert.Equal(t, "band@example.com", sent["To"])
		assert.Equal(t, "Liverty Music オーガナイザーコンソールのサインインリンク", sent["Subject"])
		assert.Equal(t, "organizer-sign-in-links", sent["Tag"])
		assert.Equal(t, "outbound", sent["MessageStream"])
		assert.Equal(t, "server-token", stub.tokens[0])
		assert.Equal(t, "Liverty Music オーガナイザーコンソールのサインインリンクをお送りします。\n"+
			"サインインするオーガナイザーのリンクを開き、このメールアドレスを入力してパスキーでサインインしてください。\n\n"+
			"XX Records\nhttps://organizer.liverty-music.app/?org=xx-records\n\n"+
			"YY Promotion\nhttps://organizer.liverty-music.app/?org=yy-promotion\n\n"+
			"このメールは、オーガナイザーコンソールでサインインリンクの送信がリクエストされたため送信しています。"+
			"お心当たりがない場合は、このメールを無視してください。\n\n"+
			"Liverty Music\n",
			sent["TextBody"])
	})

	// @spec components/entity/organizer/send-sign-in-links "Mail service unavailable"
	t.Run("fails with Internal when Postmark refuses the email", func(t *testing.T) {
		t.Parallel()
		stub := &stubPostmark{status: http.StatusServiceUnavailable}
		srv := httptest.NewServer(http.HandlerFunc(stub.handler))
		t.Cleanup(srv.Close)

		err := newSignInLinkSender(t, srv.URL).SendSignInLinks(context.Background(), "band@example.com", organizers)

		assert.ErrorIs(t, err, apperr.ErrInternal)
	})

	t.Run("fails with Internal when Postmark cannot be reached", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.NotFoundHandler())
		srv.Close()

		err := newSignInLinkSender(t, srv.URL).SendSignInLinks(context.Background(), "band@example.com", organizers)

		assert.ErrorIs(t, err, apperr.ErrInternal)
	})
}

func TestNoopSignInLinkSender_SendSignInLinks(t *testing.T) {
	t.Parallel()
	logger, err := logging.New()
	require.NoError(t, err)

	err = mail.NewNoopSignInLinkSender(logger).SendSignInLinks(context.Background(), "band@example.com", nil)

	assert.ErrorIs(t, err, apperr.ErrInternal)
}
