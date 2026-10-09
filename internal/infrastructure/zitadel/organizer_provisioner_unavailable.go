package zitadel

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// Compile-time interface compliance check.
var _ usecase.OrganizerProvisioner = (*UnavailableOrganizerProvisioner)(nil)

// UnavailableOrganizerProvisioner is used outside local development on every
// API workload except admin. Only the admin workload carries the
// organizer-provisioner credential, so the admin RPCs that reach the
// provisioner must not succeed silently on another workload: every call fails
// with Unavailable instead.
type UnavailableOrganizerProvisioner struct {
	workload string
}

// NewUnavailableOrganizerProvisioner creates an UnavailableOrganizerProvisioner
// for the named workload.
func NewUnavailableOrganizerProvisioner(workload string) *UnavailableOrganizerProvisioner {
	return &UnavailableOrganizerProvisioner{workload: workload}
}

func (p *UnavailableOrganizerProvisioner) err() error {
	return apperr.New(codes.Unavailable, "organizer provisioning runs only on the admin workload",
		slog.String("workload", p.workload))
}

// CheckOperatorEmailAvailable fails with Unavailable.
func (p *UnavailableOrganizerProvisioner) CheckOperatorEmailAvailable(context.Context, string) error {
	return p.err()
}

// EnsureTenantOrg fails with Unavailable.
func (p *UnavailableOrganizerProvisioner) EnsureTenantOrg(context.Context, string) (string, error) {
	return "", p.err()
}

// FindTenantOrg fails with Unavailable.
func (p *UnavailableOrganizerProvisioner) FindTenantOrg(context.Context, string) (string, error) {
	return "", p.err()
}

// ProvisionTenant fails with Unavailable.
func (p *UnavailableOrganizerProvisioner) ProvisionTenant(context.Context, string, string, string) error {
	return p.err()
}

// DeactivateOperators fails with Unavailable.
func (p *UnavailableOrganizerProvisioner) DeactivateOperators(context.Context, string) error {
	return p.err()
}

// DeleteTenant fails with Unavailable.
func (p *UnavailableOrganizerProvisioner) DeleteTenant(context.Context, string) error {
	return p.err()
}
