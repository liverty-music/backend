package zitadel_test

import (
	"context"
	"testing"

	infrazitadel "github.com/liverty-music/backend/internal/infrastructure/zitadel"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
)

// Outside the admin workload no provisioning call may succeed silently.
func TestUnavailableOrganizerProvisioner(t *testing.T) {
	t.Parallel()

	p := infrazitadel.NewUnavailableOrganizerProvisioner("fan")
	ctx := context.Background()

	_, ensureErr := p.EnsureTenantOrg(ctx, "org")
	_, findErr := p.FindTenantOrg(ctx, "org")
	errs := map[string]error{
		"CheckOperatorEmailAvailable": p.CheckOperatorEmailAvailable(ctx, "op@example.com"),
		"EnsureTenantOrg":             ensureErr,
		"FindTenantOrg":               findErr,
		"ProvisionTenant":             p.ProvisionTenant(ctx, "org", "zorg", "op@example.com"),
		"DeactivateOperators":         p.DeactivateOperators(ctx, "zorg"),
		"DeleteTenant":                p.DeleteTenant(ctx, "zorg"),
	}
	for method, err := range errs {
		assert.ErrorIs(t, err, apperr.ErrUnavailable, method)
	}
}
