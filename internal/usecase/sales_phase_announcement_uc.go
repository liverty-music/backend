package usecase

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-logging/logging"
)

// SalesPhaseAnnouncementUseCase handles the announcement of newly discovered
// sales phases to the relevant followers.
type SalesPhaseAnnouncementUseCase interface {
	// AnnounceDiscoveredPhase resolves the audience for the given discovered
	// phase and requests an announcement for each fan tracking its series,
	// naming the sale and the tour and linking to the fan's tracked event.
	//
	// A request without a series is a no-op (nil error). Only infrastructure
	// failures return a non-nil error.
	AnnounceDiscoveredPhase(ctx context.Context, data entity.SalesPhaseDiscoveredData) error
}

type salesPhaseAnnouncementUseCase struct {
	userRepo    entity.UserRepository
	journeyRepo entity.TicketJourneyRepository
	seriesRepo  entity.SeriesRepository
	publisher   EventPublisher
	logger      *logging.Logger
}

// Compile-time interface compliance check.
var _ SalesPhaseAnnouncementUseCase = (*salesPhaseAnnouncementUseCase)(nil)

// NewSalesPhaseAnnouncementUseCase wires the announcement use case.
func NewSalesPhaseAnnouncementUseCase(
	userRepo entity.UserRepository,
	journeyRepo entity.TicketJourneyRepository,
	seriesRepo entity.SeriesRepository,
	publisher EventPublisher,
	logger *logging.Logger,
) *salesPhaseAnnouncementUseCase {
	return &salesPhaseAnnouncementUseCase{
		userRepo:    userRepo,
		journeyRepo: journeyRepo,
		seriesRepo:  seriesRepo,
		publisher:   publisher,
		logger:      logger,
	}
}

// AnnounceDiscoveredPhase implements [SalesPhaseAnnouncementUseCase].
func (uc *salesPhaseAnnouncementUseCase) AnnounceDiscoveredPhase(ctx context.Context, data entity.SalesPhaseDiscoveredData) error {
	if data.SeriesID == "" {
		uc.logger.Warn(ctx, "sales_phase_announcement: empty series_id, skipping",
			slog.String("phase_id", data.PhaseID),
		)
		return nil
	}

	trackers, err := ResolveSalesPhaseAudience(ctx, data.SeriesID, uc.journeyRepo)
	if err != nil {
		return fmt.Errorf("sales_phase_announcement: resolve audience: %w", err)
	}
	if len(trackers) == 0 {
		return nil
	}

	series, err := uc.seriesRepo.Get(ctx, data.SeriesID)
	if err != nil {
		return fmt.Errorf("sales_phase_announcement: get series: %w", err)
	}
	phase := &entity.SalesPhase{
		ID:             data.PhaseID,
		SeriesID:       data.SeriesID,
		Method:         entity.SalesMethod(data.Method),
		ApplyStartTime: data.ApplyStartTime,
	}

	// Request one announcement per audience member: publish NOTIFICATION.requested
	// (deterministic id — see notification_delivery.go) so the deliver-notification
	// consumer records a durable Notification and dispatches the push. This
	// announcement fires once immediately from the discovery job's daily
	// 21:00 JST run (no quiet-hours constraint); the copy follows the
	// recipient's language and time zone, and the link opens the recipient's
	// tracked event.
	for _, tr := range trackers {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// TODO(perf): batch user hydration when UserRepository gains ListByIDs.
		user, err := uc.userRepo.Get(ctx, tr.UserID)
		if err != nil {
			uc.logger.Warn(ctx, "sales_phase_announcement: failed to hydrate user; skipping",
				slog.String("user_id", tr.UserID),
				slog.String("error", err.Error()),
			)
			continue
		}

		payload := buildAnnouncementPayload(phase, user, series.Title, tr.EventID)
		// Deterministic id: same phase + same recipient always derives the same
		// id, so a retried SALES_PHASE.discovered delivery republishes an
		// identical NOTIFICATION.requested that the stream's Duplicates window
		// discards rather than requesting delivery twice.
		reqID := notificationRequestMsgID(entity.NotificationTypeSalesPhaseAnnouncement, tr.UserID, data.PhaseID)
		if err := uc.publisher.PublishEventWithID(ctx, entity.SubjectNotificationRequested, reqID, entity.NotificationRequestedData{
			UserID:  tr.UserID,
			Type:    entity.NotificationTypeSalesPhaseAnnouncement,
			Payload: payload,
		}); err != nil {
			// Publish failure: surface so the consumer's at-least-once retry
			// re-drives the batch. Repeat pushes are deduplicated both by the
			// Duplicates window above and, browser-side, by the per-phase Tag.
			return fmt.Errorf("sales_phase_announcement: publish notification request for user %s: %w", tr.UserID, err)
		}
	}
	return nil
}
