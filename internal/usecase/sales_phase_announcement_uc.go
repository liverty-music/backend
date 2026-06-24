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
	userRepo    entity.UserRepository
	journeyRepo entity.TicketJourneyRepository
	pushSubRepo entity.PushSubscriptionRepository
	sender      entity.PushNotificationSender
	metrics     PushMetrics
	logger      *logging.Logger
}

// Compile-time interface compliance check.
var _ SalesPhaseAnnouncementUseCase = (*salesPhaseAnnouncementUseCase)(nil)

// NewSalesPhaseAnnouncementUseCase wires the announcement use case.
func NewSalesPhaseAnnouncementUseCase(
	userRepo entity.UserRepository,
	journeyRepo entity.TicketJourneyRepository,
	pushSubRepo entity.PushSubscriptionRepository,
	sender entity.PushNotificationSender,
	metrics PushMetrics,
	logger *logging.Logger,
) *salesPhaseAnnouncementUseCase {
	return &salesPhaseAnnouncementUseCase{
		userRepo:    userRepo,
		journeyRepo: journeyRepo,
		pushSubRepo: pushSubRepo,
		sender:      sender,
		metrics:     metrics,
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

	subs, err := uc.pushSubRepo.ListByUserIDs(ctx, userIDs)
	if err != nil {
		return fmt.Errorf("sales_phase_announcement: list subscriptions: %w", err)
	}
	if len(subs) == 0 {
		return nil
	}

	url := fmt.Sprintf("/series/%s", data.SeriesID)
	tag := fmt.Sprintf("sales-phase-%s", data.PhaseID)

	// Build at most one payload per distinct recipient language. This announcement
	// fires once immediately from the daytime job (no quiet-hours constraint); only
	// the copy is personalised, by the recipient's preferred language (default en).
	payloadByLang := make(map[string][]byte)
	payloadFor := func(lang string) ([]byte, error) {
		if b, ok := payloadByLang[lang]; ok {
			return b, nil
		}
		b, err := json.Marshal(&entity.NotificationPayload{
			Title: announcementTitle(lang),
			Body:  announcementBody(lang),
			URL:   url,
			Tag:   tag,
		})
		if err != nil {
			return nil, err
		}
		payloadByLang[lang] = b
		return b, nil
	}

	for _, sub := range subs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		payloadBytes, err := payloadFor(langByUser[sub.UserID])
		if err != nil {
			return fmt.Errorf("sales_phase_announcement: marshal payload: %w", err)
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
