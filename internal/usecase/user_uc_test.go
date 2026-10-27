package usecase_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

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

// userTestNow is the clock of the user usecase tests: noon on 2026-10-27 in
// Japan time.
var userTestNow = time.Date(2026, 10, 27, 12, 0, 0, 0, time.FixedZone("Asia/Tokyo", 9*60*60))

type userTestDeps struct {
	repo            *mocks.MockUserRepository
	ticketRepo      *mocks.MockTicketRepository
	eventRepo       *mocks.MockEventRepository
	listingRepo     *mocks.MockResaleListingRepository
	entryRepo       *mocks.MockLotteryEntryRepository
	checkoutRepo    *mocks.MockCheckoutRepository
	orderRepo       *mocks.MockOrderRepository
	emailVerifier   *mocks.MockEmailVerifier
	emailCodes      *mocks.MockEmailCodeVerifier
	identityRemover *mocks.MockIdentityRemover
	uc              usecase.UserUseCase
}

func newUserTestDeps(t *testing.T) *userTestDeps {
	t.Helper()
	d := &userTestDeps{
		repo:            mocks.NewMockUserRepository(t),
		ticketRepo:      mocks.NewMockTicketRepository(t),
		eventRepo:       mocks.NewMockEventRepository(t),
		listingRepo:     mocks.NewMockResaleListingRepository(t),
		entryRepo:       mocks.NewMockLotteryEntryRepository(t),
		checkoutRepo:    mocks.NewMockCheckoutRepository(t),
		orderRepo:       mocks.NewMockOrderRepository(t),
		emailVerifier:   mocks.NewMockEmailVerifier(t),
		emailCodes:      mocks.NewMockEmailCodeVerifier(t),
		identityRemover: mocks.NewMockIdentityRemover(t),
	}
	d.uc = usecase.NewUserUseCase(d.repo, usecase.DeletionBlockerReads{
		Tickets:        d.ticketRepo,
		Events:         d.eventRepo,
		ResaleListings: d.listingRepo,
		LotteryEntries: d.entryRepo,
		Checkouts:      d.checkoutRepo,
		Orders:         d.orderRepo,
		Now:            func() time.Time { return userTestNow },
	}, messaging.NewEventPublisher(newTestPublisher()), d.emailVerifier, d.emailCodes, d.identityRemover, usecase.PasskeyMailers{}, newTestLogger(t))
	return d
}

// newUserTestDepsNoVerifier builds test dependencies with a nil EmailVerifier,
// mirroring local dev where the Zitadel API client is not configured.
func newUserTestDepsNoVerifier(t *testing.T) *userTestDeps {
	t.Helper()
	d := &userTestDeps{
		repo: mocks.NewMockUserRepository(t),
	}
	d.uc = usecase.NewUserUseCase(d.repo, usecase.DeletionBlockerReads{}, messaging.NewEventPublisher(newTestPublisher()), nil, nil, nil, usecase.PasskeyMailers{}, newTestLogger(t))
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
	d.uc = usecase.NewUserUseCase(d.repo, usecase.DeletionBlockerReads{}, publisher, d.emailVerifier, nil, nil, usecase.PasskeyMailers{}, newTestLogger(t))
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
		uc := usecase.NewUserUseCase(repo, usecase.DeletionBlockerReads{}, publisher, nil, nil, nil, usecase.PasskeyMailers{}, newTestLogger(t))

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
		d.emailVerifier.EXPECT().ResendVerification(ctx, user).Return(nil).Once()

		assert.NoError(t, d.uc.ReissueEmailVerification(ctx, testResolveUserID))
	})

	// @spec components/usecase/user/reissue-email-verification "Already verified"
	t.Run("finishes when the address is already verified", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)

		d.repo.EXPECT().Get(ctx, testResolveUserID).Return(user, nil).Once()
		d.emailVerifier.EXPECT().ResendVerification(ctx, user).
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
		d.emailVerifier.EXPECT().ResendVerification(ctx, user).
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

func TestUserUseCase_VerifyEmail(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	user := &entity.User{ID: testResolveUserID, ProviderUserID: testResolveExtID}

	tests := []struct {
		name    string
		setup   func(d *userTestDeps)
		wantErr error
	}{
		{
			// @spec components/usecase/user/verify-email "Link from the email"
			name: "verifies the User's address with the code",
			setup: func(d *userTestDeps) {
				d.repo.EXPECT().Get(ctx, testResolveUserID).Return(user, nil).Once()
				d.emailCodes.EXPECT().VerifyEmail(ctx, testResolveExtID, "ABC123").Return(nil).Once()
			},
		},
		{
			// @spec components/usecase/user/verify-email "Unknown User"
			name: "an unknown User fails like a wrong code",
			setup: func(d *userTestDeps) {
				d.repo.EXPECT().Get(ctx, testResolveUserID).Return(nil, apperr.New(codes.NotFound, "user not found")).Once()
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			// @spec components/usecase/user/verify-email "Expired or used link"
			name: "a wrong, expired or used code is InvalidArgument",
			setup: func(d *userTestDeps) {
				d.repo.EXPECT().Get(ctx, testResolveUserID).Return(user, nil).Once()
				d.emailCodes.EXPECT().VerifyEmail(ctx, testResolveExtID, "ABC123").Return(apperr.New(codes.InvalidArgument, "code is not valid")).Once()
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "an identity gone at the identity provider fails like a wrong code",
			setup: func(d *userTestDeps) {
				d.repo.EXPECT().Get(ctx, testResolveUserID).Return(user, nil).Once()
				d.emailCodes.EXPECT().VerifyEmail(ctx, testResolveExtID, "ABC123").Return(apperr.New(codes.NotFound, "identity not found")).Once()
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			// @spec components/usecase/user/verify-email "Identity provider down"
			name: "an unreachable identity provider is Unavailable",
			setup: func(d *userTestDeps) {
				d.repo.EXPECT().Get(ctx, testResolveUserID).Return(user, nil).Once()
				d.emailCodes.EXPECT().VerifyEmail(ctx, testResolveExtID, "ABC123").Return(apperr.New(codes.Unavailable, "down")).Once()
			},
			wantErr: apperr.ErrUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newUserTestDeps(t)
			tt.setup(d)

			err := d.uc.VerifyEmail(ctx, testResolveUserID, "ABC123")

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}

	// @spec components/usecase/user/verify-email "Sixth attempt for one User"
	t.Run("the sixth attempt for one User in 60 minutes reads nothing", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		d.repo.EXPECT().Get(ctx, testResolveUserID).Return(nil, apperr.New(codes.NotFound, "user not found")).Times(5)

		for range 5 {
			assert.ErrorIs(t, d.uc.VerifyEmail(ctx, testResolveUserID, "WRONG1"), apperr.ErrInvalidArgument)
		}
		assert.ErrorIs(t, d.uc.VerifyEmail(ctx, testResolveUserID, "ABC123"), apperr.ErrResourceExhausted)
	})

	t.Run("another User id has its own attempts", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		const otherID = "019a0000-0000-7000-8000-0000000000u2"
		d.repo.EXPECT().Get(ctx, testResolveUserID).Return(nil, apperr.New(codes.NotFound, "user not found")).Times(5)
		d.repo.EXPECT().Get(ctx, otherID).Return(nil, apperr.New(codes.NotFound, "user not found")).Once()

		for range 5 {
			_ = d.uc.VerifyEmail(ctx, testResolveUserID, "WRONG1")
		}
		assert.ErrorIs(t, d.uc.VerifyEmail(ctx, otherID, "ABC123"), apperr.ErrInvalidArgument)
	})

	t.Run("without the identity service the call is Unavailable", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDepsNoVerifier(t)

		assert.ErrorIs(t, d.uc.VerifyEmail(ctx, testResolveUserID, "ABC123"), apperr.ErrUnavailable)
	})
}

// deletionTestUser is the User the deletion tests act on.
var deletionTestUser = &entity.User{ID: "019a0000-0000-7000-8000-0000000000a1", ProviderUserID: "zitadel-user-1"}

// eventOn returns an event whose local date is days after userTestNow's date
// in Japan time (negative: before).
func eventOn(id string, days int) *entity.Event {
	y, m, d := userTestNow.Date()
	return &entity.Event{ID: id, LocalDate: time.Date(y, m, d+days, 0, 0, 0, 0, time.UTC)}
}

// deletionReads are what the deletion tests' User has; a nil list is empty.
type deletionReads struct {
	tickets   []*entity.Ticket
	events    []*entity.Event
	listings  []*entity.ResaleListing
	entries   []*entity.LotteryEntry
	checkouts []*entity.Checkout
	// issued are the Won entries that have an Order; every other Won entry
	// has none.
	issued []entity.LotteryEntryID
}

// expect makes the read mocks return r for the deletion test user.
func (r deletionReads) expect(ctx context.Context, d *userTestDeps) {
	uid := entity.UserID(deletionTestUser.ID)
	d.ticketRepo.EXPECT().ListByUser(ctx, uid).Return(r.tickets, nil).Once()
	for _, e := range r.events {
		d.eventRepo.EXPECT().Get(ctx, e.ID).Return(e, nil).Once()
	}
	d.listingRepo.EXPECT().ListByUser(ctx, uid).Return(r.listings, nil).Once()
	d.entryRepo.EXPECT().ListByUser(ctx, uid).Return(r.entries, nil).Once()
	for _, e := range r.entries {
		if e.State != entity.LotteryEntryStateWon {
			continue
		}
		if slices.Contains(r.issued, e.ID) {
			d.orderRepo.EXPECT().GetByLotteryEntryID(ctx, e.ID).Return(&entity.Order{ID: entity.OrderID("o-" + string(e.ID)), LotteryEntryID: e.ID}, nil).Once()
		} else {
			d.orderRepo.EXPECT().GetByLotteryEntryID(ctx, e.ID).Return(nil, apperr.New(codes.NotFound, "no order")).Once()
		}
	}
	d.checkoutRepo.EXPECT().ListByUser(ctx, uid).Return(r.checkouts, nil).Once()
}

// issuedTicketFor returns an Issued ticket of the deletion test user for the
// event.
func issuedTicketFor(id, eventID string) *entity.Ticket {
	return &entity.Ticket{ID: entity.TicketID(id), UserID: entity.UserID(deletionTestUser.ID), EventID: eventID, Status: entity.TicketStatusIssued}
}

func TestUserUseCase_ListDeletionBlockers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	userID := deletionTestUser.ID
	tests := []struct {
		name  string
		reads deletionReads
		want  []entity.DeletionBlocker
	}{
		{
			// @spec components/usecase/user/list-deletion-blockers "Ticket for a concert next week"
			name: "counts two issued tickets for an event a week from today as one upcoming ticket blocker",
			reads: deletionReads{
				tickets: []*entity.Ticket{issuedTicketFor("t1", "e1"), issuedTicketFor("t2", "e1")},
				events:  []*entity.Event{eventOn("e1", 7)},
			},
			want: []entity.DeletionBlocker{{Kind: entity.DeletionBlockerKindUpcomingTicket, Count: 2}},
		},
		{
			// @spec components/usecase/user/list-deletion-blockers "Ticket for today's concert"
			name: "counts an issued ticket for today's event in Japan time",
			reads: deletionReads{
				tickets: []*entity.Ticket{issuedTicketFor("t1", "e1")},
				events:  []*entity.Event{eventOn("e1", 0)},
			},
			want: []entity.DeletionBlocker{{Kind: entity.DeletionBlockerKindUpcomingTicket, Count: 1}},
		},
		{
			// @spec components/usecase/user/list-deletion-blockers "Ticket for a past concert"
			name: "ignores an issued ticket for yesterday's event",
			reads: deletionReads{
				tickets: []*entity.Ticket{issuedTicketFor("t1", "e1")},
				events:  []*entity.Event{eventOn("e1", -1)},
			},
			want: []entity.DeletionBlocker{},
		},
		{
			name: "ignores a voided ticket without reading its event",
			reads: deletionReads{
				tickets: []*entity.Ticket{{ID: "t1", EventID: "e1", Status: entity.TicketStatusVoided}},
			},
			want: []entity.DeletionBlocker{},
		},
		{
			// @spec components/usecase/user/list-deletion-blockers "Listed resale"
			name:  "counts a listed resale listing",
			reads: deletionReads{listings: []*entity.ResaleListing{{ID: "l1", Status: entity.ResaleListingStatusListed}}},
			want:  []entity.DeletionBlocker{{Kind: entity.DeletionBlockerKindListedResale, Count: 1}},
		},
		{
			// @spec components/usecase/user/list-deletion-blockers "Entry awaiting its draw"
			name:  "counts an entered lottery entry",
			reads: deletionReads{entries: []*entity.LotteryEntry{{ID: "le1", State: entity.LotteryEntryStateEntered}}},
			want:  []entity.DeletionBlocker{{Kind: entity.DeletionBlockerKindPendingLotteryEntry, Count: 1}},
		},
		{
			// @spec components/usecase/user/list-deletion-blockers "Card authorized, not yet charged"
			name:  "counts a committed checkout",
			reads: deletionReads{checkouts: []*entity.Checkout{{ID: "c1", Status: entity.CheckoutStatusCommitted}}},
			want:  []entity.DeletionBlocker{{Kind: entity.DeletionBlockerKindPaymentInProgress, Count: 1}},
		},
		{
			name: "counts a held checkout before its hold expiry",
			reads: deletionReads{checkouts: []*entity.Checkout{
				{ID: "c1", Status: entity.CheckoutStatusHeld, HoldExpireTime: userTestNow.Add(5 * time.Minute)},
			}},
			want: []entity.DeletionBlocker{{Kind: entity.DeletionBlockerKindPaymentInProgress, Count: 1}},
		},
		{
			// @spec components/usecase/user/list-deletion-blockers "Win being charged"
			name:  "counts a won entry with no order as a payment in progress",
			reads: deletionReads{entries: []*entity.LotteryEntry{{ID: "le1", State: entity.LotteryEntryStateWon}}},
			want:  []entity.DeletionBlocker{{Kind: entity.DeletionBlockerKindPaymentInProgress, Count: 1}},
		},
		{
			// @spec components/usecase/user/list-deletion-blockers "Win already issued"
			name: "ignores a won entry with an order whose ticket is for a past event",
			reads: deletionReads{
				tickets: []*entity.Ticket{issuedTicketFor("t1", "e1")},
				events:  []*entity.Event{eventOn("e1", -1)},
				entries: []*entity.LotteryEntry{{ID: "le1", State: entity.LotteryEntryStateWon}},
				issued:  []entity.LotteryEntryID{"le1"},
			},
			want: []entity.DeletionBlocker{},
		},
		{
			// @spec components/usecase/user/list-deletion-blockers "Lapsed hold"
			name: "ignores a held checkout whose hold expired 10 minutes ago",
			reads: deletionReads{checkouts: []*entity.Checkout{
				{ID: "c1", Status: entity.CheckoutStatusHeld, HoldExpireTime: userTestNow.Add(-10 * time.Minute)},
			}},
			want: []entity.DeletionBlocker{},
		},
		{
			// @spec components/usecase/user/list-deletion-blockers "Finished entries and withdrawn listings"
			name: "ignores a lost entry and a withdrawn listing",
			reads: deletionReads{
				listings: []*entity.ResaleListing{{ID: "l1", Status: entity.ResaleListingStatusWithdrawn}},
				entries:  []*entity.LotteryEntry{{ID: "le1", State: entity.LotteryEntryStateLost}},
			},
			want: []entity.DeletionBlocker{},
		},
		{
			name: "ignores completed, expired and released checkouts and issued wins",
			reads: deletionReads{
				entries: []*entity.LotteryEntry{{ID: "le1", State: entity.LotteryEntryStateWon}},
				issued:  []entity.LotteryEntryID{"le1"},
				checkouts: []*entity.Checkout{
					{ID: "c1", Status: entity.CheckoutStatusCompleted},
					{ID: "c2", Status: entity.CheckoutStatusExpired},
					{ID: "c3", Status: entity.CheckoutStatusReleased},
				},
			},
			want: []entity.DeletionBlocker{},
		},
		{
			name: "returns every kind the user has in a fixed order",
			reads: deletionReads{
				tickets:   []*entity.Ticket{issuedTicketFor("t1", "e1")},
				events:    []*entity.Event{eventOn("e1", 3)},
				listings:  []*entity.ResaleListing{{ID: "l1", Status: entity.ResaleListingStatusListed}},
				entries:   []*entity.LotteryEntry{{ID: "le1", State: entity.LotteryEntryStateEntered}},
				checkouts: []*entity.Checkout{{ID: "c1", Status: entity.CheckoutStatusCommitted}},
			},
			want: []entity.DeletionBlocker{
				{Kind: entity.DeletionBlockerKindUpcomingTicket, Count: 1},
				{Kind: entity.DeletionBlockerKindListedResale, Count: 1},
				{Kind: entity.DeletionBlockerKindPendingLotteryEntry, Count: 1},
				{Kind: entity.DeletionBlockerKindPaymentInProgress, Count: 1},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newUserTestDeps(t)
			d.repo.EXPECT().Get(ctx, userID).Return(deletionTestUser, nil).Once()
			tt.reads.expect(ctx, d)

			got, err := d.uc.ListDeletionBlockers(ctx, userID)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	// @spec components/usecase/user/list-deletion-blockers "Unknown user"
	t.Run("returns NotFound for an unknown user and reads nothing else", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		d.repo.EXPECT().Get(ctx, userID).Return(nil, apperr.New(codes.NotFound, "user not found")).Once()

		_, err := d.uc.ListDeletionBlockers(ctx, userID)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("returns a failed read with its own code", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		d.repo.EXPECT().Get(ctx, userID).Return(deletionTestUser, nil).Once()
		d.ticketRepo.EXPECT().ListByUser(ctx, entity.UserID(userID)).Return(nil, apperr.New(codes.Unavailable, "db down")).Once()

		_, err := d.uc.ListDeletionBlockers(ctx, userID)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})
}

func TestUserUseCase_Delete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	userID := deletionTestUser.ID
	// serialized runs the deletion under the mocked lock, so the checks and
	// removals inside it are what the test sees.
	serialized := func(d *userTestDeps) {
		d.repo.EXPECT().SerializeDeletion(ctx, userID, mock.Anything).
			RunAndReturn(func(ctx context.Context, _ string, fn func(context.Context) error) error { return fn(ctx) }).Once()
	}
	// getUser expects the User to be read before the lock and inside
	// ListDeletionBlockers.
	getUser := func(d *userTestDeps) {
		d.repo.EXPECT().Get(ctx, userID).Return(deletionTestUser, nil).Twice()
	}
	// removed expects the identity and then the record to be removed.
	removed := func(d *userTestDeps) {
		identity := d.identityRemover.EXPECT().DeleteIdentity(ctx, "zitadel-user-1").Return(nil).Once()
		d.repo.EXPECT().Delete(ctx, userID).Return(nil).Once().NotBefore(identity)
	}

	tests := []struct {
		name    string
		reads   *deletionReads
		setup   func(d *userTestDeps)
		wantErr error
	}{
		{
			// @spec components/usecase/user/delete "Existing user"
			name:  "removes the identity and then the user",
			reads: &deletionReads{},
			setup: removed,
		},
		{
			// @spec components/usecase/user/delete "User holding a ticket"
			name: "refuses a user holding an issued ticket for an upcoming event and removes nothing",
			reads: &deletionReads{
				tickets: []*entity.Ticket{issuedTicketFor("t1", "e1")},
				events:  []*entity.Event{eventOn("e1", 0)},
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			// @spec components/usecase/user/delete "User whose concerts are over"
			name: "removes a user whose only ticket is for a past event",
			reads: &deletionReads{
				tickets: []*entity.Ticket{issuedTicketFor("t1", "e1")},
				events:  []*entity.Event{eventOn("e1", -1)},
			},
			setup: removed,
		},
		{
			// @spec components/usecase/user/delete "User with a listed resale"
			name:    "refuses a user with a listed resale",
			reads:   &deletionReads{listings: []*entity.ResaleListing{{ID: "l1", Status: entity.ResaleListingStatusListed}}},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			// @spec components/usecase/user/delete "User with an entry awaiting its draw"
			name:    "refuses a user with an entered lottery entry",
			reads:   &deletionReads{entries: []*entity.LotteryEntry{{ID: "le1", State: entity.LotteryEntryStateEntered}}},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			// @spec components/usecase/user/delete "User paying for tickets"
			name:    "refuses a user with a committed checkout",
			reads:   &deletionReads{checkouts: []*entity.Checkout{{ID: "c1", Status: entity.CheckoutStatusCommitted}}},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			// @spec components/usecase/user/delete "User whose win is being charged"
			name:    "refuses a user with a won entry that has no order yet",
			reads:   &deletionReads{entries: []*entity.LotteryEntry{{ID: "le1", State: entity.LotteryEntryStateWon}}},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			// @spec components/usecase/user/delete "User with only refunded purchases"
			name: "removes a user whose only ticket is voided",
			reads: &deletionReads{
				tickets: []*entity.Ticket{{ID: "t1", EventID: "e1", Status: entity.TicketStatusVoided}},
			},
			setup: removed,
		},
		{
			name:  "keeps the user record when the identity cannot be removed",
			reads: &deletionReads{},
			setup: func(d *userTestDeps) {
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
			getUser(d)
			serialized(d)
			tt.reads.expect(ctx, d)
			if tt.setup != nil {
				tt.setup(d)
			}

			err := d.uc.Delete(ctx, userID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}

	// @spec components/usecase/user/delete "Unknown user"
	t.Run("returns NotFound for an unknown user", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		d.repo.EXPECT().Get(ctx, userID).Return(nil, apperr.New(codes.NotFound, "user not found")).Once()

		assert.ErrorIs(t, d.uc.Delete(ctx, userID), apperr.ErrNotFound)
	})

	// @spec components/usecase/user/delete "Retry after the record failed"
	t.Run("a second call removes the user after the record removal failed", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		d.repo.EXPECT().Get(ctx, userID).Return(deletionTestUser, nil).Times(4)
		d.repo.EXPECT().SerializeDeletion(ctx, userID, mock.Anything).
			RunAndReturn(func(ctx context.Context, _ string, fn func(context.Context) error) error { return fn(ctx) }).Twice()
		uid := entity.UserID(userID)
		d.ticketRepo.EXPECT().ListByUser(ctx, uid).Return(nil, nil).Twice()
		d.listingRepo.EXPECT().ListByUser(ctx, uid).Return(nil, nil).Twice()
		d.entryRepo.EXPECT().ListByUser(ctx, uid).Return(nil, nil).Twice()
		d.checkoutRepo.EXPECT().ListByUser(ctx, uid).Return(nil, nil).Twice()
		// The identity is already gone on the retry, which DeleteIdentity
		// reports as success.
		d.identityRemover.EXPECT().DeleteIdentity(ctx, "zitadel-user-1").Return(nil).Twice()
		d.repo.EXPECT().Delete(ctx, userID).Return(apperr.New(codes.Unavailable, "db down")).Once()
		d.repo.EXPECT().Delete(ctx, userID).Return(nil).Once()

		assert.ErrorIs(t, d.uc.Delete(ctx, userID), apperr.ErrUnavailable)
		assert.NoError(t, d.uc.Delete(ctx, userID))
	})
}

func TestUserUseCase_GetByEmail(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// @spec components/usecase/user/get-by-email "Registered address"
	t.Run("returns the user the address belongs to, letter case ignored by the repository", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		stored := &entity.User{ID: "u1", Email: "fan@example.com"}
		d.repo.EXPECT().GetByEmail(ctx, "Fan@Example.com").Return(stored, nil).Once()

		got, err := d.uc.GetByEmail(ctx, "Fan@Example.com")

		require.NoError(t, err)
		assert.Equal(t, stored, got)
	})

	// @spec components/usecase/user/get-by-email "Unregistered address"
	t.Run("returns NotFound when no user has the address", func(t *testing.T) {
		t.Parallel()
		d := newUserTestDeps(t)
		d.repo.EXPECT().GetByEmail(ctx, "nobody@example.com").Return(nil, apperr.New(codes.NotFound, "user not found")).Once()

		_, err := d.uc.GetByEmail(ctx, "nobody@example.com")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
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
