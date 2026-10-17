package zitadel_test

import (
	"context"
	"testing"

	infrazitadel "github.com/liverty-music/backend/internal/infrastructure/zitadel"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
)

// Outside the admin workload no provisioning call may succeed silently.
func TestUnavailableOrganizationRepository(t *testing.T) {
	t.Parallel()

	p := infrazitadel.NewUnavailableOrganizationRepository("fan")
	ctx := context.Background()

	_, createErr := p.Create(ctx, "org")
	_, getErr := p.GetByName(ctx, "org")
	errs := map[string]error{
		"Create":               createErr,
		"GetByName":            getErr,
		"Provision":            p.Provision(ctx, "zorg", "xx-records", "owner@example.com"),
		"DeactivateIdentities": p.DeactivateIdentities(ctx, "zorg"),
		"Delete":               p.Delete(ctx, "zorg"),
	}
	for method, err := range errs {
		assert.ErrorIs(t, err, apperr.ErrUnavailable, method)
	}
}
