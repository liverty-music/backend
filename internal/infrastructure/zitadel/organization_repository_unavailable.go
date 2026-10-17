package zitadel

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// Compile-time interface compliance check.
var _ entity.OrganizationRepository = (*UnavailableOrganizationRepository)(nil)

// UnavailableOrganizationRepository is used outside local development on every
// API workload except admin. Only the admin workload carries the
// organizer-provisioner credential, so the admin RPCs that reach the
// identity provider's Organizations must not succeed silently on another
// workload: every call fails with Unavailable instead.
type UnavailableOrganizationRepository struct {
	workload string
}

// NewUnavailableOrganizationRepository creates an
// UnavailableOrganizationRepository for the named workload.
func NewUnavailableOrganizationRepository(workload string) *UnavailableOrganizationRepository {
	return &UnavailableOrganizationRepository{workload: workload}
}

func (p *UnavailableOrganizationRepository) err() error {
	return apperr.New(codes.Unavailable, "organizer provisioning runs only on the admin workload",
		slog.String("workload", p.workload))
}

// Create fails with Unavailable.
func (p *UnavailableOrganizationRepository) Create(context.Context, string) (string, error) {
	return "", p.err()
}

// GetByName fails with Unavailable.
func (p *UnavailableOrganizationRepository) GetByName(context.Context, string) (string, error) {
	return "", p.err()
}

// Provision fails with Unavailable.
func (p *UnavailableOrganizationRepository) Provision(context.Context, string, string, string) error {
	return p.err()
}

// DeactivateIdentities fails with Unavailable.
func (p *UnavailableOrganizationRepository) DeactivateIdentities(context.Context, string) error {
	return p.err()
}

// Delete fails with Unavailable.
func (p *UnavailableOrganizationRepository) Delete(context.Context, string) error {
	return p.err()
}
