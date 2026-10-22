package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

type userTestDeps struct {
	repo            *mocks.MockUserRepository
	ticketRepo      *mocks.MockTicketRepository
	orderRepo       *mocks.MockOrderRepository
	emailVerifier   *mocks.MockEmailVerifier
	identityRemover *mocks.MockIdentityRemover
	uc              usecase.UserUseCase
}

func newUserTestDeps(t *testing.T) *userTestDeps {
	t.Helper()
	d := &userTestDeps{
		repo:            mocks.NewMockUserRepository(t),
		ticketRepo:      mocks.NewMockTicketRepository(t),
		orderRepo:       mocks.NewMockOrderRepository(t),
		emailVerifier:   mocks.NewMockEmailVerifier(t),
		identityRemover: mocks.NewMockIdentityRemover(t),
	}
	d.uc = usecase.NewUserUseCase(d.repo, d.ticketRepo, d.orderRepo, messaging.NewEventPublisher(newTestPublisher()), d.emailVerifier, d.identityRemover, usecase.PasskeyMailers{}, newTestLogger(t))
	return d
}

// newUserTestDepsNoVerifier builds test dependencies with a nil EmailVerifier,
// mirroring local dev where the Zitadel API client is not configured.
func newUserTestDepsNoVerifier(t *testing.T) *userTestDeps {
	t.Helper()
	d := &userTestDeps{
		repo: mocks.NewMockUserRepository(t),
	}
	d.uc = usecase.NewUserUseCase(d.repo, nil, nil, messaging.NewEventPublisher(newTestPublisher()), nil, nil, usecase.PasskeyMailers{}, newTestLogger(t))
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

	t.Run("idempotent — duplicate provider_user_id returns existing user", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		params := &entity.NewUser{
			ProviderUserID: "ext-existing",
			Email:          "existing@example.com",
			Name:           "Existing User",
		}
		existingUser := &entity.User{
			ID:             "user-existing-1",
			ProviderUserID: "ext-existing",
			Email:          "existing@example.com",
			Name:           "Existing User",
		}

		d.repo.EXPECT().Create(ctx, params).
			Return(nil, apperr.New(codes.AlreadyExists, "duplicate user")).Once()
		d.repo.EXPECT().GetByProviderUserID(ctx, "ext-existing").
			Return(existingUser, nil).Once()

		result, err := d.uc.Create(ctx, params)

		assert.NoError(t, err)
		assert.Equal(t, existingUser, result)
	})

	t.Run("duplicate email with different provider_user_id surfaces AlreadyExists", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		params := &entity.NewUser{
			ProviderUserID: "ext-new-caller",
			Email:          "taken@example.com",
			Name:           "New Caller",
		}

		d.repo.EXPECT().Create(ctx, params).
			Return(nil, apperr.New(codes.AlreadyExists, "duplicate email")).Once()
		// GetByProviderUserID returns NotFound because the caller's provider_user_id is not
		// the conflicting column — the email was claimed by a different identity.
		d.repo.EXPECT().GetByProviderUserID(ctx, "ext-new-caller").
			Return(nil, apperr.New(codes.NotFound, "user not found")).Once()

		result, err := d.uc.Create(ctx, params)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
	})

	// Regression for the 2026-05-23 incident: when GetByProviderUserID fails
	// for a non-NotFound reason (Internal scan failure, Unavailable, etc.),
	// the use-case MUST surface that error class instead of collapsing it
	// into the original AlreadyExists. Previously the retry path discarded
	// getErr entirely, masking a scanUser NULL→string crash as
	// AlreadyExists at the wire with no log trail.
	t.Run("retry GetByProviderUserID non-NotFound error surfaces truthfully", func(t *testing.T) {
		t.Parallel()
		// Table covers the representative non-NotFound classes the retry
		// path can encounter. Internal is the original incident's class
		// (scanUser NULL→string crash); Unavailable covers transient pool
		// / connection failures. Both MUST propagate as themselves so the
		// admin sees the real failure instead of AlreadyExists.
		cases := []struct {
			name           string
			providerUserID string
			email          string
			retryErr       error
			wantClass      error
		}{
			{
				name:           "Internal (scan failure on NULL column)",
				providerUserID: "ext-internal-error",
				email:          "ie@example.com",
				retryErr:       apperr.New(codes.Internal, "scan failure"),
				wantClass:      apperr.ErrInternal,
			},
			{
				name:           "Unavailable (transient pool failure)",
				providerUserID: "ext-unavailable",
				email:          "ua@example.com",
				retryErr:       apperr.New(codes.Unavailable, "connection refused"),
				wantClass:      apperr.ErrUnavailable,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				d := newUserTestDeps(t)

				params := &entity.NewUser{
					ProviderUserID: tc.providerUserID,
					Email:          tc.email,
					Name:           "retry probe",
				}

				d.repo.EXPECT().Create(ctx, params).
					Return(nil, apperr.New(codes.AlreadyExists, "duplicate user")).Once()
				d.repo.EXPECT().GetByProviderUserID(ctx, tc.providerUserID).
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
					ProviderUserID:    "ext-bad-lang",
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

	// Get must surface the repository's error code unchanged: only a missing
	// user is NotFound. Other failures (e.g. a database outage) must not be
	// reported as NotFound, or clients would treat a provisioned user as
	// missing and monitoring would miss the outage. Regression test for
	// https://github.com/liverty-music/backend/issues/471.
	errCases := []struct {
		name    string
		repoErr error
		wantErr error
	}{
		{
			name:    "not found",
			repoErr: apperr.ErrNotFound,
			wantErr: apperr.ErrNotFound,
		},
		{
			name:    "unavailable",
			repoErr: apperr.ErrUnavailable,
			wantErr: apperr.ErrUnavailable,
		},
		{
			name:    "internal",
			repoErr: apperr.ErrInternal,
			wantErr: apperr.ErrInternal,
		},
	}

	for _, tc := range errCases {
		t.Run("error - "+tc.name, func(t *testing.T) {
			t.Parallel()
			d := newUserTestDeps(t)

			d.repo.EXPECT().Get(ctx, "user-123").Return(nil, tc.repoErr).Once()

			result, err := d.uc.Get(ctx, "user-123")

			assert.Error(t, err)
			assert.Nil(t, result)
			assert.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestUserUseCase_GetByProviderUserID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		expectedUser := &entity.User{
			ID:             "user-123",
			ProviderUserID: "ext-123",
			Name:           "John Doe",
			Email:          "john@example.com",
		}

		d.repo.EXPECT().GetByProviderUserID(ctx, "ext-123").Return(expectedUser, nil).Once()

		result, err := d.uc.GetByProviderUserID(ctx, "ext-123")

		assert.NoError(t, err)
		assert.Equal(t, expectedUser, result)
	})

	// GetByProviderUserID must surface the repository's error code unchanged, for
	// the same reason as Get above.
	errCases := []struct {
		name    string
		repoErr error
		wantErr error
	}{
		{
			name:    "not found",
			repoErr: apperr.ErrNotFound,
			wantErr: apperr.ErrNotFound,
		},
		{
			name:    "unavailable",
			repoErr: apperr.ErrUnavailable,
			wantErr: apperr.ErrUnavailable,
		},
		{
			name:    "internal",
			repoErr: apperr.ErrInternal,
			wantErr: apperr.ErrInternal,
		},
	}

	for _, tc := range errCases {
		t.Run("error - "+tc.name, func(t *testing.T) {
			t.Parallel()
			d := newUserTestDeps(t)

			d.repo.EXPECT().GetByProviderUserID(ctx, "ext-123").Return(nil, tc.repoErr).Once()

			result, err := d.uc.GetByProviderUserID(ctx, "ext-123")

			assert.Error(t, err)
			assert.Nil(t, result)
			assert.ErrorIs(t, err, tc.wantErr)
		})
	}
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

		result, err := d.uc.SetHome(ctx, "user-123", home)

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

				result, err := d.uc.SetHome(ctx, "user-123", tt.home)

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

				result, err := d.uc.SetHome(ctx, "user-123", tt.home)

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

		result, err := d.uc.SetHome(ctx, "user-123", home)

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

				result, err := d.uc.SetHome(ctx, "user-123", tt.home)

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

				result, err := d.uc.SetHome(ctx, "user-123", home)

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

		result, err := d.uc.SetHome(ctx, "user-123", home)

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

		updatedUser := &entity.User{
			ID:                "user-lang-1",
			Email:             "lang@example.com",
			PreferredLanguage: "en",
		}

		d.repo.EXPECT().UpdatePreferredLanguage(ctx, "user-lang-1", "en").
			Return(updatedUser, nil).Once()

		result, err := d.uc.SetPreferredLanguage(ctx, "user-lang-1", "en")

		assert.NoError(t, err)
		assert.Equal(t, updatedUser, result)
		assert.Equal(t, "en", result.PreferredLanguage)
	})

	t.Run("not found — repository returns NotFound at update", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		d.repo.EXPECT().UpdatePreferredLanguage(ctx, "ghost-id", "ja").
			Return(nil, apperr.New(codes.NotFound, "user not found")).Once()

		result, err := d.uc.SetPreferredLanguage(ctx, "ghost-id", "ja")

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

				result, err := d.uc.SetPreferredLanguage(ctx, "user-1", bad)

				assert.Nil(t, result)
				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			})
		}
	})

	t.Run("InvalidArgument when id is empty", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		// repo MUST NOT be called.

		result, err := d.uc.SetPreferredLanguage(ctx, "", "ja")

		assert.Nil(t, result)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestUserUseCase_ResolveCaller(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// @spec components/usecase/user/resolve-caller "Signed-in caller with an account"
	t.Run("returns the caller's own User", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		expectedUser := &entity.User{ID: testResolveUserID, ProviderUserID: testResolveExtID}
		d.repo.EXPECT().GetByProviderUserID(ctx, testResolveExtID).Return(expectedUser, nil).Once()

		result, err := d.uc.ResolveCaller(ctx, testResolveExtID)

		assert.NoError(t, err)
		assert.Equal(t, expectedUser, result)
	})

	// @spec components/usecase/user/resolve-caller "Caller without an account"
	t.Run("NotFound — no user exists for providerUserID", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		d.repo.EXPECT().GetByProviderUserID(ctx, "ext-ghost").
			Return(nil, apperr.New(codes.NotFound, "user not found")).Once()

		result, err := d.uc.ResolveCaller(ctx, "ext-ghost")

		assert.Nil(t, result)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

const (
	testResolveUserID = "user-resolve-1"
	testResolveExtID  = "ext-resolve-1"
)

// newUserTestDepsWithPublisher builds test dependencies whose EventPublisher is
// a mock, so a test can assert what is announced or make the announcement fail.
func newUserTestDepsWithPublisher(t *testing.T) (*userTestDeps, *ucmocks.MockEventPublisher) {
	t.Helper()
	publisher := ucmocks.NewMockEventPublisher(t)
	d := &userTestDeps{
		repo:          mocks.NewMockUserRepository(t),
		emailVerifier: mocks.NewMockEmailVerifier(t),
	}
	d.uc = usecase.NewUserUseCase(d.repo, nil, nil, publisher, d.emailVerifier, nil, usecase.PasskeyMailers{}, newTestLogger(t))
	return d, publisher
}

// expectResendAnnounced expects one resend request announced for userID.
func expectResendAnnounced(publisher *ucmocks.MockEventPublisher, userID string, times int) {
	publisher.EXPECT().PublishEvent(mock.Anything, entity.SubjectUserEmailVerificationResendRequested,
		entity.UserEmailVerificationResendRequestedData{UserID: userID}).Return(nil).Times(times)
}

func TestUserUseCase_ResendEmailVerification(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// @spec components/usecase/user/resend-email-verification "Caller resends their own email"
	t.Run("announces a resend request for the caller's User", func(t *testing.T) {
		t.Parallel()
		d, publisher := newUserTestDepsWithPublisher(t)

		d.repo.EXPECT().GetByProviderUserID(ctx, testResolveExtID).
			Return(&entity.User{ID: testResolveUserID, ProviderUserID: testResolveExtID}, nil).Once()
		expectResendAnnounced(publisher, testResolveUserID, 1)
		// The verifier MUST NOT be called: sending is ReissueEmailVerification's part.

		err := d.uc.ResendEmailVerification(ctx, testResolveExtID)

		assert.NoError(t, err)
	})

	// @spec components/usecase/user/resend-email-verification "Caller already verified"
	// @spec components/usecase/user/resend-email-verification "Email verification not configured"
	t.Run("succeeds and announces without a verifier, whatever the address state", func(t *testing.T) {
		t.Parallel()
		publisher := ucmocks.NewMockEventPublisher(t)
		repo := mocks.NewMockUserRepository(t)
		uc := usecase.NewUserUseCase(repo, nil, nil, publisher, nil, nil, usecase.PasskeyMailers{}, newTestLogger(t))

		repo.EXPECT().GetByProviderUserID(ctx, testResolveExtID).
			Return(&entity.User{ID: testResolveUserID, ProviderUserID: testResolveExtID}, nil).Once()
		expectResendAnnounced(publisher, testResolveUserID, 1)

		err := uc.ResendEmailVerification(ctx, testResolveExtID)

		assert.NoError(t, err)
	})

	t.Run("NotFound — the caller has no User", func(t *testing.T) {
		t.Parallel()
		d, _ := newUserTestDepsWithPublisher(t)

		d.repo.EXPECT().GetByProviderUserID(ctx, "ext-ghost").
			Return(nil, apperr.New(codes.NotFound, "user not found")).Once()

		err := d.uc.ResendEmailVerification(ctx, "ext-ghost")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	// @spec components/usecase/user/resend-email-verification "Announcement fails"
	t.Run("Unavailable — the resend request cannot be announced", func(t *testing.T) {
		t.Parallel()
		d, publisher := newUserTestDepsWithPublisher(t)

		d.repo.EXPECT().GetByProviderUserID(ctx, testResolveExtID).
			Return(&entity.User{ID: testResolveUserID, ProviderUserID: testResolveExtID}, nil).Once()
		publisher.EXPECT().PublishEvent(mock.Anything, entity.SubjectUserEmailVerificationResendRequested, mock.Anything).
			Return(errors.New("nats down")).Once()

		err := d.uc.ResendEmailVerification(ctx, testResolveExtID)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})

	// The resend rate limit (3 per rolling 10-minute window) is tracked in
	// memory, scoped to this process/instance — see the Go doc on
	// UserUseCase.ResendEmailVerification. A 4th request within the window
	// is rejected with ResourceExhausted before anything is announced.
	// @spec components/usecase/user/resend-email-verification "Too many requests"
	t.Run("ResourceExhausted — 4th request within the 10-minute window is rate-limited", func(t *testing.T) {
		t.Parallel()
		d, publisher := newUserTestDepsWithPublisher(t)

		const rateExtID = "ext-rate"
		const rateUserID = "user-rate"
		rateUser := &entity.User{ID: rateUserID, ProviderUserID: rateExtID}

		d.repo.EXPECT().GetByProviderUserID(ctx, rateExtID).Return(rateUser, nil).Times(4)
		expectResendAnnounced(publisher, rateUserID, 3)

		for range 3 {
			err := d.uc.ResendEmailVerification(ctx, rateExtID)
			assert.NoError(t, err)
		}

		err := d.uc.ResendEmailVerification(ctx, rateExtID)
		assert.ErrorIs(t, err, apperr.ErrResourceExhausted)
	})

	// Every request counts toward the limit once the caller is resolved, whether
	// or not an email is then sent: three requests for an already verified
	// address send nothing but still use up the quota. Failed announcements
	// count too.
	// @spec components/usecase/user/resend-email-verification "Failed attempts count"
	t.Run("requests that send nothing count toward the limit", func(t *testing.T) {
		t.Parallel()
		d, publisher := newUserTestDepsWithPublisher(t)

		const rateExtID = "ext-rate-failed"
		const rateUserID = "user-rate-failed"
		rateUser := &entity.User{ID: rateUserID, ProviderUserID: rateExtID}

		d.repo.EXPECT().GetByProviderUserID(ctx, rateExtID).Return(rateUser, nil).Times(4)
		publisher.EXPECT().PublishEvent(mock.Anything, entity.SubjectUserEmailVerificationResendRequested, mock.Anything).
			Return(errors.New("nats down")).Times(3)

		for range 3 {
			err := d.uc.ResendEmailVerification(ctx, rateExtID)
			assert.ErrorIs(t, err, apperr.ErrUnavailable)
		}

		err := d.uc.ResendEmailVerification(ctx, rateExtID)
		assert.ErrorIs(t, err, apperr.ErrResourceExhausted)
	})

	// The rate limit is per providerUserID: a different user is unaffected by
	// another user's exhausted quota.
	// @spec components/usecase/user/resend-email-verification "Limit is per user"
	t.Run("rate limit is scoped per user", func(t *testing.T) {
		t.Parallel()
		d, publisher := newUserTestDepsWithPublisher(t)

		const rateExtID = "ext-rate-a"
		const otherExtID = "ext-rate-b"
		const rateUserID = "user-rate-a"
		const otherUserID = "user-rate-b"

		d.repo.EXPECT().GetByProviderUserID(ctx, rateExtID).
			Return(&entity.User{ID: rateUserID, ProviderUserID: rateExtID}, nil).Times(3)
		d.repo.EXPECT().GetByProviderUserID(ctx, otherExtID).
			Return(&entity.User{ID: otherUserID, ProviderUserID: otherExtID}, nil).Once()
		expectResendAnnounced(publisher, rateUserID, 3)
		expectResendAnnounced(publisher, otherUserID, 1)

		for range 3 {
			require.NoError(t, d.uc.ResendEmailVerification(ctx, rateExtID))
		}

		assert.NoError(t, d.uc.ResendEmailVerification(ctx, otherExtID))
	})
}

func TestUserUseCase_ReissueEmailVerification(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	user := &entity.User{ID: testResolveUserID, ProviderUserID: testResolveExtID}

	// @spec components/usecase/user/reissue-email-verification "Unverified address"
	t.Run("sends a fresh code to the User's address", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		d.repo.EXPECT().Get(ctx, testResolveUserID).Return(user, nil).Once()
		d.emailVerifier.EXPECT().ResendVerification(ctx, testResolveExtID).Return(nil).Once()

		assert.NoError(t, d.uc.ReissueEmailVerification(ctx, testResolveUserID))
	})

	// @spec components/usecase/user/reissue-email-verification "Already verified"
	t.Run("finishes when the address is already verified", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		d.repo.EXPECT().Get(ctx, testResolveUserID).Return(user, nil).Once()
		d.emailVerifier.EXPECT().ResendVerification(ctx, testResolveExtID).
			Return(apperr.New(codes.FailedPrecondition, "email is already verified")).Once()

		assert.NoError(t, d.uc.ReissueEmailVerification(ctx, testResolveUserID))
	})

	// @spec components/usecase/user/reissue-email-verification "Account deleted since the request"
	t.Run("finishes without sending when the User is gone", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		d.repo.EXPECT().Get(ctx, testResolveUserID).
			Return(nil, apperr.New(codes.NotFound, "user not found")).Once()
		// The verifier MUST NOT be called.

		assert.NoError(t, d.uc.ReissueEmailVerification(ctx, testResolveUserID))
	})

	// @spec components/usecase/user/reissue-email-verification "Verification not configured"
	t.Run("finishes without sending when the verifier is not configured", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDepsNoVerifier(t)
		// Nothing is read or sent.

		assert.NoError(t, d.uc.ReissueEmailVerification(ctx, testResolveUserID))
	})

	// @spec components/usecase/user/reissue-email-verification "Identity provider down"
	t.Run("fails so the request runs again when the identity provider is down", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		d.repo.EXPECT().Get(ctx, testResolveUserID).Return(user, nil).Once()
		d.emailVerifier.EXPECT().ResendVerification(ctx, testResolveExtID).
			Return(apperr.New(codes.Internal, "zitadel unavailable")).Once()

		assert.ErrorIs(t, d.uc.ReissueEmailVerification(ctx, testResolveUserID), apperr.ErrInternal)
	})

	t.Run("fails so the request runs again when the User cannot be read", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		d.repo.EXPECT().Get(ctx, testResolveUserID).
			Return(nil, apperr.New(codes.Unavailable, "database unavailable")).Once()

		assert.ErrorIs(t, d.uc.ReissueEmailVerification(ctx, testResolveUserID), apperr.ErrUnavailable)
	})
}

func TestUserUseCase_Delete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const userID = "019a0000-0000-7000-8000-0000000000u1"
	user := &entity.User{ID: userID, ProviderUserID: "zitadel-user-1"}
	uid := entity.UserID(userID)
	refunded := []*entity.Order{{ID: "order-1", UserID: uid, Status: entity.OrderStatusRefunded}}
	voided := []*entity.Ticket{{ID: "ticket-1", UserID: uid, Status: entity.TicketStatusVoided}}

	tests := []struct {
		name    string
		setup   func(d *userTestDeps)
		wantErr error
	}{
		{
			// @spec components/usecase/user/delete "Existing user"
			name: "removes the identity and then the user",
			setup: func(d *userTestDeps) {
				d.repo.EXPECT().Get(ctx, userID).Return(user, nil).Once()
				d.ticketRepo.EXPECT().ListByUser(ctx, uid).Return(nil, nil).Once()
				d.orderRepo.EXPECT().ListByUser(ctx, uid).Return([]*entity.Order{}, nil).Once()
				identity := d.identityRemover.EXPECT().DeleteIdentity(ctx, "zitadel-user-1").Return(nil).Once()
				d.repo.EXPECT().Delete(ctx, userID).Return(nil).Once().NotBefore(identity)
			},
		},
		{
			// @spec components/usecase/user/delete "Unknown user"
			name: "returns NotFound for an unknown user",
			setup: func(d *userTestDeps) {
				d.repo.EXPECT().Get(ctx, userID).Return(nil, apperr.New(codes.NotFound, "user not found")).Once()
			},
			wantErr: apperr.ErrNotFound,
		},
		{
			// @spec components/usecase/user/delete "User holding a ticket"
			name: "refuses a user holding an issued ticket and removes nothing",
			setup: func(d *userTestDeps) {
				d.repo.EXPECT().Get(ctx, userID).Return(user, nil).Once()
				d.ticketRepo.EXPECT().ListByUser(ctx, uid).
					Return([]*entity.Ticket{{ID: "ticket-1", UserID: uid, Status: entity.TicketStatusIssued}}, nil).Once()
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name: "refuses a user with a paid order and removes nothing",
			setup: func(d *userTestDeps) {
				d.repo.EXPECT().Get(ctx, userID).Return(user, nil).Once()
				d.ticketRepo.EXPECT().ListByUser(ctx, uid).Return(voided, nil).Once()
				d.orderRepo.EXPECT().ListByUser(ctx, uid).
					Return([]*entity.Order{{ID: "order-1", UserID: uid, Status: entity.OrderStatusPaid}}, nil).Once()
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			// @spec components/usecase/user/delete "User with only refunded purchases"
			name: "removes a user whose only order is refunded and whose ticket is voided",
			setup: func(d *userTestDeps) {
				d.repo.EXPECT().Get(ctx, userID).Return(user, nil).Once()
				d.ticketRepo.EXPECT().ListByUser(ctx, uid).Return(voided, nil).Once()
				d.orderRepo.EXPECT().ListByUser(ctx, uid).Return(refunded, nil).Once()
				d.identityRemover.EXPECT().DeleteIdentity(ctx, "zitadel-user-1").Return(nil).Once()
				d.repo.EXPECT().Delete(ctx, userID).Return(nil).Once()
			},
		},
		{
			name: "keeps the user record when the identity cannot be removed",
			setup: func(d *userTestDeps) {
				d.repo.EXPECT().Get(ctx, userID).Return(user, nil).Once()
				d.ticketRepo.EXPECT().ListByUser(ctx, uid).Return(nil, nil).Once()
				d.orderRepo.EXPECT().ListByUser(ctx, uid).Return(nil, nil).Once()
				d.identityRemover.EXPECT().DeleteIdentity(ctx, "zitadel-user-1").
					Return(apperr.New(codes.Internal, "delete zitadel user")).Once()
			},
			wantErr: apperr.ErrInternal,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newUserTestDeps(t)
			tt.setup(d)

			err := d.uc.Delete(ctx, userID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}

	// @spec components/usecase/user/delete "Retry after the record failed"
	t.Run("a second call removes the user after the record removal failed", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		d.repo.EXPECT().Get(ctx, userID).Return(user, nil).Twice()
		d.ticketRepo.EXPECT().ListByUser(ctx, uid).Return(nil, nil).Twice()
		d.orderRepo.EXPECT().ListByUser(ctx, uid).Return(nil, nil).Twice()
		// The identity is already gone on the retry, which DeleteIdentity
		// reports as success.
		d.identityRemover.EXPECT().DeleteIdentity(ctx, "zitadel-user-1").Return(nil).Twice()
		d.repo.EXPECT().Delete(ctx, userID).Return(apperr.New(codes.Unavailable, "db down")).Once()
		d.repo.EXPECT().Delete(ctx, userID).Return(nil).Once()

		assert.ErrorIs(t, d.uc.Delete(ctx, userID), apperr.ErrUnavailable)
		assert.NoError(t, d.uc.Delete(ctx, userID))
	})
}

func TestUserUseCase_Update(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const extID = "ext-update-1"
	strPtr := func(s string) *string { return &s }
	current := func() *entity.User {
		return &entity.User{ID: "user-update-1", ProviderUserID: extID, FullName: "山田 花子", PhoneNumber: "+819012345678"}
	}

	type args struct {
		fullName    *string
		phoneNumber *string
	}
	tests := []struct {
		name     string
		args     args
		setup    func(d *userTestDeps)
		wantName string
		wantErr  error
	}{
		{
			// @spec components/usecase/user/update "Fan corrects their name"
			name: "fan corrects their name",
			args: args{fullName: strPtr("佐藤 花子")},
			setup: func(d *userTestDeps) {
				d.repo.EXPECT().GetByProviderUserID(ctx, extID).Return(current(), nil).Once()
				d.repo.EXPECT().UpdatePersonalDetails(ctx, "user-update-1", "佐藤 花子", "+819012345678").
					Return(&entity.User{ID: "user-update-1", FullName: "佐藤 花子", PhoneNumber: "+819012345678"}, nil).Once()
			},
			wantName: "佐藤 花子",
		},
		{
			// @spec components/usecase/user/update "Only the phone number given"
			name: "only the phone number given",
			args: args{phoneNumber: strPtr("+818011112222")},
			setup: func(d *userTestDeps) {
				d.repo.EXPECT().GetByProviderUserID(ctx, extID).Return(current(), nil).Once()
				d.repo.EXPECT().UpdatePersonalDetails(ctx, "user-update-1", "山田 花子", "+818011112222").
					Return(&entity.User{ID: "user-update-1", FullName: "山田 花子", PhoneNumber: "+818011112222"}, nil).Once()
			},
			wantName: "山田 花子",
		},
		{
			// @spec components/usecase/user/update "Invalid phone number"
			name: "invalid phone number",
			args: args{phoneNumber: strPtr("090-1234-5678")},
			setup: func(d *userTestDeps) {
				d.repo.EXPECT().GetByProviderUserID(ctx, extID).Return(current(), nil).Once()
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "nothing given",
			args:    args{},
			setup:   func(*userTestDeps) {},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "empty full name given",
			args:    args{fullName: strPtr("")},
			setup:   func(*userTestDeps) {},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "caller without an account",
			args: args{fullName: strPtr("佐藤 花子")},
			setup: func(d *userTestDeps) {
				d.repo.EXPECT().GetByProviderUserID(ctx, extID).Return(nil, apperr.New(codes.NotFound, "user not found")).Once()
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newUserTestDeps(t)
			tt.setup(d)

			got, err := d.uc.Update(ctx, extID, tt.args.fullName, tt.args.phoneNumber)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantName, got.FullName)
			// The faces of the fan's Tickets read the User's current values.
			face := (&entity.Ticket{UserID: entity.UserID(got.ID), ResaleWithoutConsentProhibited: true}).Face(got)
			assert.Equal(t, tt.wantName, face.FullName)
		})
	}
}
