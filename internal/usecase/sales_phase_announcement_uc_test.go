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
	concertRepo *entitymocks.MockConcertRepository
	publisher   *ucmocks.MockEventPublisher
	uc          usecase.SalesPhaseAnnouncementUseCase
}

func newAnnouncementTestDeps(t *testing.T) *announcementTestDeps {
	t.Helper()
	d := &announcementTestDeps{
		userRepo:    entitymocks.NewMockUserRepository(t),
		journeyRepo: entitymocks.NewMockTicketJourneyRepository(t),
		concertRepo: entitymocks.NewMockConcertRepository(t),
		publisher:   ucmocks.NewMockEventPublisher(t),
	}
	d.uc = usecase.NewSalesPhaseAnnouncementUseCase(
		d.userRepo,
		d.journeyRepo,
		d.concertRepo,
		d.publisher,
		newTestLogger(t),
	)
	return d
}

// expectAnnouncementRequested sets up a PublishEventWithID expectation
// matching a NOTIFICATION.requested publish for the given recipient with a
// sales-phase-announcement payload satisfying payloadMatches. AnnounceDiscoveredPhase
// now requests delivery per recipient instead of calling NotificationUseCase.Deliver
// directly, so these tests assert on the publish rather than on a mocked Notify call.
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

// @spec components/usecase/sales-phase/announce-discovered-phase "Two recipients"
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
	d.concertRepo.EXPECT().
		ListEventsBySeries(ctx, "series-1").
		Return([]*entity.Event{{ID: "event-1", LocalDate: time.Now().UTC().AddDate(0, 0, 7)}}, nil).
		Once()

	// Assert each recipient's requested payload carries the correct localized
	// title, language-independent URL (the series' resolved event, not the
	// phase itself), and per-phase Tag.
	// @spec components/usecase/sales-phase/announce-discovered-phase "Japanese-speaking fan"
	expectAnnouncementRequested(t, d.publisher, "user-ja", func(p *entity.NotificationPayload) bool {
		return p.Title == "チケット販売情報の新着" &&
			p.Data[entity.NotificationDataKeyURL] == "/concerts/event-1" &&
			p.Tag == "sales-phase-phase-1"
	})
	expectAnnouncementRequested(t, d.publisher, "user-en", func(p *entity.NotificationPayload) bool {
		return p.Title == "New Ticket Sales Phase" &&
			p.Data[entity.NotificationDataKeyURL] == "/concerts/event-1" &&
			p.Tag == "sales-phase-phase-1"
	})
	// @spec components/usecase/sales-phase/announce-discovered-phase "Other language"
	// user-unset has no preferred language, matching the scenario's "or not set".
	expectAnnouncementRequested(t, d.publisher, "user-unset", func(p *entity.NotificationPayload) bool {
		// Unset language falls back to English.
		return p.Title == "New Ticket Sales Phase" &&
			p.Data[entity.NotificationDataKeyURL] == "/concerts/event-1" &&
			p.Tag == "sales-phase-phase-1"
	})

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
	// The series has no event: the link falls back to the dashboard. Not
	// asserted below since this test only checks title localization.
	d.concertRepo.EXPECT().ListEventsBySeries(ctx, "series-1").Return(nil, nil).Once()

	// Both audience members still get a requested notification. user-broken
	// falls back to the English copy because it never made it into the
	// language map.
	expectAnnouncementRequested(t, d.publisher, "user-ja", func(p *entity.NotificationPayload) bool {
		return p.Title == "チケット販売情報の新着"
	})
	expectAnnouncementRequested(t, d.publisher, "user-broken", func(p *entity.NotificationPayload) bool {
		// Hydration failure falls back to English.
		return p.Title == "New Ticket Sales Phase"
	})

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
	// No user hydration, no publish calls expected.

	err := d.uc.AnnounceDiscoveredPhase(ctx, data)
	require.NoError(t, err)
	d.publisher.AssertNotCalled(t, "PublishEventWithID")
}

// @spec components/usecase/sales-phase/announce-discovered-phase "Request fails"
func TestAnnounceDiscoveredPhase_PublishError_PropagatesAndAborts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newAnnouncementTestDeps(t)
	data := entity.SalesPhaseDiscoveredData{SeriesID: "series-1", PhaseID: "phase-1"}

	d.journeyRepo.EXPECT().
		ListUserIDsTrackingSeries(ctx, "series-1").
		Return([]string{"user-1", "user-2", "user-3"}, nil).
		Once()
	for _, uid := range []string{"user-1", "user-2", "user-3"} {
		d.userRepo.EXPECT().Get(ctx, uid).Return(&entity.User{ID: uid, PreferredLanguage: "en"}, nil).Once()
	}
	d.concertRepo.EXPECT().ListEventsBySeries(ctx, "series-1").Return(nil, nil).Once()

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

	err := d.uc.AnnounceDiscoveredPhase(ctx, data)
	require.Error(t, err)
	assert.ErrorIs(t, err, publishErr)
	d.publisher.AssertNumberOfCalls(t, "PublishEventWithID", 2)
}

// @spec components/usecase/sales-phase/announce-discovered-phase "No upcoming event"
func TestAnnounceDiscoveredPhase_NoUpcomingEvent_LinksToEarliestEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newAnnouncementTestDeps(t)
	data := entity.SalesPhaseDiscoveredData{SeriesID: "series-1", PhaseID: "phase-1"}

	d.journeyRepo.EXPECT().
		ListUserIDsTrackingSeries(ctx, "series-1").
		Return([]string{"user-1"}, nil).
		Once()
	d.userRepo.EXPECT().Get(ctx, "user-1").Return(&entity.User{ID: "user-1", PreferredLanguage: "en"}, nil).Once()
	// Every event of the series is in the past: the more recent one is
	// nonetheless the "earliest" of the two, and is the one linked to.
	past := time.Now().UTC().AddDate(0, 0, -30)
	moreRecentPast := time.Now().UTC().AddDate(0, 0, -1)
	d.concertRepo.EXPECT().
		ListEventsBySeries(ctx, "series-1").
		Return([]*entity.Event{
			{ID: "event-oldest", LocalDate: past},
			{ID: "event-recent", LocalDate: moreRecentPast},
		}, nil).
		Once()

	expectAnnouncementRequested(t, d.publisher, "user-1", func(p *entity.NotificationPayload) bool {
		return p.Data[entity.NotificationDataKeyURL] == "/concerts/event-oldest"
	})

	err := d.uc.AnnounceDiscoveredPhase(ctx, data)
	require.NoError(t, err)
}

// @spec components/usecase/sales-phase/announce-discovered-phase "Series with no event"
func TestAnnounceDiscoveredPhase_SeriesWithNoEvent_LinksToDashboard(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newAnnouncementTestDeps(t)
	data := entity.SalesPhaseDiscoveredData{SeriesID: "series-1", PhaseID: "phase-1"}

	d.journeyRepo.EXPECT().
		ListUserIDsTrackingSeries(ctx, "series-1").
		Return([]string{"user-1"}, nil).
		Once()
	d.userRepo.EXPECT().Get(ctx, "user-1").Return(&entity.User{ID: "user-1", PreferredLanguage: "en"}, nil).Once()
	d.concertRepo.EXPECT().ListEventsBySeries(ctx, "series-1").Return(nil, nil).Once()

	expectAnnouncementRequested(t, d.publisher, "user-1", func(p *entity.NotificationPayload) bool {
		return p.Data[entity.NotificationDataKeyURL] == "/dashboard"
	})

	err := d.uc.AnnounceDiscoveredPhase(ctx, data)
	require.NoError(t, err)
}
