package usecase

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-logging/logging"
)

// TicketSaleAnnouncementUseCase announces newly discovered ticket sales to the
// fans tracking their series. An Organizer's sale is never announced.
type TicketSaleAnnouncementUseCase interface {
	// AnnounceDiscoveredSale requests an announcement of the given discovered
	// sale for each fan tracking its series, naming the sale and the tour and
	// linking to the fan's tracked event.
	//
	// A request without a series is a no-op (nil error). Only infrastructure
	// failures return a non-nil error.
	AnnounceDiscoveredSale(ctx context.Context, data entity.TicketSaleDiscoveredData) error
}

type ticketSaleAnnouncementUseCase struct {
	userRepo    entity.UserRepository
	journeyRepo entity.TicketJourneyRepository
	seriesRepo  entity.SeriesRepository
	workflows   Workflows
	logger      *logging.Logger
}

// Compile-time interface compliance check.
var _ TicketSaleAnnouncementUseCase = (*ticketSaleAnnouncementUseCase)(nil)

// NewTicketSaleAnnouncementUseCase wires the announcement use case.
func NewTicketSaleAnnouncementUseCase(
	userRepo entity.UserRepository,
	journeyRepo entity.TicketJourneyRepository,
	seriesRepo entity.SeriesRepository,
	workflows Workflows,
	logger *logging.Logger,
) *ticketSaleAnnouncementUseCase {
	return &ticketSaleAnnouncementUseCase{
		userRepo:    userRepo,
		journeyRepo: journeyRepo,
		seriesRepo:  seriesRepo,
		workflows:   workflows,
		logger:      logger,
	}
}

// AnnounceDiscoveredSale implements [TicketSaleAnnouncementUseCase].
func (uc *ticketSaleAnnouncementUseCase) AnnounceDiscoveredSale(ctx context.Context, data entity.TicketSaleDiscoveredData) error {
	if data.SeriesID == "" {
		uc.logger.Warn(ctx, "ticket_sale_announcement: empty series_id, skipping",
			slog.String("ticket_sale_id", data.TicketSaleID),
		)
		return nil
	}

	// The audience is every fan with a Tracking journey on an event of the
	// series, each with the event the announcement links them to.
	trackers, err := uc.journeyRepo.ListUserIDsTrackingSeries(ctx, data.SeriesID)
	if err != nil {
		return fmt.Errorf("ticket_sale_announcement: list tracking users: %w", err)
	}
	if len(trackers) == 0 {
		return nil
	}

	series, err := uc.seriesRepo.Get(ctx, data.SeriesID)
	if err != nil {
		return fmt.Errorf("ticket_sale_announcement: get series: %w", err)
	}
	sale := &entity.TicketSale{
		ID:            entity.TicketSaleID(data.TicketSaleID),
		SeriesID:      data.SeriesID,
		Method:        entity.TicketSaleMethod(data.Method),
		SaleStartTime: data.SaleStartTime,
	}

	// Request one announcement per audience member: start the
	// deliver-notification workflow, which records a durable Notification and
	// dispatches the push. This
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
			uc.logger.Warn(ctx, "ticket_sale_announcement: failed to hydrate user; skipping",
				slog.String("user_id", tr.UserID),
				slog.String("error", err.Error()),
			)
			continue
		}

		payload := buildAnnouncementPayload(sale, user, series.Title, tr.EventID)
		// Named after the recipient and the sale, so a repeated run notifies
		// a recipient once.
		id := deliverWorkflowID(entity.NotificationTypeTicketSaleAnnouncement, tr.UserID, data.TicketSaleID)
		if err := uc.workflows.Start(ctx, WorkflowDeliverNotification, id, DeliverRequest{
			UserID:  tr.UserID,
			Type:    entity.NotificationTypeTicketSaleAnnouncement,
			Payload: payload,
		}); err != nil {
			// Surface so the sale is announced again; the recipients already
			// started are not started again.
			return fmt.Errorf("ticket_sale_announcement: start the notification of user %s: %w", tr.UserID, err)
		}
	}
	return nil
}
