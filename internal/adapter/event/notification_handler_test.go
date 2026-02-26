package event_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- test doubles ---

type fakeArtistRepo struct {
	artists   map[string]*entity.Artist
	followers map[string][]*entity.User
}

func (r *fakeArtistRepo) Get(_ context.Context, id string) (*entity.Artist, error) {
	if a, ok := r.artists[id]; ok {
		return a, nil
	}
	return nil, apperr.New(codes.NotFound, "artist not found")
}

func (r *fakeArtistRepo) Create(_ context.Context, _ ...*entity.Artist) ([]*entity.Artist, error) {
	return nil, nil
}

func (r *fakeArtistRepo) List(_ context.Context) ([]*entity.Artist, error) {
	return nil, nil
}

func (r *fakeArtistRepo) GetByMBID(_ context.Context, _ string) (*entity.Artist, error) {
	return nil, nil
}

func (r *fakeArtistRepo) CreateOfficialSite(_ context.Context, _ *entity.OfficialSite) error {
	return nil
}

func (r *fakeArtistRepo) GetOfficialSite(_ context.Context, _ string) (*entity.OfficialSite, error) {
	return nil, apperr.New(codes.NotFound, "not found")
}

func (r *fakeArtistRepo) Follow(_ context.Context, _, _ string) error { return nil }

func (r *fakeArtistRepo) Unfollow(_ context.Context, _, _ string) error { return nil }

func (r *fakeArtistRepo) SetPassionLevel(_ context.Context, _, _ string, _ entity.PassionLevel) error {
	return nil
}

func (r *fakeArtistRepo) ListFollowed(_ context.Context, _ string) ([]*entity.FollowedArtist, error) {
	return nil, nil
}

func (r *fakeArtistRepo) ListAllFollowed(_ context.Context) ([]*entity.Artist, error) {
	return nil, nil
}

func (r *fakeArtistRepo) ListFollowers(_ context.Context, artistID string) ([]*entity.User, error) {
	return r.followers[artistID], nil
}

type fakePushNotificationUC struct {
	notified []string // artist IDs that were notified
	err      error
}

func (uc *fakePushNotificationUC) Subscribe(_ context.Context, _, _, _, _ string) error {
	return nil
}

func (uc *fakePushNotificationUC) Unsubscribe(_ context.Context, _ string) error {
	return nil
}

func (uc *fakePushNotificationUC) NotifyNewConcerts(_ context.Context, artist *entity.Artist, _ []*entity.Concert) error {
	if uc.err != nil {
		return uc.err
	}
	uc.notified = append(uc.notified, artist.ID)
	return nil
}

// --- helpers ---

func makeCreatedMsg(t *testing.T, data messaging.ConcertCreatedData) *message.Message {
	t.Helper()
	payload, err := json.Marshal(data)
	require.NoError(t, err)
	return message.NewMessage("test-id", payload)
}

// --- tests ---

func TestNotificationHandler_Handle(t *testing.T) {
	t.Run("sends notifications on concert.created event", func(t *testing.T) {
		artistRepo := &fakeArtistRepo{
			artists: map[string]*entity.Artist{
				"artist-1": {ID: "artist-1", Name: "Test Artist"},
			},
		}
		concertRepo := &fakeConcertRepo{} // from concert_handler_test.go
		pushUC := &fakePushNotificationUC{}
		handler := event.NewNotificationHandler(artistRepo, concertRepo, pushUC, newTestLogger(t))

		data := messaging.ConcertCreatedData{
			ArtistID:     "artist-1",
			ArtistName:   "Test Artist",
			ConcertCount: 3,
		}

		msg := makeCreatedMsg(t, data)
		err := handler.Handle(msg)
		require.NoError(t, err)

		assert.Contains(t, pushUC.notified, "artist-1")
	})

	t.Run("returns error when artist not found", func(t *testing.T) {
		artistRepo := &fakeArtistRepo{artists: map[string]*entity.Artist{}}
		concertRepo := &fakeConcertRepo{}
		pushUC := &fakePushNotificationUC{}
		handler := event.NewNotificationHandler(artistRepo, concertRepo, pushUC, newTestLogger(t))

		data := messaging.ConcertCreatedData{
			ArtistID:     "nonexistent",
			ArtistName:   "Unknown",
			ConcertCount: 1,
		}

		msg := makeCreatedMsg(t, data)
		err := handler.Handle(msg)
		assert.Error(t, err)
	})

	t.Run("returns error when notification fails", func(t *testing.T) {
		artistRepo := &fakeArtistRepo{
			artists: map[string]*entity.Artist{
				"artist-2": {ID: "artist-2", Name: "Another Artist"},
			},
		}
		concertRepo := &fakeConcertRepo{}
		pushUC := &fakePushNotificationUC{err: fmt.Errorf("push service unavailable")}
		handler := event.NewNotificationHandler(artistRepo, concertRepo, pushUC, newTestLogger(t))

		data := messaging.ConcertCreatedData{
			ArtistID:     "artist-2",
			ArtistName:   "Another Artist",
			ConcertCount: 1,
		}

		msg := makeCreatedMsg(t, data)
		err := handler.Handle(msg)
		assert.Error(t, err)
	})

	t.Run("returns error on invalid payload", func(t *testing.T) {
		artistRepo := &fakeArtistRepo{artists: map[string]*entity.Artist{}}
		concertRepo := &fakeConcertRepo{}
		pushUC := &fakePushNotificationUC{}
		handler := event.NewNotificationHandler(artistRepo, concertRepo, pushUC, newTestLogger(t))

		msg := message.NewMessage("bad-id", []byte("not json"))
		err := handler.Handle(msg)
		assert.Error(t, err)
	})
}
