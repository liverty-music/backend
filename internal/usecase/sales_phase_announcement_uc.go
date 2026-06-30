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
	// phase and pushes an announcement to all eligible followers.
	//
	// An empty covered-event list is a no-op (nil error). Only infrastructure
	// failures return a non-nil error.
	AnnounceDiscoveredPhase(ctx context.Context, data entity.SalesPhaseDiscoveredData) error
}

type salesPhaseAnnouncementUseCase struct {
	userRepo       entity.UserRepository
	journeyRepo    entity.TicketJourneyRepository
	notificationUC NotificationUseCase
	logger         *logging.Logger
}

// Compile-time interface compliance check.
var _ SalesPhaseAnnouncementUseCase = (*salesPhaseAnnouncementUseCase)(nil)

// NewSalesPhaseAnnouncementUseCase wires the announcement use case.
func NewSalesPhaseAnnouncementUseCase(
	userRepo entity.UserRepository,
	journeyRepo entity.TicketJourneyRepository,
	notificationUC NotificationUseCase,
	logger *logging.Logger,
) *salesPhaseAnnouncementUseCase {
	return &salesPhaseAnnouncementUseCase{
		userRepo:       userRepo,
		journeyRepo:    journeyRepo,
		notificationUC: notificationUC,
		logger:         logger,
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

	userIDs, err := ResolveSalesPhaseAudience(ctx, data.SeriesID, uc.journeyRepo)
	if err != nil {
		return fmt.Errorf("sales_phase_announcement: resolve audience: %w", err)
	}
	if len(userIDs) == 0 {
		return nil
	}

	// Hydrate recipients to localize the announcement copy by preferred language.
	// TODO(perf): batch user hydration when UserRepository gains ListByIDs.
	langByUser := make(map[string]string, len(userIDs))
	for _, uid := range userIDs {
		u, err := uc.userRepo.Get(ctx, uid)
		if err != nil {
			uc.logger.Warn(ctx, "sales_phase_announcement: failed to hydrate user; skipping",
				slog.String("user_id", uid),
				slog.String("error", err.Error()),
			)
			continue
		}
		langByUser[uid] = u.PreferredLanguage
	}

	url := fmt.Sprintf("/series/%s", data.SeriesID)
	tag := fmt.Sprintf("sales-phase-%s", data.PhaseID)

	// Record and dispatch one announcement per audience member through the
	// notification service, so every recipient gets a durable record and a
	// delivery outcome. This announcement fires once immediately from the daytime
	// job (no quiet-hours constraint); only the copy is personalised, by the
	// recipient's preferred language (default en).
	for _, userID := range userIDs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		lang := langByUser[userID]
		payload := &entity.NotificationPayload{
			Title: announcementTitle(lang),
			Body:  announcementBody(lang),
			URL:   url,
			Tag:   tag,
		}
		if _, err := uc.notificationUC.Notify(ctx, userID, entity.NotificationTypeSalesPhaseAnnouncement, payload); err != nil {
			// Record-create failure ("no record => no send"): surface so the
			// consumer's at-least-once retry re-drives the batch. Repeat pushes
			// are deduplicated browser-side by the per-phase Tag.
			return fmt.Errorf("sales_phase_announcement: notify user %s: %w", userID, err)
		}
	}
	return nil
}

// announcementTitle returns the new-phase announcement title for the given
// language, falling back to English for empty or unsupported codes.
func announcementTitle(lang string) string {
	switch lang {
	case "ja":
		return "チケット販売情報の新着"
	default:
		return "New Ticket Sales Phase"
	}
}

// announcementBody returns the new-phase announcement body for the given
// language, falling back to English for empty or unsupported codes.
func announcementBody(lang string) string {
	switch lang {
	case "ja":
		return "チケットの販売情報が新たに公開されました。詳細をご確認ください。"
	default:
		return "A new ticket sales phase was announced. Check the details."
	}
}
