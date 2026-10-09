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

// CheckOperatorEmailAvailable performs no check; every email is available.
func (p *NoopOrganizerProvisioner) CheckOperatorEmailAvailable(context.Context, string) error {
	return nil
}

// EnsureTenantOrg performs no real provisioning and returns a placeholder org id.
func (p *NoopOrganizerProvisioner) EnsureTenantOrg(ctx context.Context, organizerID string) (string, error) {
	p.logger.Warn(ctx, "organizer tenant org creation skipped: no organizer-provisioner credential configured",
		slog.String("organizer_id", organizerID))
	return "local-org-" + organizerID, nil
}

// FindTenantOrg reports NotFound: no tenant org exists without a live Zitadel.
func (p *NoopOrganizerProvisioner) FindTenantOrg(_ context.Context, organizerID string) (string, error) {
	return "", apperr.New(codes.NotFound, "tenant org not found", slog.String("organizer_id", organizerID))
}

// ProvisionTenant performs no real provisioning.
func (p *NoopOrganizerProvisioner) ProvisionTenant(ctx context.Context, organizerID, _, _ string) error {
	p.logger.Warn(ctx, "organizer provisioning skipped: no organizer-provisioner credential configured",
		slog.String("organizer_id", organizerID))
	return nil
}

// DeactivateOperators performs no real teardown.
func (p *NoopOrganizerProvisioner) DeactivateOperators(ctx context.Context, zitadelOrgID string) error {
	p.logger.Warn(ctx, "organizer operator deactivation skipped: no organizer-provisioner credential configured",
		slog.String("zitadel_org_id", zitadelOrgID))
	return nil
}

// DeleteTenant performs no real teardown.
func (p *NoopOrganizerProvisioner) DeleteTenant(ctx context.Context, zitadelOrgID string) error {
	p.logger.Warn(ctx, "organizer tenant removal skipped: no organizer-provisioner credential configured",
		slog.String("zitadel_org_id", zitadelOrgID))
	return nil
}
