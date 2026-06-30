package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// announcementTestDeps holds the mocks and use case for announcement tests.
type announcementTestDeps struct {
	userRepo       *entitymocks.MockUserRepository
	journeyRepo    *entitymocks.MockTicketJourneyRepository
	notificationUC *ucmocks.MockNotificationUseCase
	uc             usecase.SalesPhaseAnnouncementUseCase
}

func newAnnouncementTestDeps(t *testing.T) *announcementTestDeps {
	t.Helper()
	d := &announcementTestDeps{
		userRepo:       entitymocks.NewMockUserRepository(t),
		journeyRepo:    entitymocks.NewMockTicketJourneyRepository(t),
		notificationUC: ucmocks.NewMockNotificationUseCase(t),
	}
	d.uc = usecase.NewSalesPhaseAnnouncementUseCase(
		d.userRepo,
		d.journeyRepo,
		d.notificationUC,
		newTestLogger(t),
	)
	return d
}

// deliveredNotif returns a Notification with DeliveryStatus=Delivered for use
// as the mock return value in announcement tests.
func deliveredNotif() *entity.Notification {
	return &entity.Notification{ID: "notif-ann-1", DeliveryStatus: entity.NotificationDeliveryStatusDelivered}
}

func TestAnnounceDiscoveredPhase_LocalizesCopyPerRecipient(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newAnnouncementTestDeps(t)
	data := entity.SalesPhaseDiscoveredData{SeriesID: "series-1", PhaseID: "phase-1"}

	d.journeyRepo.EXPECT().
		ListUserIDsTrackingSeries(ctx, "series-1").
		Return([]string{"user-ja", "user-en", "user-unset"}, nil).
		Once()
	d.userRepo.EXPECT().Get(ctx, "user-ja").Return(&entity.User{ID: "user-ja", PreferredLanguage: "ja"}, nil).Once()
	d.userRepo.EXPECT().Get(ctx, "user-en").Return(&entity.User{ID: "user-en", PreferredLanguage: "en"}, nil).Once()
	d.userRepo.EXPECT().Get(ctx, "user-unset").Return(&entity.User{ID: "user-unset"}, nil).Once()

	// Assert each recipient gets a Notify call with the correct localized title,
	// language-independent URL, and per-phase Tag.
	d.notificationUC.EXPECT().
		Notify(anyCtx, "user-ja", entity.NotificationTypeSalesPhaseAnnouncement,
			mock.MatchedBy(func(p *entity.NotificationPayload) bool {
				return p.Title == "チケット販売情報の新着" &&
					p.URL == "/series/series-1" &&
					p.Tag == "sales-phase-phase-1"
			})).
		Return(deliveredNotif(), nil).
		Once()
	d.notificationUC.EXPECT().
		Notify(anyCtx, "user-en", entity.NotificationTypeSalesPhaseAnnouncement,
			mock.MatchedBy(func(p *entity.NotificationPayload) bool {
				return p.Title == "New Ticket Sales Phase" &&
					p.URL == "/series/series-1" &&
					p.Tag == "sales-phase-phase-1"
			})).
		Return(deliveredNotif(), nil).
		Once()
	d.notificationUC.EXPECT().
		Notify(anyCtx, "user-unset", entity.NotificationTypeSalesPhaseAnnouncement,
			mock.MatchedBy(func(p *entity.NotificationPayload) bool {
				// Unset language falls back to English.
				return p.Title == "New Ticket Sales Phase" &&
					p.URL == "/series/series-1" &&
					p.Tag == "sales-phase-phase-1"
			})).
		Return(deliveredNotif(), nil).
		Once()

	err := d.uc.AnnounceDiscoveredPhase(ctx, data)
	require.NoError(t, err)
}

func TestAnnounceDiscoveredPhase_HydrationError_SkipsButContinues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newAnnouncementTestDeps(t)
	data := entity.SalesPhaseDiscoveredData{SeriesID: "series-1", PhaseID: "phase-1"}

	d.journeyRepo.EXPECT().
		ListUserIDsTrackingSeries(ctx, "series-1").
		Return([]string{"user-ja", "user-broken"}, nil).
		Once()
	d.userRepo.EXPECT().Get(ctx, "user-ja").Return(&entity.User{ID: "user-ja", PreferredLanguage: "ja"}, nil).Once()
	// user-broken fails hydration; the use case logs a warning and continues.
	d.userRepo.EXPECT().Get(ctx, "user-broken").Return(nil, apperr.ErrInternal).Once()

	// Both audience members still receive a Notify call. user-broken falls back
	// to the English copy because it never made it into the language map.
	d.notificationUC.EXPECT().
		Notify(anyCtx, "user-ja", entity.NotificationTypeSalesPhaseAnnouncement,
			mock.MatchedBy(func(p *entity.NotificationPayload) bool {
				return p.Title == "チケット販売情報の新着"
			})).
		Return(deliveredNotif(), nil).
		Once()
	d.notificationUC.EXPECT().
		Notify(anyCtx, "user-broken", entity.NotificationTypeSalesPhaseAnnouncement,
			mock.MatchedBy(func(p *entity.NotificationPayload) bool {
				// Hydration failure falls back to English.
				return p.Title == "New Ticket Sales Phase"
			})).
		Return(deliveredNotif(), nil).
		Once()

	err := d.uc.AnnounceDiscoveredPhase(ctx, data)
	require.NoError(t, err)
}

func TestAnnounceDiscoveredPhase_EmptyAudience_NoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newAnnouncementTestDeps(t)
	data := entity.SalesPhaseDiscoveredData{SeriesID: "series-1", PhaseID: "phase-1"}

	d.journeyRepo.EXPECT().
		ListUserIDsTrackingSeries(ctx, "series-1").
		Return([]string{}, nil).
		Once()
	// No user hydration, no Notify calls expected.

	err := d.uc.AnnounceDiscoveredPhase(ctx, data)
	require.NoError(t, err)
	d.notificationUC.AssertNotCalled(t, "Notify")
}

func TestAnnounceDiscoveredPhase_NotifyError_PropagatesAndAborts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newAnnouncementTestDeps(t)
	data := entity.SalesPhaseDiscoveredData{SeriesID: "series-1", PhaseID: "phase-1"}

	d.journeyRepo.EXPECT().
		ListUserIDsTrackingSeries(ctx, "series-1").
		Return([]string{"user-1"}, nil).
		Once()
	d.userRepo.EXPECT().Get(ctx, "user-1").Return(&entity.User{ID: "user-1", PreferredLanguage: "en"}, nil).Once()

	notifyErr := errors.New("record creation failed")
	d.notificationUC.EXPECT().
		Notify(anyCtx, "user-1", entity.NotificationTypeSalesPhaseAnnouncement, mock.AnythingOfType("*entity.NotificationPayload")).
		Return(nil, notifyErr).
		Once()

	err := d.uc.AnnounceDiscoveredPhase(ctx, data)
	require.Error(t, err)
	assert.ErrorIs(t, err, notifyErr)
}
