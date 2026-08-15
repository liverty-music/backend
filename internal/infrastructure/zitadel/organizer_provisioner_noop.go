package zitadel

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/usecase"
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

// ProvisionTenant performs no real provisioning and returns a placeholder org id.
func (p *NoopOrganizerProvisioner) ProvisionTenant(ctx context.Context, organizerID, name, operatorEmail string) (string, error) {
	p.logger.Warn(ctx, "organizer provisioning skipped: no organizer-provisioner credential configured",
		slog.String("organizer_id", organizerID))
	return "local-org-" + organizerID, nil
}

// DeactivateOperators performs no real teardown.
func (p *NoopOrganizerProvisioner) DeactivateOperators(ctx context.Context, zitadelOrgID string) error {
	p.logger.Warn(ctx, "organizer operator deactivation skipped: no organizer-provisioner credential configured",
		slog.String("zitadel_org_id", zitadelOrgID))
	return nil
}
