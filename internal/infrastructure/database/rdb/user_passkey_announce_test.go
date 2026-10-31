package rdb_test

import (
	"context"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"

	"bytes"
	"encoding/base64"
	"encoding/json"
	"github.com/liverty-music/backend/internal/adapter/webhook"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
	"github.com/zitadel/zitadel-go/v3/pkg/actions"
	"net/http"
	"net/http/httptest"
)

// newUniqueUser returns a NewUser with a provider user id and email of its
// own.
func newUniqueUser() *entity.NewUser {
	tag := uuid.NewV7().String()
	return &entity.NewUser{ProviderUserID: "ext-" + tag, Email: tag + "@example.com", Name: "Display Name"}
}

func usersAnnounced(t *testing.T, mark int64, subject, userID string) []*entityv1.User {
	t.Helper()
	return announcedSince(t, mark, subject, func() *entityv1.User { return &entityv1.User{} },
		func(u *entityv1.User) bool { return u.GetId().GetValue() == userID })
}

func TestUserRepository_CreateAnnounces(t *testing.T) {
	ctx := context.Background()
	repo := rdb.NewUserRepository(testDB, testOutbox)

	t.Run("new user", func(t *testing.T) {
		// @spec components/entity/user/create "New user"
		// @spec components/infrastructure/backend/process/event-delivery "New user announced"
		params := newUniqueUser()
		mark := outboxMark(t)

		user, err := repo.Create(ctx, params)

		require.NoError(t, err)
		got := usersAnnounced(t, mark, entity.SubjectUserCreated, user.ID)
		require.Len(t, got, 1)
		assert.Equal(t, params.ProviderUserID, got[0].GetProviderUserId().GetValue())
		assert.Equal(t, params.Email, got[0].GetEmail().GetValue())
		assert.Empty(t, got[0].GetName(), "no display name")
		assert.Nil(t, got[0].GetFullName())
		assert.Nil(t, got[0].GetPhoneNumber())
	})

	t.Run("identity already registered", func(t *testing.T) {
		// @spec components/entity/user/create "Identity already registered"
		params := newUniqueUser()
		first, err := repo.Create(ctx, params)
		require.NoError(t, err)
		mark := outboxMark(t)

		_, err = repo.Create(ctx, params)

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
		assert.Empty(t, announcedSince(t, mark, entity.SubjectUserCreated, func() *entityv1.User { return &entityv1.User{} },
			func(u *entityv1.User) bool { return u.GetProviderUserId().GetValue() == params.ProviderUserID }))
		_ = first
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/user/create "Announcement cannot be recorded"
		params := newUniqueUser()

		_, err := rdb.NewUserRepository(testDB, failingOutbox{}).Create(ctx, params)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		_, err = repo.GetByProviderUserID(ctx, params.ProviderUserID)
		assert.ErrorIs(t, err, apperr.ErrNotFound, "no User is stored")
	})
}

func TestUserRepository_RecordLogin(t *testing.T) {
	ctx := context.Background()
	repo := rdb.NewUserRepository(testDB, testOutbox)
	jst := time.FixedZone("JST", 9*60*60)
	at10, at12 := time.Date(2026, 10, 28, 10, 0, 0, 0, jst), time.Date(2026, 10, 28, 12, 0, 0, 0, jst)
	newUser := func(t *testing.T) *entity.User {
		t.Helper()
		u, err := repo.Create(ctx, newUniqueUser())
		require.NoError(t, err)
		return u
	}
	lastLogin := func(t *testing.T, id string) *time.Time {
		t.Helper()
		u, err := repo.Get(ctx, id)
		require.NoError(t, err)
		return u.LastLoginTime
	}

	t.Run("first sign-in recorded", func(t *testing.T) {
		// @spec components/entity/user/record-login "First sign-in recorded"
		u := newUser(t)

		require.NoError(t, repo.RecordLogin(ctx, u.ID, at10))

		require.NotNil(t, lastLogin(t, u.ID))
		assert.True(t, lastLogin(t, u.ID).Equal(at10))
	})

	t.Run("later sign-in", func(t *testing.T) {
		// @spec components/entity/user/record-login "Later sign-in"
		// @spec components/entity/user/record-login "Sign-in announced"
		u := newUser(t)
		require.NoError(t, repo.RecordLogin(ctx, u.ID, at10))
		mark := outboxMark(t)

		require.NoError(t, repo.RecordLogin(ctx, u.ID, at12))

		assert.True(t, lastLogin(t, u.ID).Equal(at12))
		got := usersAnnounced(t, mark, entity.SubjectUserLoggedIn, u.ID)
		require.Len(t, got, 1)
		assert.True(t, got[0].GetLastLoginTime().AsTime().Equal(at12))
		assert.Empty(t, got[0].GetName())
	})

	t.Run("same or older sign-in", func(t *testing.T) {
		// @spec components/entity/user/record-login "Same or older sign-in"
		u := newUser(t)
		require.NoError(t, repo.RecordLogin(ctx, u.ID, at12))
		mark := outboxMark(t)

		require.NoError(t, repo.RecordLogin(ctx, u.ID, at12))
		require.NoError(t, repo.RecordLogin(ctx, u.ID, at10))

		assert.True(t, lastLogin(t, u.ID).Equal(at12))
		assert.Empty(t, usersAnnounced(t, mark, entity.SubjectUserLoggedIn, u.ID))
	})

	t.Run("unknown user", func(t *testing.T) {
		// @spec components/entity/user/record-login "Unknown User"
		assert.ErrorIs(t, repo.RecordLogin(ctx, uuid.NewV7().String(), at10), apperr.ErrNotFound)
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/user/record-login "Announcement cannot be recorded"
		u := newUser(t)
		require.NoError(t, repo.RecordLogin(ctx, u.ID, at10))

		err := rdb.NewUserRepository(testDB, failingOutbox{}).RecordLogin(ctx, u.ID, at12)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.True(t, lastLogin(t, u.ID).Equal(at10), "the last login time is unchanged")
	})
}

func TestPasskeyRepository_Announces(t *testing.T) {
	ctx := context.Background()
	users := rdb.NewUserRepository(testDB, testOutbox)
	repo := rdb.NewPasskeyRepository(testDB, testOutbox)
	created := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	newFan := func(t *testing.T) *entity.User {
		t.Helper()
		u, err := users.Create(ctx, newUniqueUser())
		require.NoError(t, err)
		return u
	}
	passkeysAnnounced := func(t *testing.T, mark int64, subject, id string) []*entityv1.Passkey {
		t.Helper()
		return announcedSince(t, mark, subject, func() *entityv1.Passkey { return &entityv1.Passkey{} },
			func(p *entityv1.Passkey) bool { return p.GetId().GetValue() == id })
	}
	stored := func(t *testing.T, userID string) int {
		t.Helper()
		got, err := repo.ListByUser(ctx, userID)
		require.NoError(t, err)
		return len(got)
	}

	t.Run("passkey added", func(t *testing.T) {
		// @spec components/entity/passkey/create "Passkey added"
		fan, id, name := newFan(t), "token-"+uuid.NewV7().String(), "iPhone"
		mark := outboxMark(t)

		require.NoError(t, repo.Create(ctx, &entity.Passkey{ProviderPasskeyID: id, UserID: fan.ID, AAGUID: googlePasswordManagerAAGUID, CreateTime: created, Name: &name}))

		got := passkeysAnnounced(t, mark, entity.SubjectUserPasskeyAdded, id)
		require.Len(t, got, 1)
		assert.Equal(t, fan.ID, got[0].GetUserId().GetValue())
		assert.Equal(t, "iPhone", got[0].GetName().GetValue())
		assert.NotEmpty(t, got[0].GetProviderName().GetValue(), "the provider named by the AAGUID")
		assert.True(t, got[0].GetCreateTime().AsTime().Equal(created))
	})

	t.Run("same passkey again", func(t *testing.T) {
		// @spec components/entity/passkey/create "Same passkey again"
		fan, id := newFan(t), "token-"+uuid.NewV7().String()
		require.NoError(t, repo.Create(ctx, &entity.Passkey{ProviderPasskeyID: id, UserID: fan.ID, AAGUID: googlePasswordManagerAAGUID, CreateTime: created}))
		mark := outboxMark(t)

		require.NoError(t, repo.Create(ctx, &entity.Passkey{ProviderPasskeyID: id, UserID: fan.ID, AAGUID: entity.ZeroAAGUID, CreateTime: created}))

		assert.Empty(t, passkeysAnnounced(t, mark, entity.SubjectUserPasskeyAdded, id))
	})

	t.Run("add announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/passkey/create "Announcement cannot be recorded"
		fan := newFan(t)

		err := rdb.NewPasskeyRepository(testDB, failingOutbox{}).Create(ctx, &entity.Passkey{ProviderPasskeyID: "token-" + uuid.NewV7().String(), UserID: fan.ID, AAGUID: googlePasswordManagerAAGUID, CreateTime: created})

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Zero(t, stored(t, fan.ID))
	})

	t.Run("passkey removed", func(t *testing.T) {
		// @spec components/entity/passkey/delete "Passkey removed"
		fan, id := newFan(t), "token-"+uuid.NewV7().String()
		require.NoError(t, repo.Create(ctx, &entity.Passkey{ProviderPasskeyID: id, UserID: fan.ID, AAGUID: googlePasswordManagerAAGUID, CreateTime: created}))
		mark := outboxMark(t)

		require.NoError(t, repo.Delete(ctx, id))

		got := passkeysAnnounced(t, mark, entity.SubjectUserPasskeyRemoved, id)
		require.Len(t, got, 1)
		assert.Equal(t, fan.ID, got[0].GetUserId().GetValue())
		assert.True(t, got[0].GetCreateTime().AsTime().Equal(created))
		assert.Zero(t, stored(t, fan.ID))
	})

	t.Run("never recorded", func(t *testing.T) {
		// @spec components/entity/passkey/delete "Never recorded"
		id := "token-" + uuid.NewV7().String()
		mark := outboxMark(t)

		require.NoError(t, repo.Delete(ctx, id))
		assert.Empty(t, passkeysAnnounced(t, mark, entity.SubjectUserPasskeyRemoved, id))
	})

	t.Run("remove announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/passkey/delete "Announcement cannot be recorded"
		fan, id := newFan(t), "token-"+uuid.NewV7().String()
		require.NoError(t, repo.Create(ctx, &entity.Passkey{ProviderPasskeyID: id, UserID: fan.ID, AAGUID: googlePasswordManagerAAGUID, CreateTime: created}))

		err := rdb.NewPasskeyRepository(testDB, failingOutbox{}).Delete(ctx, id)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Equal(t, 1, stored(t, fan.ID), "the Passkey is still stored")
	})
}

// TestLoginEvents_OlderNoticeArrivesLate sends two signed sign-in notices of
// one fan through the real login-events webhook, UserUseCase.RecordLogin and
// User.RecordLogin, the later sign-in first.
//
// @spec components/adapter/fan/api/webhook/login-events "Older notice arrives late"
func TestLoginEvents_OlderNoticeArrivesLate(t *testing.T) {
	ctx := context.Background()
	logger, err := logging.New()
	require.NoError(t, err)
	repo := rdb.NewUserRepository(testDB, testOutbox)
	u, err := repo.Create(ctx, newUniqueUser())
	require.NoError(t, err)
	users := usecase.NewUserUseCase(repo, usecase.DeletionBlockerReads{}, nil, nil, nil, nil, usecase.PasskeyMailers{}, logger)
	const signingKey = "login-events-story-key"
	handler := webhook.NewLoginEventHandler(signingKey, users, logger)
	jst := time.FixedZone("JST", 9*60*60)
	notice := func(at time.Time) int {
		inner, err := json.Marshal(map[string]string{"userID": u.ProviderUserID})
		require.NoError(t, err)
		body, err := json.Marshal(map[string]any{
			"event_type":    "session.user.checked",
			"created_at":    at.Format(time.RFC3339Nano),
			"event_payload": base64.StdEncoding.EncodeToString(inner),
		})
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, "/account-login-event", bytes.NewReader(body))
		req.Header.Set(actions.SigningHeader, actions.ComputeSignatureHeader(time.Now(), body, signingKey))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	require.Equal(t, http.StatusOK, notice(time.Date(2026, 10, 28, 12, 0, 0, 0, jst)))
	assert.Equal(t, http.StatusOK, notice(time.Date(2026, 10, 28, 10, 0, 0, 0, jst)))

	got, err := repo.Get(ctx, u.ID)
	require.NoError(t, err)
	require.NotNil(t, got.LastLoginTime)
	assert.True(t, got.LastLoginTime.Equal(time.Date(2026, 10, 28, 12, 0, 0, 0, jst)))
}
