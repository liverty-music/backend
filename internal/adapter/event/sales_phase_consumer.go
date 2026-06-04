package event

import (
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// SalesPhaseAnnouncementConsumer handles SALES_PHASE.discovered events by
// delegating to the announcement use case. It is a thin adapter: parse the
// CloudEvent and hand off to the use case — no repository lookups or business
// logic here.
type SalesPhaseAnnouncementConsumer struct {
	announcementUC usecase.SalesPhaseAnnouncementUseCase
	logger         *logging.Logger
}

// NewSalesPhaseAnnouncementConsumer creates a new SalesPhaseAnnouncementConsumer.
func NewSalesPhaseAnnouncementConsumer(
	announcementUC usecase.SalesPhaseAnnouncementUseCase,
	logger *logging.Logger,
) *SalesPhaseAnnouncementConsumer {
	return &SalesPhaseAnnouncementConsumer{
		announcementUC: announcementUC,
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

	h.logger.Info(ctx, "sales_phase_announcement: processing",
		slog.String("phase_id", data.PhaseID),
		slog.String("series_id", data.SeriesID),
		slog.Int("covered_event_count", len(data.CoveredEventIDs)),
	)

	if err := h.announcementUC.AnnounceDiscoveredPhase(ctx, data); err != nil {
		return fmt.Errorf("sales_phase_announcement: %w", err)
	}
	return nil
}
