package di

import (
	"context"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// organizerReconcileInterval is how often the reconciler sweeps organizers stuck
// in the provisioning state and re-runs the (idempotent) provisioning saga.
const organizerReconcileInterval = 5 * time.Minute

// startOrganizerReconciler launches a background sweep that completes
// provisioning for any Organizer left in the provisioning state after a partial
// failure. It runs only in the workload that holds the real
// organizer-provisioner credential (the isolated admin workload) — callers gate
// this on that credential being configured. The sweep stops when ctx is
// canceled (the shutdown signal), so no separate drain phase is needed.
func startOrganizerReconciler(ctx context.Context, uc usecase.OrganizerUseCase, logger *logging.Logger) {
	go func() {
		ticker := time.NewTicker(organizerReconcileInterval)
		defer ticker.Stop()

		logger.Info(ctx, "organizer provisioning reconciler started",
			slog.Duration("interval", organizerReconcileInterval),
		)
		for {
			select {
			case <-ctx.Done():
				logger.Info(ctx, "organizer provisioning reconciler stopped")
				return
			case <-ticker.C:
				if err := uc.ReconcileProvisioning(ctx); err != nil {
					logger.Warn(ctx, "organizer provisioning reconcile sweep failed",
						slog.Any("error", err),
					)
				}
			}
		}
	}()
}
