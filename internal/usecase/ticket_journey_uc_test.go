package usecase_test

import (
	"context"
	"testing"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
)

type ticketJourneyTestDeps struct {
	repo     *mocks.MockTicketJourneyRepository
	users    *mocks.MockUserRepository
	concerts *mocks.MockConcertRepository
	follows  *mocks.MockFollowRepository
	entries  *mocks.MockLotteryEntryRepository
	uc       usecase.TicketJourneyUseCase
}

func newTicketJourneyTestDeps(t *testing.T) *ticketJourneyTestDeps {
	t.Helper()
	d := &ticketJourneyTestDeps{
		repo:     mocks.NewMockTicketJourneyRepository(t),
		users:    mocks.NewMockUserRepository(t),
		concerts: mocks.NewMockConcertRepository(t),
		follows:  mocks.NewMockFollowRepository(t),
		entries:  mocks.NewMockLotteryEntryRepository(t),
	}
	d.uc = usecase.NewTicketJourneyUseCase(d.repo, d.users, d.concerts, d.follows, d.entries, newTestLogger(t))
	return d
}

// raiseFixture is a fan with home JP-13 (Tokyo) and concerts of artist-1:
// event-far in JP-40 (Fukuoka, Away) and event-near in JP-14 (Yokohama,
// Nearby).
var (
	tokyoLevel1  = "JP-13"
	tokyoHome    = &entity.Home{CountryCode: "JP", Level1: tokyoLevel1, Centroid: &entity.Coordinates{Latitude: 35.6762, Longitude: 139.6503}}
	fukuokaLevel = "JP-40"
	yokohamaArea = "JP-14"
)

func raiseConcert(eventID string, artistIDs ...string) *entity.Concert {
	c := &entity.Concert{ID: eventID}
	switch eventID {
	case "event-near":
		c.Venue = &entity.Venue{AdminArea: &yokohamaArea, Coordinates: &entity.Coordinates{Latitude: 35.4437, Longitude: 139.6380}}
	default:
		c.Venue = &entity.Venue{AdminArea: &fukuokaLevel, Coordinates: &entity.Coordinates{Latitude: 33.5902, Longitude: 130.4017}}
	}
	for _, id := range artistIDs {
		c.Artists = append(c.Artists, &entity.Artist{ID: id})
	}
	return c
}

func followed(artistID string, h entity.Hype, by entity.HypeSetBy) *entity.FollowedArtist {
	return &entity.FollowedArtist{UserID: "user-1", Artist: &entity.Artist{ID: artistID}, Hype: h, HypeSetBy: by}
}

// expectRaiseReads stubs the reads of the hype raise for user-1 and eventID.
func (d *ticketJourneyTestDeps) expectRaiseReads(ctx context.Context, eventID string, concert *entity.Concert, follows ...*entity.FollowedArtist) {
	d.users.EXPECT().Get(ctx, "user-1").Return(&entity.User{ID: "user-1", Home: tokyoHome}, nil).Once()
	var concerts []*entity.Concert
	if concert != nil {
		concerts = []*entity.Concert{concert}
	}
	d.concerts.EXPECT().ListByIDs(ctx, []string{eventID}).Return(concerts, nil).Once()
	if concert != nil {
		d.follows.EXPECT().ListByUser(ctx, "user-1").Return(follows, nil).Once()
	}
}

func TestTicketJourneyUseCase_SetStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	journey := func(s entity.TicketJourneyStatus) *entity.TicketJourney {
		return &entity.TicketJourney{UserID: "user-1", EventID: "event-far", Status: s, StatusSetBy: entity.TicketJourneySetByFan}
	}

	tests := []struct {
		name    string
		status  entity.TicketJourneyStatus
		upsert  error
		setup   func(d *ticketJourneyTestDeps)
		wantErr error
	}{
		{
			// @spec components/usecase/ticket-journey/set-status "First status for an event"
			name:   "first status for an event",
			status: entity.TicketJourneyStatusTracking,
		},
		{
			// @spec components/usecase/ticket-journey/set-status "Changing an existing status"
			name:   "changing an existing status",
			status: entity.TicketJourneyStatusUnpaid,
			setup: func(d *ticketJourneyTestDeps) {
				d.expectRaiseReads(ctx, "event-far", raiseConcert("event-far", "artist-1"), followed("artist-1", entity.HypeAway, entity.HypeSetByFan))
			},
		},
		{
			// @spec components/usecase/ticket-journey/set-status "Recording the status fails"
			name:    "recording the status fails",
			status:  entity.TicketJourneyStatusApplied,
			upsert:  apperr.ErrUnavailable,
			wantErr: apperr.ErrUnavailable,
		},
		{
			// @spec components/usecase/ticket-journey/set-status "Applied for a distant concert"
			name:   "applied for a distant concert",
			status: entity.TicketJourneyStatusApplied,
			setup: func(d *ticketJourneyTestDeps) {
				d.expectRaiseReads(ctx, "event-far", raiseConcert("event-far", "artist-1"), followed("artist-1", entity.HypeNearby, entity.HypeSetByDefault))
				d.follows.EXPECT().RaiseHype(ctx, "user-1", "artist-1", entity.HypeAway).Return(true, nil).Once()
			},
		},
		{
			// @spec components/usecase/ticket-journey/set-status "Level the fan set"
			name:   "level the fan set",
			status: entity.TicketJourneyStatusPaid,
			setup: func(d *ticketJourneyTestDeps) {
				d.expectRaiseReads(ctx, "event-far", raiseConcert("event-far", "artist-1"), followed("artist-1", entity.HypeHome, entity.HypeSetByFan))
			},
		},
		{
			// @spec components/usecase/ticket-journey/set-status "Tracking raises nothing"
			name:   "tracking raises nothing",
			status: entity.TicketJourneyStatusTracking,
		},
		{
			name:   "lost raises nothing",
			status: entity.TicketJourneyStatusLost,
		},
		{
			// @spec components/usecase/ticket-journey/set-status "Several followed artists perform"
			name:   "several followed artists perform",
			status: entity.TicketJourneyStatusApplied,
			setup: func(d *ticketJourneyTestDeps) {
				d.expectRaiseReads(ctx, "event-far", raiseConcert("event-far", "artist-1", "artist-2"),
					followed("artist-1", entity.HypeNearby, entity.HypeSetByDefault), followed("artist-2", entity.HypeNearby, entity.HypeSetByDefault))
			},
		},
		{
			// @spec components/usecase/ticket-journey/set-status "Raising fails"
			name:   "raising fails",
			status: entity.TicketJourneyStatusApplied,
			setup: func(d *ticketJourneyTestDeps) {
				d.expectRaiseReads(ctx, "event-far", raiseConcert("event-far", "artist-1"), followed("artist-1", entity.HypeNearby, entity.HypeSetByDefault))
				d.follows.EXPECT().RaiseHype(ctx, "user-1", "artist-1", entity.HypeAway).Return(false, apperr.ErrUnavailable).Once()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newTicketJourneyTestDeps(t)
			// No prior read: the strict mock fails on any Get.
			d.repo.EXPECT().Upsert(ctx, journey(tt.status)).Return(tt.upsert).Once()
			if tt.setup != nil {
				tt.setup(d)
			}

			err := d.uc.SetStatus(ctx, "user-1", "event-far", tt.status)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// TestTicketJourneyUseCase_RaiseHypeToMatch covers the steps of the hype raise
// shared by SetStatus, MarkPaid and MarkApplied, through SetStatus with
// Applied.
func TestTicketJourneyUseCase_RaiseHypeToMatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tests := []struct {
		name  string
		setup func(d *ticketJourneyTestDeps)
	}{
		{
			// @spec components/usecase/ticket-journey/set-status "Concert already matched"
			name: "concert already matched",
			setup: func(d *ticketJourneyTestDeps) {
				d.expectRaiseReads(ctx, "event-near", raiseConcert("event-near", "artist-1"), followed("artist-1", entity.HypeNearby, entity.HypeSetByDefault))
			},
		},
		{
			name: "only one of two performers followed",
			setup: func(d *ticketJourneyTestDeps) {
				d.expectRaiseReads(ctx, "event-near", raiseConcert("event-near", "artist-1", "artist-2"),
					followed("artist-1", entity.HypeHome, entity.HypeSetByInference), followed("artist-3", entity.HypeWatch, entity.HypeSetByFan))
				d.follows.EXPECT().RaiseHype(ctx, "user-1", "artist-1", entity.HypeNearby).Return(true, nil).Once()
			},
		},
		{
			name: "fan without a home",
			setup: func(d *ticketJourneyTestDeps) {
				d.users.EXPECT().Get(ctx, "user-1").Return(&entity.User{ID: "user-1"}, nil).Once()
			},
		},
		{
			name: "event that is not a concert",
			setup: func(d *ticketJourneyTestDeps) {
				d.expectRaiseReads(ctx, "event-near", nil)
			},
		},
		{
			name: "no performer followed",
			setup: func(d *ticketJourneyTestDeps) {
				d.expectRaiseReads(ctx, "event-near", raiseConcert("event-near", "artist-1"))
			},
		},
		{
			name: "reading the user fails",
			setup: func(d *ticketJourneyTestDeps) {
				d.users.EXPECT().Get(ctx, "user-1").Return(nil, apperr.ErrInternal).Once()
			},
		},
		{
			name: "reading the concert fails",
			setup: func(d *ticketJourneyTestDeps) {
				d.users.EXPECT().Get(ctx, "user-1").Return(&entity.User{ID: "user-1", Home: tokyoHome}, nil).Once()
				d.concerts.EXPECT().ListByIDs(ctx, []string{"event-near"}).Return(nil, apperr.ErrInternal).Once()
			},
		},
		{
			name: "reading the follows fails",
			setup: func(d *ticketJourneyTestDeps) {
				d.users.EXPECT().Get(ctx, "user-1").Return(&entity.User{ID: "user-1", Home: tokyoHome}, nil).Once()
				d.concerts.EXPECT().ListByIDs(ctx, []string{"event-near"}).Return([]*entity.Concert{raiseConcert("event-near", "artist-1")}, nil).Once()
				d.follows.EXPECT().ListByUser(ctx, "user-1").Return(nil, apperr.ErrInternal).Once()
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newTicketJourneyTestDeps(t)
			d.repo.EXPECT().Upsert(ctx, &entity.TicketJourney{
				UserID: "user-1", EventID: "event-near", Status: entity.TicketJourneyStatusApplied, StatusSetBy: entity.TicketJourneySetByFan,
			}).Return(nil).Once()
			tt.setup(d)

			assert.NoError(t, d.uc.SetStatus(ctx, "user-1", "event-near", entity.TicketJourneyStatusApplied))
		})
	}
}

func TestTicketJourneyUseCase_InferTracking(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tracking := &entity.TicketJourney{
		UserID: "user-1", EventID: "event-far", Status: entity.TicketJourneyStatusTracking, StatusSetBy: entity.TicketJourneySetByInference,
	}
	tests := []struct {
		name        string
		stored      bool
		err         error
		wantStarted bool
	}{
		// @spec components/usecase/ticket-journey/infer-tracking "No journey yet"
		{name: "no journey yet", stored: true, wantStarted: true},
		// @spec components/usecase/ticket-journey/infer-tracking "A journey exists"
		{name: "a journey exists", stored: false},
		// @spec components/usecase/ticket-journey/infer-tracking "After the fan removed an inferred journey"
		{name: "after the fan removed an inferred journey", stored: true, wantStarted: true},
		// @spec components/usecase/ticket-journey/infer-tracking "Unknown event"
		{name: "unknown event", err: apperr.ErrFailedPrecondition},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// No hype read or raise: the strict mocks fail on any call.
			d := newTicketJourneyTestDeps(t)
			d.repo.EXPECT().UpsertIfReplaceable(ctx, tracking).Return(tt.stored, tt.err).Once()

			started, err := d.uc.InferTracking(ctx, "user-1", "event-far")

			if tt.err != nil {
				assert.ErrorIs(t, err, tt.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantStarted, started)
		})
	}
}

func TestTicketJourneyUseCase_MarkApplied(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	entered := entity.LotteryEntryEnteredData{LotteryEntryID: "entry-1", UserID: "user-1", EventID: "event-far"}
	applied := &entity.TicketJourney{
		UserID: "user-1", EventID: "event-far", Status: entity.TicketJourneyStatusApplied, StatusSetBy: entity.TicketJourneySetByPlatform,
	}
	entry := func(s entity.LotteryEntryState) *entity.LotteryEntry {
		return &entity.LotteryEntry{ID: "entry-1", State: s}
	}
	tests := []struct {
		name    string
		setup   func(d *ticketJourneyTestDeps)
		wantErr error
	}{
		{
			// @spec components/usecase/ticket-journey/mark-applied "Fan enters a lottery"
			name: "fan enters a lottery",
			setup: func(d *ticketJourneyTestDeps) {
				d.entries.EXPECT().Get(ctx, entity.LotteryEntryID("entry-1")).Return(entry(entity.LotteryEntryStateEntered), nil).Once()
				d.repo.EXPECT().UpsertIfReplaceable(ctx, applied).Return(true, nil).Once()
				d.expectRaiseReads(ctx, "event-far", raiseConcert("event-far", "artist-1"))
			},
		},
		{
			// @spec components/usecase/ticket-journey/mark-applied "Fan was tracking the event"
			name: "fan was tracking the event",
			setup: func(d *ticketJourneyTestDeps) {
				d.entries.EXPECT().Get(ctx, entity.LotteryEntryID("entry-1")).Return(entry(entity.LotteryEntryStateEntered), nil).Once()
				d.repo.EXPECT().UpsertIfReplaceable(ctx, applied).Return(true, nil).Once()
				d.expectRaiseReads(ctx, "event-far", raiseConcert("event-far", "artist-1"), followed("artist-1", entity.HypeAway, entity.HypeSetByFan))
			},
		},
		{
			// @spec components/usecase/ticket-journey/mark-applied "Announcement arrives after the purchase"
			name: "announcement arrives after the purchase",
			setup: func(d *ticketJourneyTestDeps) {
				d.entries.EXPECT().Get(ctx, entity.LotteryEntryID("entry-1")).Return(entry(entity.LotteryEntryStateEntered), nil).Once()
				// The journey is Paid, so nothing is stored and nothing is raised.
				d.repo.EXPECT().UpsertIfReplaceable(ctx, applied).Return(false, nil).Once()
			},
		},
		{
			// @spec components/usecase/ticket-journey/mark-applied "Announcement arrives after the draw"
			name: "announcement arrives after the draw",
			setup: func(d *ticketJourneyTestDeps) {
				d.entries.EXPECT().Get(ctx, entity.LotteryEntryID("entry-1")).Return(entry(entity.LotteryEntryStateLost), nil).Once()
			},
		},
		{
			// @spec components/usecase/ticket-journey/mark-applied "Recording fails"
			name: "recording fails",
			setup: func(d *ticketJourneyTestDeps) {
				d.entries.EXPECT().Get(ctx, entity.LotteryEntryID("entry-1")).Return(entry(entity.LotteryEntryStateEntered), nil).Once()
				d.repo.EXPECT().UpsertIfReplaceable(ctx, applied).Return(false, apperr.ErrUnavailable).Once()
			},
			wantErr: apperr.ErrUnavailable,
		},
		{
			// @spec components/usecase/ticket-journey/mark-applied "Entry for a distant concert"
			name: "entry for a distant concert",
			setup: func(d *ticketJourneyTestDeps) {
				d.entries.EXPECT().Get(ctx, entity.LotteryEntryID("entry-1")).Return(entry(entity.LotteryEntryStateEntered), nil).Once()
				d.repo.EXPECT().UpsertIfReplaceable(ctx, applied).Return(true, nil).Once()
				d.expectRaiseReads(ctx, "event-far", raiseConcert("event-far", "artist-1"), followed("artist-1", entity.HypeNearby, entity.HypeSetByDefault))
				d.follows.EXPECT().RaiseHype(ctx, "user-1", "artist-1", entity.HypeAway).Return(true, nil).Once()
			},
		},
		{
			// @spec components/usecase/ticket-journey/mark-applied "Raising fails"
			name: "raising fails",
			setup: func(d *ticketJourneyTestDeps) {
				d.entries.EXPECT().Get(ctx, entity.LotteryEntryID("entry-1")).Return(entry(entity.LotteryEntryStateEntered), nil).Once()
				d.repo.EXPECT().UpsertIfReplaceable(ctx, applied).Return(true, nil).Once()
				d.expectRaiseReads(ctx, "event-far", raiseConcert("event-far", "artist-1"), followed("artist-1", entity.HypeNearby, entity.HypeSetByDefault))
				d.follows.EXPECT().RaiseHype(ctx, "user-1", "artist-1", entity.HypeAway).Return(false, apperr.ErrInternal).Once()
			},
		},
		{
			name: "reading the entry fails",
			setup: func(d *ticketJourneyTestDeps) {
				d.entries.EXPECT().Get(ctx, entity.LotteryEntryID("entry-1")).Return(nil, apperr.ErrUnavailable).Once()
			},
			wantErr: apperr.ErrUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newTicketJourneyTestDeps(t)
			tt.setup(d)

			err := d.uc.MarkApplied(ctx, entered)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestTicketJourneyUseCase_Delete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name    string
		userID  string
		eventID string
		setup   func(t *testing.T, d *ticketJourneyTestDeps)
		wantErr error
	}{
		{
			name:    "successfully deletes journey",
			userID:  "user-1",
			eventID: "event-1",
			setup: func(t *testing.T, d *ticketJourneyTestDeps) {
				t.Helper()
				d.repo.EXPECT().
					Delete(ctx, "user-1", "event-1").
					Return(nil).
					Once()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newTicketJourneyTestDeps(t)
			tt.setup(t, d)

			err := d.uc.Delete(ctx, tt.userID, tt.eventID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestTicketJourneyUseCase_ListByUser(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("returns journeys for user", func(t *testing.T) {
		t.Parallel()
		d := newTicketJourneyTestDeps(t)

		expected := []*entity.TicketJourney{
			{UserID: "user-1", EventID: "event-1", Status: entity.TicketJourneyStatusTracking, StatusSetBy: entity.TicketJourneySetByInference},
			{UserID: "user-1", EventID: "event-2", Status: entity.TicketJourneyStatusPaid, StatusSetBy: entity.TicketJourneySetByPlatform},
		}
		d.repo.EXPECT().ListByUser(ctx, "user-1").Return(expected, nil).Once()

		result, err := d.uc.List(ctx, "user-1")

		assert.NoError(t, err)
		assert.Equal(t, expected, result)
	})
}
