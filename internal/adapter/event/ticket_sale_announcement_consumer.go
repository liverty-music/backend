package event

import (
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
)

// TicketSaleAnnouncementConsumer handles SALES_PHASE.discovered events by
// delegating to the announcement use case. It is a thin adapter: parse the
// CloudEvent and hand off to the use case — no repository lookups or business
// logic here.
type TicketSaleAnnouncementConsumer struct {
	announcementUC usecase.TicketSaleAnnouncementUseCase
	logger         *logging.Logger
}

// NewTicketSaleAnnouncementConsumer creates a new TicketSaleAnnouncementConsumer.
func NewTicketSaleAnnouncementConsumer(
	announcementUC usecase.TicketSaleAnnouncementUseCase,
	logger *logging.Logger,
) *TicketSaleAnnouncementConsumer {
	return &TicketSaleAnnouncementConsumer{
		announcementUC: announcementUC,
		logger:         logger,
	}
}

// Handle processes a SALES_PHASE.discovered event.
func (h *TicketSaleAnnouncementConsumer) Handle(msg *message.Message) error {
	ctx := msg.Context()

	data, err := parseTicketSaleDiscovered(msg)
	if err != nil {
		h.logger.Error(ctx, "ticket_sale_announcement: failed to parse event", err)
		return fmt.Errorf("parse SALES_PHASE.discovered: %w", err)
	}

	h.logger.Info(ctx, "ticket_sale_announcement: processing",
		slog.String("ticket_sale_id", data.TicketSaleID),
		slog.String("series_id", data.SeriesID),
	)

	if err := h.announcementUC.AnnounceDiscoveredSale(ctx, data); err != nil {
		return fmt.Errorf("ticket_sale_announcement: %w", err)
	}
	return nil
}
