package zitadel

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// Compile-time interface compliance check.
var _ usecase.OrganizerProvisioner = (*NoopOrganizerProvisioner)(nil)

// NoopOrganizerProvisioner is used when no organizer-provisioner credential is
// configured (local development, where there is no live Zitadel). It performs no
// real provisioning and returns a deterministic placeholder org id so the admin
// Create path stays exercisable without a live Zitadel instance.
type NoopOrganizerProvisioner struct {
	logger *logging.Logger
}

// NewNoopOrganizerProvisioner creates a NoopOrganizerProvisioner.
func NewNoopOrganizerProvisioner(logger *logging.Logger) *NoopOrganizerProvisioner {
	return &NoopOrganizerProvisioner{logger: logger}
}

// EnsureTenantOrg performs no real provisioning and returns a placeholder org id.
func (p *NoopOrganizerProvisioner) EnsureTenantOrg(ctx context.Context, shortName string) (string, error) {
	p.logger.Warn(ctx, "organizer tenant org creation skipped: no organizer-provisioner credential configured",
		slog.String("short_name", shortName))
	return "local-org-" + shortName, nil
}

// FindTenantOrg reports NotFound: no tenant org exists without a live Zitadel.
func (p *NoopOrganizerProvisioner) FindTenantOrg(_ context.Context, shortName string) (string, error) {
	return "", apperr.New(codes.NotFound, "tenant org not found", slog.String("short_name", shortName))
}

// ProvisionTenant performs no real provisioning.
func (p *NoopOrganizerProvisioner) ProvisionTenant(ctx context.Context, providerOrganizerID, _, _ string) error {
	p.logger.Warn(ctx, "organizer provisioning skipped: no organizer-provisioner credential configured",
		slog.String("zitadel_org_id", providerOrganizerID))
	return nil
}

// DeactivateOperators performs no real teardown.
func (p *NoopOrganizerProvisioner) DeactivateOperators(ctx context.Context, providerOrganizerID string) error {
	p.logger.Warn(ctx, "organizer operator deactivation skipped: no organizer-provisioner credential configured",
		slog.String("zitadel_org_id", providerOrganizerID))
	return nil
}

// DeleteTenant performs no real teardown.
func (p *NoopOrganizerProvisioner) DeleteTenant(ctx context.Context, providerOrganizerID string) error {
	p.logger.Warn(ctx, "organizer tenant removal skipped: no organizer-provisioner credential configured",
		slog.String("zitadel_org_id", providerOrganizerID))
	return nil
}
