package mail_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/mail"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPasskeyChangeSender(t *testing.T, stub *stubPostmark) *mail.PostmarkPasskeyChangeSender {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(srv.Close)
	logger, err := logging.New()
	require.NoError(t, err)
	japanTime, err := time.LoadLocation("Asia/Tokyo")
	require.NoError(t, err)
	return mail.NewPostmarkPasskeyChangeSenderForTest("https://liverty-music.app", japanTime, srv.URL, "server-token", logger)
}

func TestPostmarkPasskeyChangeSender_SendPasskeyChangeEmail(t *testing.T) {
	t.Parallel()

	// 2026-10-22 03:04 UTC is 12:04 in Japan.
	changeTime := time.Date(2026, 10, 22, 3, 4, 0, 0, time.UTC)
	fan := &entity.User{ID: "user-1", ProviderUserID: "zitadel-fan-1", Email: "fan@example.com", PreferredLanguage: "ja"}

	// @spec components/entity/user/send-passkey-change-email "Passkey added"
	t.Run("sends one Japanese email naming the added passkey, when, and what to do", func(t *testing.T) {
		t.Parallel()
		stub := &stubPostmark{status: http.StatusOK}

		err := newPasskeyChangeSender(t, stub).SendPasskeyChangeEmail(context.Background(), fan, entity.PasskeyChange{
			Kind: entity.PasskeyChangeAdded, Name: "Apple iPhone, iOS, Mobile Safari", ChangeTime: changeTime,
		})

		require.NoError(t, err)
		require.Len(t, stub.sent, 1, "one email")
		sent := stub.sent[0]
		assert.Equal(t, "fan@example.com", sent["To"])
		assert.Equal(t, "passkey-change", sent["Tag"])
		assert.Equal(t, "【Liverty Music】アカウントにパスキーが追加されました", sent["Subject"])
		assert.Equal(t, "Liverty Music のアカウントにパスキーが追加されました。\n\n"+
			"パスキー: Apple iPhone, iOS, Mobile Safari\n"+
			"日時: 2026年10月22日 12:04（日本時間）\n\n"+
			"ご自身で操作した場合は、対応は不要です。\n\n"+
			"お心当たりがない場合は、第三者があなたのアカウントを操作した可能性があります。\n"+
			"・サインインできない場合は、サインインヘルプから新しいパスキーを登録してください: https://liverty-music.app/passkey-recovery\n"+
			"・見覚えのないパスキーは、設定画面から削除してください: https://liverty-music.app/settings/passkeys\n\n"+
			"Liverty Music\n",
			sent["TextBody"])
	})

	// @spec components/entity/user/send-passkey-change-email "Passkey removed"
	t.Run("sends one email saying a passkey was removed, without a name", func(t *testing.T) {
		t.Parallel()
		stub := &stubPostmark{status: http.StatusOK}

		err := newPasskeyChangeSender(t, stub).SendPasskeyChangeEmail(context.Background(), fan, entity.PasskeyChange{
			Kind: entity.PasskeyChangeRemoved, ChangeTime: changeTime,
		})

		require.NoError(t, err)
		require.Len(t, stub.sent, 1)
		assert.Equal(t, "【Liverty Music】アカウントからパスキーが削除されました", stub.sent[0]["Subject"])
		body, _ := stub.sent[0]["TextBody"].(string)
		assert.Contains(t, body, "パスキーが削除されました")
		assert.NotContains(t, body, "パスキー: ")
		assert.Contains(t, body, "2026年10月22日 12:04（日本時間）")
	})

	t.Run("writes in English for a fan who prefers English", func(t *testing.T) {
		t.Parallel()
		stub := &stubPostmark{status: http.StatusOK}
		english := *fan
		english.PreferredLanguage = "en"

		err := newPasskeyChangeSender(t, stub).SendPasskeyChangeEmail(context.Background(), &english, entity.PasskeyChange{
			Kind: entity.PasskeyChangeAdded, Name: "Google Pixel 9, Android, Chrome", ChangeTime: changeTime,
		})

		require.NoError(t, err)
		require.Len(t, stub.sent, 1)
		assert.Equal(t, "[Liverty Music] A passkey was added to your account", stub.sent[0]["Subject"])
		body, _ := stub.sent[0]["TextBody"].(string)
		assert.Contains(t, body, "Passkey: Google Pixel 9, Android, Chrome\n")
		assert.Contains(t, body, "Time: Oct 22, 2026 12:04 (Japan time)\n")
		assert.Contains(t, body, "https://liverty-music.app/passkey-recovery")
		assert.Contains(t, body, "https://liverty-music.app/settings/passkeys")
	})

	// @spec components/entity/user/send-passkey-change-email "Mail unavailable"
	t.Run("fails with Unavailable when email cannot be sent", func(t *testing.T) {
		t.Parallel()
		stub := &stubPostmark{status: http.StatusServiceUnavailable}

		err := newPasskeyChangeSender(t, stub).SendPasskeyChangeEmail(context.Background(), fan, entity.PasskeyChange{
			Kind: entity.PasskeyChangeRemoved, ChangeTime: changeTime,
		})

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})

	t.Run("fails with InvalidArgument when the user has no email address", func(t *testing.T) {
		t.Parallel()
		stub := &stubPostmark{status: http.StatusOK}
		noEmail := *fan
		noEmail.Email = ""

		err := newPasskeyChangeSender(t, stub).SendPasskeyChangeEmail(context.Background(), &noEmail, entity.PasskeyChange{
			Kind: entity.PasskeyChangeAdded, ChangeTime: changeTime,
		})

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Empty(t, stub.sent)
	})
}
