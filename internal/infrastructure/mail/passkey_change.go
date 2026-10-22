package mail

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// passkeyChangeTag tags passkey change emails in Postmark's activity log.
const passkeyChangeTag = "passkey-change"

// PostmarkPasskeyChangeSender implements [entity.PasskeyChangeEmailSender]
// with Postmark's transactional message stream.
type PostmarkPasskeyChangeSender struct {
	postmarkClient
	// fanWebBaseURL is the fan web origin, e.g. https://liverty-music.app.
	fanWebBaseURL string
	// japanTime is the Asia/Tokyo location the change time is shown in.
	japanTime *time.Location
}

// Compile-time interface compliance check.
var _ entity.PasskeyChangeEmailSender = (*PostmarkPasskeyChangeSender)(nil)

// NewPostmarkPasskeyChangeSender creates a sender for the given server token,
// From address and message stream whose links open the fan web at
// fanWebBaseURL and whose times are shown in japanTime.
func NewPostmarkPasskeyChangeSender(fanWebBaseURL string, japanTime *time.Location, serverToken, fromAddress, messageStream string, logger *logging.Logger) *PostmarkPasskeyChangeSender {
	return newPostmarkPasskeyChangeSender(fanWebBaseURL, japanTime, postmarkAPIURL, serverToken, fromAddress, messageStream, logger)
}

func newPostmarkPasskeyChangeSender(fanWebBaseURL string, japanTime *time.Location, apiURL, serverToken, fromAddress, messageStream string, logger *logging.Logger) *PostmarkPasskeyChangeSender {
	return &PostmarkPasskeyChangeSender{
		postmarkClient: newPostmarkClient(apiURL, serverToken, fromAddress, messageStream, logger),
		fanWebBaseURL:  strings.TrimRight(fanWebBaseURL, "/"),
		japanTime:      japanTime,
	}
}

// SendPasskeyChangeEmail implements [entity.PasskeyChangeEmailSender]. A
// failure to hand the email to Postmark fails with Unavailable.
func (s *PostmarkPasskeyChangeSender) SendPasskeyChangeEmail(ctx context.Context, user *entity.User, change entity.PasskeyChange) error {
	attrs := []slog.Attr{slog.String("user_id", user.ID), slog.String("kind", string(change.Kind))}
	if user.Email == "" {
		return apperr.New(codes.InvalidArgument, "the user has no email address", attrs...)
	}
	if !change.Kind.IsValid() {
		return apperr.New(codes.InvalidArgument, "unknown kind of passkey change", attrs...)
	}
	subject, body := s.passkeyChangeEmail(emailLanguage(user.PreferredLanguage), change)
	if err := s.post(ctx, user.Email, subject, body, passkeyChangeTag,
		map[string]string{"user_id": user.ID, "kind": string(change.Kind)}, attrs...); err != nil {
		return apperr.Wrap(err, codes.Unavailable, "passkey change email cannot be sent", attrs...)
	}
	return nil
}

// passkeyChangeEmail renders the subject and text of the passkey change email
// in lang ("ja" or "en"): what changed, the passkey's name when known, when
// (Japan time), and what to do when the reader did not make the change.
func (s *PostmarkPasskeyChangeSender) passkeyChangeEmail(lang string, change entity.PasskeyChange) (subject, body string) {
	at := change.ChangeTime.In(s.japanTime)
	recoveryURL := s.fanWebBaseURL + "/passkey-recovery"
	passkeysURL := s.fanWebBaseURL + "/settings/passkeys"
	added := change.Kind == entity.PasskeyChangeAdded

	var b strings.Builder
	if lang == "en" {
		if added {
			subject = "[Liverty Music] A passkey was added to your account"
			b.WriteString("A passkey was added to your Liverty Music account.\n\n")
		} else {
			subject = "[Liverty Music] A passkey was removed from your account"
			b.WriteString("A passkey was removed from your Liverty Music account.\n\n")
		}
		if change.Name != "" {
			fmt.Fprintf(&b, "Passkey: %s\n", change.Name)
		}
		fmt.Fprintf(&b, "Time: %s (Japan time)\n\n", at.Format("Jan 2, 2006 15:04"))
		b.WriteString("If you made this change, no action is needed.\n\n")
		b.WriteString("If you did not make this change, someone else may have access to your account:\n")
		fmt.Fprintf(&b, "- If you cannot sign in, get a new passkey from the sign-in help: %s\n", recoveryURL)
		fmt.Fprintf(&b, "- Remove any passkey you do not recognize in Settings: %s\n\n", passkeysURL)
		b.WriteString("Liverty Music\n")
		return subject, b.String()
	}

	if added {
		subject = "【Liverty Music】アカウントにパスキーが追加されました"
		b.WriteString("Liverty Music のアカウントにパスキーが追加されました。\n\n")
	} else {
		subject = "【Liverty Music】アカウントからパスキーが削除されました"
		b.WriteString("Liverty Music のアカウントからパスキーが削除されました。\n\n")
	}
	if change.Name != "" {
		fmt.Fprintf(&b, "パスキー: %s\n", change.Name)
	}
	fmt.Fprintf(&b, "日時: %d年%d月%d日 %s（日本時間）\n\n", at.Year(), int(at.Month()), at.Day(), at.Format("15:04"))
	b.WriteString("ご自身で操作した場合は、対応は不要です。\n\n")
	b.WriteString("お心当たりがない場合は、第三者があなたのアカウントを操作した可能性があります。\n")
	fmt.Fprintf(&b, "・サインインできない場合は、サインインヘルプから新しいパスキーを登録してください: %s\n", recoveryURL)
	fmt.Fprintf(&b, "・見覚えのないパスキーは、設定画面から削除してください: %s\n\n", passkeysURL)
	b.WriteString("Liverty Music\n")
	return subject, b.String()
}

// NoopPasskeyChangeSender is used when no Postmark token is configured (local
// development): every call fails with Unavailable.
type NoopPasskeyChangeSender struct {
	logger *logging.Logger
}

// Compile-time interface compliance check.
var _ entity.PasskeyChangeEmailSender = (*NoopPasskeyChangeSender)(nil)

// NewNoopPasskeyChangeSender creates a NoopPasskeyChangeSender.
func NewNoopPasskeyChangeSender(logger *logging.Logger) *NoopPasskeyChangeSender {
	return &NoopPasskeyChangeSender{logger: logger}
}

// SendPasskeyChangeEmail returns Unavailable because no Postmark token is set.
func (s *NoopPasskeyChangeSender) SendPasskeyChangeEmail(ctx context.Context, user *entity.User, change entity.PasskeyChange) error {
	s.logger.Warn(ctx, "passkey change email skipped: POSTMARK_SERVER_TOKEN is not configured",
		slog.String("user_id", user.ID), slog.String("kind", string(change.Kind)))
	return apperr.New(codes.Unavailable, "email is not configured")
}
