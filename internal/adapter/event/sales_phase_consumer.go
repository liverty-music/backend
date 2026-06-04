package event

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// SalesPhaseAnnouncementConsumer handles SALES_PHASE.discovered events by
// resolving the phase's covered events → performers → followers (same full
// ShouldNotify proximity filter as NotifyNewConcerts) and pushing an
// announcement via Web Push.
type SalesPhaseAnnouncementConsumer struct {
	salesPhaseRepo entity.SalesPhaseRepository
	concertRepo    entity.ConcertRepository
	followRepo     entity.FollowRepository
	pushSubRepo    entity.PushSubscriptionRepository
	sender         entity.PushNotificationSender
	metrics        usecase.PushMetrics
	logger         *logging.Logger
}

// NewSalesPhaseAnnouncementConsumer creates a new SalesPhaseAnnouncementConsumer.
func NewSalesPhaseAnnouncementConsumer(
	salesPhaseRepo entity.SalesPhaseRepository,
	concertRepo entity.ConcertRepository,
	followRepo entity.FollowRepository,
	pushSubRepo entity.PushSubscriptionRepository,
	sender entity.PushNotificationSender,
	metrics usecase.PushMetrics,
	logger *logging.Logger,
) *SalesPhaseAnnouncementConsumer {
	return &SalesPhaseAnnouncementConsumer{
		salesPhaseRepo: salesPhaseRepo,
		concertRepo:    concertRepo,
		followRepo:     followRepo,
		pushSubRepo:    pushSubRepo,
		sender:         sender,
		metrics:        metrics,
		logger:         logger,
	}
}

// Handle processes a SALES_PHASE.discovered event.
func (h *SalesPhaseAnnouncementConsumer) Handle(msg *message.Message) error {
	ctx := msg.Context()

	var data entity.SalesPhaseDiscoveredData
	if err := messaging.ParseCloudEventData(msg, &data); err != nil {
		h.logger.Error(ctx, "sales_phase_announcement: failed to parse event", err)
		return fmt.Errorf("parse SALES_PHASE.discovered: %w", err)
	}

	attrs := []slog.Attr{
		slog.String("phase_id", data.PhaseID),
		slog.String("series_id", data.SeriesID),
		slog.Int("covered_event_count", len(data.CoveredEventIDs)),
	}
	h.logger.Info(ctx, "sales_phase_announcement: processing", attrs...)

	if len(data.CoveredEventIDs) == 0 {
		h.logger.Warn(ctx, "sales_phase_announcement: no covered events, skipping",
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

	// Resolve audience using the shared helper — applies the same full
	// ShouldNotify proximity filter used by the reminder scan and
	// NotifyNewConcerts.
	_, userIDs, err := usecase.ResolveSalesPhaseAudience(ctx, phase, h.concertRepo, h.followRepo, attrs, h.logger)
	if err != nil {
		return fmt.Errorf("sales_phase_announcement: resolve audience: %w", err)
	}
	if len(userIDs) == 0 {
		return nil
	}

	subs, err := h.pushSubRepo.ListByUserIDs(ctx, userIDs)
	if err != nil {
		return fmt.Errorf("sales_phase_announcement: list subscriptions: %w", err)
	}
	if len(subs) == 0 {
		return nil
	}

	// Build a generic announcement payload. Per-user personalisation
	// (timezone, language) is intentionally omitted for the discovery
	// announcement — it fires once immediately from the daytime job
	// (no quiet-hours constraint) and a generic body suffices until a
	// richer notification UX is designed.
	//
	// TODO: swap to generated type after BSR gen (Refs #571) — use
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
		if err := h.sender.Send(ctx, payloadBytes, sub); err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				h.metrics.RecordPushSend(ctx, "gone")
				if delErr := h.pushSubRepo.Delete(ctx, sub.UserID, sub.Endpoint); delErr != nil {
					h.logger.Error(ctx, "sales_phase_announcement: delete stale sub failed", delErr,
						slog.String("user_id", sub.UserID),
					)
				}
			} else {
				h.metrics.RecordPushSend(ctx, "error")
				h.logger.Error(ctx, "sales_phase_announcement: send failed", err,
					slog.String("user_id", sub.UserID),
				)
			}
		} else {
			h.metrics.RecordPushSend(ctx, "success")
		}
	}
	return nil
}
