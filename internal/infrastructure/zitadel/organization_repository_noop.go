package zitadel

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time interface compliance check.
var _ entity.OrganizationRepository = (*NoopOrganizationRepository)(nil)

// NoopOrganizationRepository is used when no organizer-provisioner credential
// is configured (local development, where there is no live Zitadel). It
// performs no real provisioning and returns a deterministic placeholder
// Organization id so the admin Create path stays exercisable without a live
// Zitadel instance.
type NoopOrganizationRepository struct {
	logger *logging.Logger
}

// NewNoopOrganizationRepository creates a NoopOrganizationRepository.
func NewNoopOrganizationRepository(logger *logging.Logger) *NoopOrganizationRepository {
	return &NoopOrganizationRepository{logger: logger}
}

// Create performs no real provisioning and returns a placeholder Organization id.
func (p *NoopOrganizationRepository) Create(ctx context.Context, shortName string) (string, error) {
	p.logger.Warn(ctx, "organization creation skipped: no organizer-provisioner credential configured",
		slog.String("short_name", shortName))
	return "local-org-" + shortName, nil
}

// GetByName reports NotFound: no Organization exists without a live Zitadel.
func (p *NoopOrganizationRepository) GetByName(_ context.Context, shortName string) (string, error) {
	return "", apperr.New(codes.NotFound, "organization not found", slog.String("short_name", shortName))
}

// Provision performs no real provisioning.
func (p *NoopOrganizationRepository) Provision(ctx context.Context, providerOrganizerID, _, _ string) error {
	p.logger.Warn(ctx, "organization provisioning skipped: no organizer-provisioner credential configured",
		slog.String("zitadel_org_id", providerOrganizerID))
	return nil
}

// DeactivateIdentities performs no real teardown.
func (p *NoopOrganizationRepository) DeactivateIdentities(ctx context.Context, providerOrganizerID string) error {
	p.logger.Warn(ctx, "identity deactivation skipped: no organizer-provisioner credential configured",
		slog.String("zitadel_org_id", providerOrganizerID))
	return nil
}

// Delete performs no real teardown.
func (p *NoopOrganizationRepository) Delete(ctx context.Context, providerOrganizerID string) error {
	p.logger.Warn(ctx, "organization removal skipped: no organizer-provisioner credential configured",
		slog.String("zitadel_org_id", providerOrganizerID))
	return nil
}
