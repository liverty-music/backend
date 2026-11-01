package di

import (
	"fmt"
	"time"

	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/identity"
	"github.com/liverty-music/backend/internal/infrastructure/mail"
	infrazitadel "github.com/liverty-music/backend/internal/infrastructure/zitadel"
	"github.com/liverty-music/backend/pkg/config"
)

// newEmailVerifier returns the sender of verification emails, whose
// codes the identity service issues and whose links open the fan web (origin
// derived from the issuer), or nil without IDENTITY_SERVICE_URL (local
// development), which makes the verification handlers skip with a warning.
// Without a Postmark token (local development) the sender fails every send.
func newEmailVerifier(cfg *config.AccountEmailConfig, logger *logging.Logger) (entity.EmailVerifier, error) {
	if cfg.IdentityServiceURL == "" {
		return nil, nil
	}
	if cfg.Postmark.ServerToken == "" {
		return mail.NewNoopEmailVerificationSender(logger), nil
	}
	fanWebBaseURL, err := infrazitadel.FanWebBaseURLFromIssuer(cfg.ZitadelDomain)
	if err != nil {
		return nil, fmt.Errorf("derive fan web base url: %w", err)
	}
	return mail.NewPostmarkEmailVerificationSender(identity.NewUserClient(cfg.IdentityServiceURL), fanWebBaseURL,
		cfg.Postmark.ServerToken, cfg.Postmark.FromAddress, cfg.Postmark.MessageStream, logger), nil
}

// newPasskeyRegistrationLinkSender returns the sender of passkey recovery
// links, whose link the identity service issues, or nil without
// IDENTITY_SERVICE_URL (local development). Without a Postmark token (local
// development) the sender fails every send.
func newPasskeyRegistrationLinkSender(cfg *config.AccountEmailConfig, logger *logging.Logger) entity.PasskeyRegistrationLinkSender {
	if cfg.IdentityServiceURL == "" {
		return nil
	}
	if cfg.Postmark.ServerToken == "" {
		return mail.NewNoopPasskeyRegistrationLinkSender(logger)
	}
	return mail.NewPostmarkPasskeyRegistrationLinkSender(identity.NewUserClient(cfg.IdentityServiceURL), cfg.Postmark.ServerToken,
		cfg.Postmark.FromAddress, cfg.Postmark.MessageStream, logger)
}

// newPasskeyChangeEmailSender returns the sender of the passkey change email:
// Postmark when its server token is set, else (local development) a sender
// that fails with Unavailable. Its links open the fan web, whose origin is
// derived from the issuer.
func newPasskeyChangeEmailSender(cfg *config.AccountEmailConfig, japanTime *time.Location, logger *logging.Logger) (entity.PasskeyChangeEmailSender, error) {
	if cfg.Postmark.ServerToken == "" {
		return mail.NewNoopPasskeyChangeSender(logger), nil
	}
	fanWebBaseURL, err := infrazitadel.FanWebBaseURLFromIssuer(cfg.ZitadelDomain)
	if err != nil {
		return nil, fmt.Errorf("derive fan web base url: %w", err)
	}
	return mail.NewPostmarkPasskeyChangeSender(fanWebBaseURL, japanTime, cfg.Postmark.ServerToken,
		cfg.Postmark.FromAddress, cfg.Postmark.MessageStream, logger), nil
}
