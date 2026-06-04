package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// SalesPhaseAnnouncementUseCase handles the announcement of newly discovered
// sales phases to the relevant followers.
type SalesPhaseAnnouncementUseCase interface {
	// AnnounceDiscoveredPhase resolves the audience for the given discovered
	// phase and pushes an announcement to all eligible followers.
	//
	// An empty covered-event list is a no-op (nil error). Only infrastructure
	// failures return a non-nil error.
	AnnounceDiscoveredPhase(ctx context.Context, data entity.SalesPhaseDiscoveredData) error
}

type salesPhaseAnnouncementUseCase struct {
	concertRepo entity.ConcertRepository
	followRepo  entity.FollowRepository
	pushSubRepo entity.PushSubscriptionRepository
	sender      entity.PushNotificationSender
	metrics     PushMetrics
	logger      *logging.Logger
}

// Compile-time interface compliance check.
var _ SalesPhaseAnnouncementUseCase = (*salesPhaseAnnouncementUseCase)(nil)

// NewSalesPhaseAnnouncementUseCase wires the announcement use case.
func NewSalesPhaseAnnouncementUseCase(
	concertRepo entity.ConcertRepository,
	followRepo entity.FollowRepository,
	pushSubRepo entity.PushSubscriptionRepository,
	sender entity.PushNotificationSender,
	metrics PushMetrics,
	logger *logging.Logger,
) *salesPhaseAnnouncementUseCase {
	return &salesPhaseAnnouncementUseCase{
		concertRepo: concertRepo,
		followRepo:  followRepo,
		pushSubRepo: pushSubRepo,
		sender:      sender,
		metrics:     metrics,
		logger:      logger,
	}
}

// AnnounceDiscoveredPhase implements [SalesPhaseAnnouncementUseCase].
func (uc *salesPhaseAnnouncementUseCase) AnnounceDiscoveredPhase(ctx context.Context, data entity.SalesPhaseDiscoveredData) error {
	attrs := []slog.Attr{
		slog.String("phase_id", data.PhaseID),
		slog.String("series_id", data.SeriesID),
		slog.Int("covered_event_count", len(data.CoveredEventIDs)),
	}

	if len(data.CoveredEventIDs) == 0 {
		uc.logger.Warn(ctx, "sales_phase_announcement: no covered events, skipping",
			slog.String("phase_id", data.PhaseID),
		)
		return nil
	}

	// Build a minimal SalesPhase so the shared audience resolver can work with
	// the event payload (which only carries PhaseID + CoveredEventIDs).
	phase := &entity.SalesPhase{
		ID:              data.PhaseID,
		SeriesID:        data.SeriesID,
		CoveredEventIDs: data.CoveredEventIDs,
	}

	_, userIDs, err := ResolveSalesPhaseAudience(ctx, phase, uc.concertRepo, uc.followRepo, attrs, uc.logger)
	if err != nil {
		return fmt.Errorf("sales_phase_announcement: resolve audience: %w", err)
	}
	if len(userIDs) == 0 {
		return nil
	}

	subs, err := uc.pushSubRepo.ListByUserIDs(ctx, userIDs)
	if err != nil {
		return fmt.Errorf("sales_phase_announcement: list subscriptions: %w", err)
	}
	if len(subs) == 0 {
		return nil
	}

	// Build a generic announcement payload. Per-user personalisation
	// (timezone, language) is intentionally omitted — it fires once
	// immediately from the daytime job (no quiet-hours constraint) and a
	// generic body suffices until a richer notification UX is designed.
	//
	// TODO: swap to generated type after BSR gen (Refs #571) — use the
	// proto-generated series title from the phase once BSR types land.
	payload := &entity.NotificationPayload{
		Title: "New Ticket Sales Phase",
		Body:  "A new ticket sales phase was announced. Check the details.",
		URL:   fmt.Sprintf("/series/%s", data.SeriesID),
		Tag:   fmt.Sprintf("sales-phase-%s", data.PhaseID),
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("sales_phase_announcement: marshal payload: %w", err)
	}

	for _, sub := range subs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := uc.sender.Send(ctx, payloadBytes, sub); err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				uc.metrics.RecordPushSend(ctx, "gone")
				if delErr := uc.pushSubRepo.Delete(ctx, sub.UserID, sub.Endpoint); delErr != nil {
					uc.logger.Error(ctx, "sales_phase_announcement: delete stale sub failed", delErr,
						slog.String("user_id", sub.UserID),
					)
				}
			} else {
				uc.metrics.RecordPushSend(ctx, "error")
				uc.logger.Error(ctx, "sales_phase_announcement: send failed", err,
					slog.String("user_id", sub.UserID),
				)
			}
		} else {
			uc.metrics.RecordPushSend(ctx, "success")
		}
	}
	return nil
}
