package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

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
	userRepo    *entitymocks.MockUserRepository
	journeyRepo *entitymocks.MockTicketJourneyRepository
	seriesRepo  *entitymocks.MockSeriesRepository
	publisher   *ucmocks.MockEventPublisher
	uc          usecase.SalesPhaseAnnouncementUseCase
}

func newAnnouncementTestDeps(t *testing.T) *announcementTestDeps {
	t.Helper()
	d := &announcementTestDeps{
		userRepo:    entitymocks.NewMockUserRepository(t),
		journeyRepo: entitymocks.NewMockTicketJourneyRepository(t),
		seriesRepo:  entitymocks.NewMockSeriesRepository(t),
		publisher:   ucmocks.NewMockEventPublisher(t),
	}
	d.uc = usecase.NewSalesPhaseAnnouncementUseCase(
		d.userRepo,
		d.journeyRepo,
		d.seriesRepo,
		d.publisher,
		newTestLogger(t),
	)
	return d
}

// expectAnnouncementRequested sets up a PublishEventWithID expectation
// matching a NOTIFICATION.requested publish for the given recipient with a
// sales-phase-announcement payload satisfying payloadMatches.
func expectAnnouncementRequested(t *testing.T, publisher *ucmocks.MockEventPublisher, userID string, payloadMatches func(p *entity.NotificationPayload) bool) *mock.Call {
	t.Helper()
	return publisher.EXPECT().
		PublishEventWithID(anyCtx, entity.SubjectNotificationRequested, mock.AnythingOfType("string"),
			mock.MatchedBy(func(data entity.NotificationRequestedData) bool {
				return data.UserID == userID && data.Type == entity.NotificationTypeSalesPhaseAnnouncement && payloadMatches(data.Payload)
			})).
		Return(nil).
		Once()
}

// announcementJST is Asia/Tokyo for building phase times.
var announcementJST = time.FixedZone("JST", 9*60*60)

// discoveredLottery is a lottery opening on 5 October 2026 18:00 JST.
func discoveredLottery() entity.SalesPhaseDiscoveredData {
	return entity.SalesPhaseDiscoveredData{
		SeriesID:       "series-1",
		PhaseID:        "phase-1",
		Method:         int16(entity.SalesMethodLottery),
		ApplyStartTime: time.Date(2026, 10, 5, 18, 0, 0, 0, announcementJST),
	}
}

func trackers(userEvents ...string) []*entity.SeriesTracker {
	out := make([]*entity.SeriesTracker, 0, len(userEvents)/2)
	for i := 0; i+1 < len(userEvents); i += 2 {
		out = append(out, &entity.SeriesTracker{UserID: userEvents[i], EventID: userEvents[i+1]})
	}
	return out
}

// @spec components/usecase/sales-phase/announce-discovered-phase "Two recipients"
// @spec components/usecase/sales-phase/announce-discovered-phase "Tracking fan"
// @spec components/usecase/sales-phase/announce-discovered-phase "Two fans tracking different shows"
func TestAnnounceDiscoveredPhase_OneRequestPerRecipient(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newAnnouncementTestDeps(t)
	d.journeyRepo.EXPECT().
		ListUserIDsTrackingSeries(ctx, "series-1").
		Return(trackers("fan-a", "event-osaka", "fan-b", "event-tokyo"), nil).
		Once()
	d.seriesRepo.EXPECT().Get(ctx, "series-1").Return(&entity.Series{ID: "series-1", Title: "KICKOFF"}, nil).Once()
	d.userRepo.EXPECT().Get(ctx, "fan-a").Return(&entity.User{ID: "fan-a", PreferredLanguage: "ja"}, nil).Once()
	d.userRepo.EXPECT().Get(ctx, "fan-b").Return(&entity.User{ID: "fan-b", PreferredLanguage: "en"}, nil).Once()

	expectAnnouncementRequested(t, d.publisher, "fan-a", func(p *entity.NotificationPayload) bool {
		return p.Data[entity.NotificationDataKeyURL] == "/concerts/event-osaka" && p.Tag == "sales-phase-phase-1"
	})
	expectAnnouncementRequested(t, d.publisher, "fan-b", func(p *entity.NotificationPayload) bool {
		return p.Data[entity.NotificationDataKeyURL] == "/concerts/event-tokyo" && p.Tag == "sales-phase-phase-1"
	})

	require.NoError(t, d.uc.AnnounceDiscoveredPhase(ctx, discoveredLottery()))
}

// @spec components/usecase/sales-phase/announce-discovered-phase "Profile unreadable"
func TestAnnounceDiscoveredPhase_HydrationError_SkipsButContinues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newAnnouncementTestDeps(t)
	d.journeyRepo.EXPECT().
		ListUserIDsTrackingSeries(ctx, "series-1").
		Return(trackers("user-ja", "event-1", "user-broken", "event-1"), nil).
		Once()
	d.seriesRepo.EXPECT().Get(ctx, "series-1").Return(&entity.Series{ID: "series-1", Title: "KICKOFF"}, nil).Once()
	d.userRepo.EXPECT().Get(ctx, "user-ja").Return(&entity.User{ID: "user-ja", PreferredLanguage: "ja"}, nil).Once()
	d.userRepo.EXPECT().Get(ctx, "user-broken").Return(nil, apperr.ErrInternal).Once()

	// Only the readable fan gets a request; a request for user-broken would
	// fail the mock.
	expectAnnouncementRequested(t, d.publisher, "user-ja", func(p *entity.NotificationPayload) bool {
		return p.Title == "チケット抽選受付のお知らせ"
	})

	require.NoError(t, d.uc.AnnounceDiscoveredPhase(ctx, discoveredLottery()))
}

// @spec components/usecase/sales-phase/announce-discovered-phase "Nobody tracking"
func TestAnnounceDiscoveredPhase_EmptyAudience_NoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newAnnouncementTestDeps(t)
	d.journeyRepo.EXPECT().
		ListUserIDsTrackingSeries(ctx, "series-1").
		Return(nil, nil).
		Once()
	// No series read, user hydration or publish expected.

	require.NoError(t, d.uc.AnnounceDiscoveredPhase(ctx, discoveredLottery()))
}

// @spec components/usecase/sales-phase/announce-discovered-phase "Request without a series"
func TestAnnounceDiscoveredPhase_NoSeries_NoOp(t *testing.T) {
	t.Parallel()

	d := newAnnouncementTestDeps(t)
	require.NoError(t, d.uc.AnnounceDiscoveredPhase(context.Background(), entity.SalesPhaseDiscoveredData{PhaseID: "phase-1"}))
}

// @spec components/usecase/sales-phase/announce-discovered-phase "Request fails"
func TestAnnounceDiscoveredPhase_PublishError_PropagatesAndAborts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newAnnouncementTestDeps(t)
	d.journeyRepo.EXPECT().
		ListUserIDsTrackingSeries(ctx, "series-1").
		Return(trackers("user-1", "e", "user-2", "e", "user-3", "e"), nil).
		Once()
	d.seriesRepo.EXPECT().Get(ctx, "series-1").Return(&entity.Series{ID: "series-1", Title: "KICKOFF"}, nil).Once()
	for _, uid := range []string{"user-1", "user-2"} {
		d.userRepo.EXPECT().Get(ctx, uid).Return(&entity.User{ID: uid, PreferredLanguage: "en"}, nil).Once()
	}

	requestedFor := func(userID string) any {
		return mock.MatchedBy(func(data entity.NotificationRequestedData) bool { return data.UserID == userID })
	}
	publishErr := errors.New("nats unavailable")
	d.publisher.EXPECT().
		PublishEventWithID(anyCtx, entity.SubjectNotificationRequested, mock.AnythingOfType("string"), requestedFor("user-1")).
		Return(nil).
		Once()
	d.publisher.EXPECT().
		PublishEventWithID(anyCtx, entity.SubjectNotificationRequested, mock.AnythingOfType("string"), requestedFor("user-2")).
		Return(publishErr).
		Once()
	// No request is expected for user-3: a third publish would fail the mock.

	err := d.uc.AnnounceDiscoveredPhase(ctx, discoveredLottery())
	require.Error(t, err)
	assert.ErrorIs(t, err, publishErr)
	d.publisher.AssertNumberOfCalls(t, "PublishEventWithID", 2)
}

func TestBuildAnnouncementPayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		phase     *entity.SalesPhase
		user      *entity.User
		tour      string
		wantTitle string
		wantText  string
	}{
		{
			// @spec components/usecase/sales-phase/announce-discovered-phase "First-come sale in Japanese"
			name: "First-come sale in Japanese",
			phase: &entity.SalesPhase{ID: "p", Method: entity.SalesMethodFirstCome,
				ApplyStartTime: time.Date(2026, 10, 6, 19, 0, 0, 0, announcementJST)},
			user:      &entity.User{PreferredLanguage: "ja", TimeZone: "Asia/Tokyo"},
			tour:      "星降る晩餐会",
			wantTitle: "チケット先着販売のお知らせ",
			wantText:  "10/6(火) 19:00からチケットの先着販売スタート!!\n星降る晩餐会",
		},
		{
			// @spec components/usecase/sales-phase/announce-discovered-phase "Lottery in English"
			name: "Lottery in English",
			phase: &entity.SalesPhase{ID: "p", Method: entity.SalesMethodLottery,
				ApplyStartTime: time.Date(2026, 10, 5, 18, 0, 0, 0, announcementJST)},
			user:      &entity.User{PreferredLanguage: "fr"},
			tour:      "KICKOFF",
			wantTitle: "Ticket Lottery",
			wantText:  "Ticket lottery entry starts Oct 5 (Mon) 18:00!\nKICKOFF",
		},
		{
			name: "lottery in Japanese",
			phase: &entity.SalesPhase{ID: "p", Method: entity.SalesMethodLottery,
				ApplyStartTime: time.Date(2026, 10, 5, 18, 0, 0, 0, announcementJST)},
			user:      &entity.User{PreferredLanguage: "ja"},
			tour:      "KICKOFF",
			wantTitle: "チケット抽選受付のお知らせ",
			wantText:  "10/5(月) 18:00からチケットの抽選申し込みスタート!!\nKICKOFF",
		},
		{
			name: "first-come in English, in the recipient's time zone",
			phase: &entity.SalesPhase{ID: "p", Method: entity.SalesMethodFirstCome,
				ApplyStartTime: time.Date(2026, 10, 6, 19, 0, 0, 0, announcementJST)},
			user:      &entity.User{PreferredLanguage: "en", TimeZone: "Asia/Taipei"},
			tour:      "Tour",
			wantTitle: "First-Come Ticket Sale",
			wantText:  "Ticket sale (first come) starts Oct 6 (Tue) 18:00!\nTour",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := usecase.ExportedBuildAnnouncementPayload(tt.phase, tt.user, tt.tour, "event-1")
			assert.Equal(t, tt.wantTitle, p.Title)
			assert.Equal(t, tt.wantText, p.Body)
			assert.Equal(t, "/concerts/event-1", p.Data[entity.NotificationDataKeyURL])
			assert.Equal(t, "sales-phase-p", p.Tag)
		})
	}
}
