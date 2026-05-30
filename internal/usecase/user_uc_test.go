package usecase_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

type userTestDeps struct {
	repo *mocks.MockUserRepository
	uc   usecase.UserUseCase
}

func newUserTestDeps(t *testing.T) *userTestDeps {
	t.Helper()
	d := &userTestDeps{
		repo: mocks.NewMockUserRepository(t),
	}
	d.uc = usecase.NewUserUseCase(d.repo, messaging.NewEventPublisher(newTestPublisher()), newTestLogger(t))
	return d
}

func TestUserUseCase_CreateUser(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		params := &entity.NewUser{
			Name:  "John Doe",
			Email: "john@example.com",
		}

		expectedUser := &entity.User{
			ID:    "user-123",
			Name:  "John Doe",
			Email: "john@example.com",
		}

		d.repo.EXPECT().Create(ctx, params).Return(expectedUser, nil).Once()

		result, err := d.uc.Create(ctx, params)

		assert.NoError(t, err)
		assert.Equal(t, expectedUser, result)
	})

	t.Run("success with home", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		params := &entity.NewUser{
			Name:  "John Doe",
			Email: "john@example.com",
			Home: &entity.Home{
				CountryCode: "JP",
				Level1:      "JP-13",
			},
		}

		expectedUser := &entity.User{
			ID:    "user-123",
			Name:  "John Doe",
			Email: "john@example.com",
			Home: &entity.Home{
				ID:          "home-1",
				CountryCode: "JP",
				Level1:      "JP-13",
			},
		}

		d.repo.EXPECT().Create(ctx, params).Return(expectedUser, nil).Once()

		result, err := d.uc.Create(ctx, params)

		assert.NoError(t, err)
		assert.Equal(t, expectedUser, result)
	})

	t.Run("error - invalid home country_code", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		params := &entity.NewUser{
			Name:  "John Doe",
			Email: "john@example.com",
			Home: &entity.Home{
				CountryCode: "jp",
				Level1:      "JP-13",
			},
		}

		result, err := d.uc.Create(ctx, params)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("error - home level_1 prefix mismatch", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		params := &entity.NewUser{
			Name:  "John Doe",
			Email: "john@example.com",
			Home: &entity.Home{
				CountryCode: "JP",
				Level1:      "US-CA",
			},
		}

		result, err := d.uc.Create(ctx, params)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("error - repository returns nil user without error", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		params := &entity.NewUser{
			Name:  "Jane Doe",
			Email: "jane@example.com",
		}

		d.repo.EXPECT().Create(ctx, params).Return(nil, nil).Once()

		result, err := d.uc.Create(ctx, params)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, apperr.ErrInternal)
	})

	t.Run("error - repository fails", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		params := &entity.NewUser{
			Name:  "Jane Doe",
			Email: "jane@example.com",
		}

		d.repo.EXPECT().Create(ctx, params).Return(nil, apperr.New(codes.Internal, "failed to create user")).Once()

		result, err := d.uc.Create(ctx, params)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, apperr.ErrInternal)
	})

	t.Run("idempotent — duplicate external_id returns existing user", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		params := &entity.NewUser{
			ExternalID: "ext-existing",
			Email:      "existing@example.com",
			Name:       "Existing User",
		}
		existingUser := &entity.User{
			ID:         "user-existing-1",
			ExternalID: "ext-existing",
			Email:      "existing@example.com",
			Name:       "Existing User",
		}

		d.repo.EXPECT().Create(ctx, params).
			Return(nil, apperr.New(codes.AlreadyExists, "duplicate user")).Once()
		d.repo.EXPECT().GetByExternalID(ctx, "ext-existing").
			Return(existingUser, nil).Once()

		result, err := d.uc.Create(ctx, params)

		assert.NoError(t, err)
		assert.Equal(t, existingUser, result)
	})

	t.Run("duplicate email with different external_id surfaces AlreadyExists", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		params := &entity.NewUser{
			ExternalID: "ext-new-caller",
			Email:      "taken@example.com",
			Name:       "New Caller",
		}

		d.repo.EXPECT().Create(ctx, params).
			Return(nil, apperr.New(codes.AlreadyExists, "duplicate email")).Once()
		// GetByExternalID returns NotFound because the caller's external_id is not
		// the conflicting column — the email was claimed by a different identity.
		d.repo.EXPECT().GetByExternalID(ctx, "ext-new-caller").
			Return(nil, apperr.New(codes.NotFound, "user not found")).Once()

		result, err := d.uc.Create(ctx, params)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
	})

	// Regression for the 2026-05-23 incident: when GetByExternalID fails
	// for a non-NotFound reason (Internal scan failure, Unavailable, etc.),
	// the use-case MUST surface that error class instead of collapsing it
	// into the original AlreadyExists. Previously the retry path discarded
	// getErr entirely, masking a scanUser NULL→string crash as
	// AlreadyExists at the wire with no log trail.
	t.Run("retry GetByExternalID non-NotFound error surfaces truthfully", func(t *testing.T) {
		t.Parallel()
		// Table covers the representative non-NotFound classes the retry
		// path can encounter. Internal is the original incident's class
		// (scanUser NULL→string crash); Unavailable covers transient pool
		// / connection failures. Both MUST propagate as themselves so the
		// operator sees the real failure instead of AlreadyExists.
		cases := []struct {
			name       string
			externalID string
			email      string
			retryErr   error
			wantClass  error
		}{
			{
				name:       "Internal (scan failure on NULL column)",
				externalID: "ext-internal-error",
				email:      "ie@example.com",
				retryErr:   apperr.New(codes.Internal, "scan failure"),
				wantClass:  apperr.ErrInternal,
			},
			{
				name:       "Unavailable (transient pool failure)",
				externalID: "ext-unavailable",
				email:      "ua@example.com",
				retryErr:   apperr.New(codes.Unavailable, "connection refused"),
				wantClass:  apperr.ErrUnavailable,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				d := newUserTestDeps(t)

				params := &entity.NewUser{
					ExternalID: tc.externalID,
					Email:      tc.email,
					Name:       "retry probe",
				}

				d.repo.EXPECT().Create(ctx, params).
					Return(nil, apperr.New(codes.AlreadyExists, "duplicate user")).Once()
				d.repo.EXPECT().GetByExternalID(ctx, tc.externalID).
					Return(nil, tc.retryErr).Once()

				result, err := d.uc.Create(ctx, params)

				assert.Error(t, err)
				assert.Nil(t, result)
				// The retry's error class wins; original AlreadyExists is
				// logged for forensics but NOT returned (rationale in
				// user_uc.go).
				assert.ErrorIs(t, err, tc.wantClass)
				assert.NotErrorIs(t, err, apperr.ErrAlreadyExists)
			})
		}
	})

	// Create accepts an empty preferred_language (old clients omit the
	// field; the row is created NULL and the client backfills on next
	// hydration). A non-empty value MUST match ISO 639-1.
	t.Run("InvalidArgument when preferred_language is present but malformed", func(t *testing.T) {
		t.Parallel()
		cases := []string{
			"e",       // too short
			"eng",     // too long
			"EN",      // uppercase
			"42",      // digits
			"ja-JP",   // region tag
			"english", // word
		}
		for _, bad := range cases {
			t.Run(bad, func(t *testing.T) {
				t.Parallel()
				d := newUserTestDeps(t)
				// repo MUST NOT be called.

				result, err := d.uc.Create(ctx, &entity.NewUser{
					ExternalID:        "ext-bad-lang",
					Email:             "bad-lang@example.com",
					Name:              "Bad Lang",
					PreferredLanguage: bad,
				})

				assert.Nil(t, result)
				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			})
		}
	})
}

func TestUserUseCase_GetUser(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		expectedUser := &entity.User{
			ID:    "user-123",
			Name:  "John Doe",
			Email: "john@example.com",
		}

		d.repo.EXPECT().Get(ctx, "user-123").Return(expectedUser, nil).Once()

		result, err := d.uc.Get(ctx, "user-123")

		assert.NoError(t, err)
		assert.Equal(t, expectedUser, result)
	})

	t.Run("error - not found", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		d.repo.EXPECT().Get(ctx, "nonexistent").Return(nil, apperr.ErrNotFound).Once()

		result, err := d.uc.Get(ctx, "nonexistent")

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestUserUseCase_UpdateHome(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		home := &entity.Home{
			CountryCode: "JP",
			Level1:      "JP-13",
		}

		expectedUser := &entity.User{
			ID:   "user-123",
			Name: "John Doe",
			Home: &entity.Home{
				ID:          "home-1",
				CountryCode: "JP",
				Level1:      "JP-13",
			},
		}

		d.repo.EXPECT().UpdateHome(ctx, "user-123", home).Return(expectedUser, nil).Once()

		result, err := d.uc.UpdateHome(ctx, "user-123", home)

		assert.NoError(t, err)
		assert.Equal(t, expectedUser, result)
		assert.Equal(t, "JP-13", result.Home.Level1)
	})

	t.Run("error - invalid country_code", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			home *entity.Home
		}{
			{"lowercase", &entity.Home{CountryCode: "jp", Level1: "JP-13"}},
			{"too long", &entity.Home{CountryCode: "JPN", Level1: "JP-13"}},
			{"single char", &entity.Home{CountryCode: "J", Level1: "JP-13"}},
			{"empty", &entity.Home{CountryCode: "", Level1: "JP-13"}},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				d := newUserTestDeps(t)

				result, err := d.uc.UpdateHome(ctx, "user-123", tt.home)

				assert.Error(t, err)
				assert.Nil(t, result)
				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			})
		}
	})

	t.Run("error - invalid level_1", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			home *entity.Home
		}{
			{"empty", &entity.Home{CountryCode: "JP", Level1: ""}},
			{"no hyphen", &entity.Home{CountryCode: "JP", Level1: "JP13"}},
			{"lowercase country prefix", &entity.Home{CountryCode: "JP", Level1: "jp-13"}},
			{"free text", &entity.Home{CountryCode: "JP", Level1: "東京都"}},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				d := newUserTestDeps(t)

				result, err := d.uc.UpdateHome(ctx, "user-123", tt.home)

				assert.Error(t, err)
				assert.Nil(t, result)
				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			})
		}
	})

	t.Run("error - level_1 prefix mismatch", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		home := &entity.Home{
			CountryCode: "JP",
			Level1:      "US-CA",
		}

		result, err := d.uc.UpdateHome(ctx, "user-123", home)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("error - invalid level_2 length", func(t *testing.T) {
		t.Parallel()

		emptyL2 := ""
		tooLongL2 := "123456789012345678901"

		tests := []struct {
			name string
			home *entity.Home
		}{
			{"empty level_2", &entity.Home{CountryCode: "JP", Level1: "JP-13", Level2: &emptyL2}},
			{"too long level_2", &entity.Home{CountryCode: "JP", Level1: "JP-13", Level2: &tooLongL2}},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				d := newUserTestDeps(t)

				result, err := d.uc.UpdateHome(ctx, "user-123", tt.home)

				assert.Error(t, err)
				assert.Nil(t, result)
				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			})
		}
	})

	t.Run("success - various valid codes", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			countryCode string
			level1      string
		}{
			{"JP", "JP-13"},
			{"JP", "JP-01"},
			{"US", "US-CA"},
			{"GB", "GB-ENG"},
		}

		for _, tt := range tests {
			t.Run(tt.level1, func(t *testing.T) {
				t.Parallel()
				d := newUserTestDeps(t)

				home := &entity.Home{
					CountryCode: tt.countryCode,
					Level1:      tt.level1,
				}

				expectedUser := &entity.User{
					ID: "user-123",
					Home: &entity.Home{
						ID:          "home-1",
						CountryCode: tt.countryCode,
						Level1:      tt.level1,
					},
				}
				d.repo.EXPECT().UpdateHome(ctx, "user-123", home).Return(expectedUser, nil).Once()

				result, err := d.uc.UpdateHome(ctx, "user-123", home)

				assert.NoError(t, err)
				assert.Equal(t, tt.level1, result.Home.Level1)
			})
		}
	})

	t.Run("error - repository fails", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		home := &entity.Home{
			CountryCode: "JP",
			Level1:      "JP-13",
		}

		d.repo.EXPECT().UpdateHome(ctx, "user-123", home).Return(nil, apperr.New(codes.NotFound, "user not found")).Once()

		result, err := d.uc.UpdateHome(ctx, "user-123", home)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestUserUseCase_UpdatePreferredLanguage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("success — updates and returns the user", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		priorUser := &entity.User{
			ID:                "user-lang-1",
			Email:             "lang@example.com",
			PreferredLanguage: "ja",
		}
		updatedUser := &entity.User{
			ID:                "user-lang-1",
			Email:             "lang@example.com",
			PreferredLanguage: "en",
		}

		d.repo.EXPECT().Get(ctx, "user-lang-1").Return(priorUser, nil).Once()
		d.repo.EXPECT().UpdatePreferredLanguage(ctx, "user-lang-1", "en").
			Return(updatedUser, nil).Once()

		result, err := d.uc.UpdatePreferredLanguage(ctx, "user-lang-1", "en")

		assert.NoError(t, err)
		assert.Equal(t, updatedUser, result)
		assert.Equal(t, "en", result.PreferredLanguage)
	})

	t.Run("not found — pre-fetch Get returns NotFound", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		d.repo.EXPECT().Get(ctx, "ghost-id").
			Return(nil, apperr.New(codes.NotFound, "user not found")).Once()
		// UpdatePreferredLanguage MUST NOT be called when the pre-fetch fails.

		result, err := d.uc.UpdatePreferredLanguage(ctx, "ghost-id", "ja")

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("not found — repository returns NotFound at update", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		priorUser := &entity.User{ID: "ghost-id", PreferredLanguage: "en"}
		d.repo.EXPECT().Get(ctx, "ghost-id").Return(priorUser, nil).Once()
		d.repo.EXPECT().UpdatePreferredLanguage(ctx, "ghost-id", "ja").
			Return(nil, apperr.New(codes.NotFound, "user not found")).Once()

		result, err := d.uc.UpdatePreferredLanguage(ctx, "ghost-id", "ja")

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	// Defense-in-depth: a non-RPC caller (integration test, internal
	// script, future handler that bypasses protovalidate) MUST be rejected
	// at the use-case layer when lang doesn't match ISO 639-1. The repo
	// mock is intentionally not registered — the use-case must fail before
	// touching it.
	t.Run("InvalidArgument when lang does not match ISO 639-1", func(t *testing.T) {
		t.Parallel()
		cases := []string{
			"",        // empty
			"e",       // too short
			"eng",     // too long
			"EN",      // uppercase
			"42",      // digits
			"ja-JP",   // region tag
			"english", // word
			" ja",     // whitespace
		}
		for _, bad := range cases {
			t.Run(bad, func(t *testing.T) {
				t.Parallel()
				d := newUserTestDeps(t)
				// repo MUST NOT be called — no EXPECT() registered.

				result, err := d.uc.UpdatePreferredLanguage(ctx, "user-1", bad)

				assert.Nil(t, result)
				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			})
		}
	})

	t.Run("InvalidArgument when id is empty", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		// repo MUST NOT be called.

		result, err := d.uc.UpdatePreferredLanguage(ctx, "", "ja")

		assert.Nil(t, result)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}
