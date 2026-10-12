package rdb_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestUser(providerUserID, email, name string) *entity.NewUser {
	return &entity.NewUser{
		ProviderUserID:    providerUserID,
		Email:             email,
		Name:              name,
		PreferredLanguage: "ja",
		TimeZone:          "Asia/Tokyo",
	}
}

func newTestUserWithHome(providerUserID, email, name string) *entity.NewUser {
	u := newTestUser(providerUserID, email, name)
	u.Home = &entity.Home{
		CountryCode: "JP",
		Level1:      "JP-13",
	}
	return u
}

func TestUserRepository_Create(t *testing.T) {
	repo := rdb.NewUserRepository(testDB)
	ctx := context.Background()

	type args struct {
		params *entity.NewUser
	}

	tests := []struct {
		name    string
		setup   func()
		args    args
		wantErr error
	}{
		{
			name:  "creates a user successfully",
			setup: func() { cleanDatabase(t) },
			args: args{
				params: newTestUser("ext-001", "alice@example.com", "Alice"),
			},
			wantErr: nil,
		},
		{
			name:  "creates a user with home",
			setup: func() { cleanDatabase(t) },
			args: args{
				params: newTestUserWithHome("ext-home-1", "withhome@example.com", "WithHome"),
			},
			wantErr: nil,
		},
		{
			name:  "nil params returns error",
			setup: func() { cleanDatabase(t) },
			args: args{
				params: nil,
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "duplicate email returns already exists",
			setup: func() {
				cleanDatabase(t)
				_, err := repo.Create(ctx, newTestUser("ext-dup-1", "dup@example.com", "First"))
				require.NoError(t, err)
			},
			args: args{
				params: newTestUser("ext-dup-2", "dup@example.com", "Second"),
			},
			wantErr: apperr.ErrAlreadyExists,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()

			got, err := repo.Create(ctx, tt.args.params)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.NotEmpty(t, got.ID)
			assert.Equal(t, tt.args.params.Email, got.Email)
			assert.Equal(t, tt.args.params.Name, got.Name)
			assert.Equal(t, tt.args.params.ProviderUserID, got.ProviderUserID)
			// Create returns the in-memory entity built from params, so
			// PreferredLanguage MUST round-trip through entity.CreateUser.
			// Catches regressions where a refactor would drop the field
			// from the constructor copy.
			assert.Equal(t, tt.args.params.PreferredLanguage, got.PreferredLanguage)

			if tt.args.params.Home != nil {
				require.NotNil(t, got.Home)
				assert.NotEmpty(t, got.Home.ID)
				assert.Equal(t, tt.args.params.Home.CountryCode, got.Home.CountryCode)
				assert.Equal(t, tt.args.params.Home.Level1, got.Home.Level1)
			} else {
				assert.Nil(t, got.Home)
			}
		})
	}
}

func TestUserRepository_Get(t *testing.T) {
	repo := rdb.NewUserRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name     string
		setup    func() string // returns user ID
		wantHome bool
		wantErr  error
	}{
		{
			name: "retrieves existing user",
			setup: func() string {
				cleanDatabase(t)
				user, err := repo.Create(ctx, newTestUser("ext-get-1", "get@example.com", "GetUser"))
				require.NoError(t, err)
				return user.ID
			},
			wantErr: nil,
		},
		{
			name: "retrieves user with home",
			setup: func() string {
				cleanDatabase(t)
				user, err := repo.Create(ctx, newTestUserWithHome("ext-get-home", "gethome@example.com", "GetHome"))
				require.NoError(t, err)
				return user.ID
			},
			wantHome: true,
			wantErr:  nil,
		},
		{
			name: "empty ID returns error",
			setup: func() string {
				return ""
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "non-existent user returns not found",
			setup: func() string {
				cleanDatabase(t)
				return "00000000-0000-0000-0000-000000000000"
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := tt.setup()

			got, err := repo.Get(ctx, id)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, id, got.ID)

			if tt.wantHome {
				require.NotNil(t, got.Home)
				assert.Equal(t, "JP", got.Home.CountryCode)
				assert.Equal(t, "JP-13", got.Home.Level1)
			}
		})
	}
}

func TestUserRepository_GetByProviderUserID(t *testing.T) {
	repo := rdb.NewUserRepository(testDB)
	ctx := context.Background()

	tests := []struct {
		name           string
		setup          func() string // returns provider user ID
		providerUserID func() string
		wantErr        error
	}{
		{
			name: "retrieves user by provider user ID",
			setup: func() string {
				cleanDatabase(t)
				_, err := repo.Create(ctx, newTestUser("ext-by-ext-1", "byext@example.com", "ByExt"))
				require.NoError(t, err)
				return "ext-by-ext-1"
			},
			wantErr: nil,
		},
		{
			name: "empty provider user ID returns error",
			setup: func() string {
				return ""
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "non-existent provider user ID returns not found",
			setup: func() string {
				cleanDatabase(t)
				return "non-existent-ext-id"
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extID := tt.setup()

			got, err := repo.GetByProviderUserID(ctx, extID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, extID, got.ProviderUserID)
		})
	}
}

func TestUserRepository_Delete(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	repo := rdb.NewUserRepository(testDB)
	ctx := context.Background()

	t.Run("existing user", func(t *testing.T) {
		// @spec components/entity/user/delete "Existing user"
		cleanDatabase(t)
		user, err := repo.Create(ctx, newTestUser("ext-del-1", "del@example.com", "Delete"))
		require.NoError(t, err)
		artistID := seedArtist(t, "del-artist", entity.NewID())
		_, err = testDB.Pool.Exec(ctx, `INSERT INTO followed_artists (user_id, artist_id) VALUES ($1, $2)`, user.ID, artistID)
		require.NoError(t, err)
		_, err = rdb.NewIdentityVerificationRepository(testDB).Create(ctx, entity.NewIdentityVerification(user.ID, "ps-"+user.ID, entity.VerificationMethodJPKI))
		require.NoError(t, err)

		require.NoError(t, repo.Delete(ctx, user.ID))

		_, err = repo.Get(ctx, user.ID)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.False(t, rowExists(t, `SELECT 1 FROM followed_artists WHERE user_id = $1`, user.ID), "follow")
		assert.False(t, rowExists(t, `SELECT 1 FROM identity_verifications WHERE user_id = $1`, user.ID), "identity_verification")
	})

	t.Run("purchases outlive the user", func(t *testing.T) {
		// @spec components/entity/user/delete "Purchases outlive the user"
		cleanDatabase(t)
		user, err := repo.Create(ctx, newTestUser("ext-del-2", "del2@example.com", "Delete"))
		require.NoError(t, err)
		ticketID := seedIssuedTickets(t, seedAdmissionEvent(t), user.ID, 1)[0]

		require.NoError(t, repo.Delete(ctx, user.ID))

		assert.True(t, rowExists(t, `SELECT 1 FROM tickets WHERE id = $1`, string(ticketID)), "ticket")
		assert.True(t, rowExists(t, `SELECT 1 FROM orders o JOIN tickets t ON t.order_id = o.id WHERE t.id = $1`, string(ticketID)), "order")
		assert.True(t, rowExists(t, `SELECT 1 FROM lottery_entries WHERE user_id = $1`, user.ID), "lottery entry")
	})

	t.Run("unknown id", func(t *testing.T) {
		// @spec components/entity/user/delete "Unknown id"
		err := repo.Delete(ctx, "00000000-0000-0000-0000-000000000000")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("empty id", func(t *testing.T) {
		// @spec components/entity/user/delete "Empty id"
		err := repo.Delete(ctx, "")

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestUserRepository_UpdateHome(t *testing.T) {
	repo := rdb.NewUserRepository(testDB)
	ctx := context.Background()

	t.Run("creates home for user without one", func(t *testing.T) {
		cleanDatabase(t)
		user, err := repo.Create(ctx, newTestUser("ext-uh-1", "uh1@example.com", "UH1"))
		require.NoError(t, err)
		assert.Nil(t, user.Home)

		home := &entity.Home{
			CountryCode: "JP",
			Level1:      "JP-13",
		}

		updated, err := repo.UpdateHome(ctx, user.ID, home)

		require.NoError(t, err)
		require.NotNil(t, updated.Home)
		assert.NotEmpty(t, updated.Home.ID)
		assert.Equal(t, "JP", updated.Home.CountryCode)
		assert.Equal(t, "JP-13", updated.Home.Level1)
		assert.Nil(t, updated.Home.Level2)
	})

	t.Run("updates existing home", func(t *testing.T) {
		cleanDatabase(t)
		user, err := repo.Create(ctx, newTestUserWithHome("ext-uh-2", "uh2@example.com", "UH2"))
		require.NoError(t, err)
		require.NotNil(t, user.Home)
		assert.Equal(t, "JP-13", user.Home.Level1)

		home := &entity.Home{
			CountryCode: "JP",
			Level1:      "JP-40",
		}

		updated, err := repo.UpdateHome(ctx, user.ID, home)

		require.NoError(t, err)
		require.NotNil(t, updated.Home)
		assert.Equal(t, "JP", updated.Home.CountryCode)
		assert.Equal(t, "JP-40", updated.Home.Level1)
	})

	t.Run("empty ID returns error", func(t *testing.T) {
		home := &entity.Home{
			CountryCode: "JP",
			Level1:      "JP-13",
		}

		_, err := repo.UpdateHome(ctx, "", home)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestUserRepository_CentroidRoundTrip(t *testing.T) {
	repo := rdb.NewUserRepository(testDB)
	ctx := context.Background()

	t.Run("supported JP code resolves centroid on Create and persists through Get", func(t *testing.T) {
		cleanDatabase(t)
		user, err := repo.Create(ctx, newTestUserWithHome("ext-crt-1", "crt1@example.com", "CRT1"))
		require.NoError(t, err)
		require.NotNil(t, user.Home)
		require.NotNil(t, user.Home.Centroid, "Centroid should be resolved for JP-13")
		assert.InDelta(t, 35.6894, user.Home.Centroid.Latitude, 0.01)
		assert.InDelta(t, 139.6917, user.Home.Centroid.Longitude, 0.01)

		// Verify round-trip through Get
		got, err := repo.Get(ctx, user.ID)
		require.NoError(t, err)
		require.NotNil(t, got.Home)
		require.NotNil(t, got.Home.Centroid, "Centroid should persist through Get")
		assert.InDelta(t, 35.6894, got.Home.Centroid.Latitude, 0.01)
		assert.InDelta(t, 139.6917, got.Home.Centroid.Longitude, 0.01)
	})

	t.Run("supported JP code resolves centroid on UpdateHome", func(t *testing.T) {
		cleanDatabase(t)
		user, err := repo.Create(ctx, newTestUser("ext-crt-2", "crt2@example.com", "CRT2"))
		require.NoError(t, err)

		updated, err := repo.UpdateHome(ctx, user.ID, &entity.Home{
			CountryCode: "JP",
			Level1:      "JP-27", // Osaka
		})
		require.NoError(t, err)
		require.NotNil(t, updated.Home)
		require.NotNil(t, updated.Home.Centroid, "Centroid should be resolved for JP-27")
		assert.InDelta(t, 34.6863, updated.Home.Centroid.Latitude, 0.01)
		assert.InDelta(t, 135.5200, updated.Home.Centroid.Longitude, 0.01)
	})

	t.Run("unsupported country code results in nil Centroid", func(t *testing.T) {
		cleanDatabase(t)
		u := newTestUser("ext-crt-3", "crt3@example.com", "CRT3")
		u.Home = &entity.Home{
			CountryCode: "US",
			Level1:      "US-NY",
		}
		user, err := repo.Create(ctx, u)
		require.NoError(t, err)
		require.NotNil(t, user.Home)
		assert.Nil(t, user.Home.Centroid, "Centroid should be nil for unsupported country")

		// Verify nil Centroid persists through Get
		got, err := repo.Get(ctx, user.ID)
		require.NoError(t, err)
		require.NotNil(t, got.Home)
		assert.Nil(t, got.Home.Centroid, "Centroid should remain nil through Get")
	})
}

func TestUserRepository_PreferredLanguage(t *testing.T) {
	repo := rdb.NewUserRepository(testDB)
	ctx := context.Background()

	t.Run("Create persists preferred_language", func(t *testing.T) {
		cleanDatabase(t)

		params := newTestUser("ext-lang-1", "lang1@example.com", "Lang1")
		params.PreferredLanguage = "ja"

		user, err := repo.Create(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, "ja", user.PreferredLanguage)

		// Verify it round-trips through Get.
		got, err := repo.Get(ctx, user.ID)
		require.NoError(t, err)
		assert.Equal(t, "ja", got.PreferredLanguage)
	})

	t.Run("Create with empty preferred_language persists SQL NULL (not empty string)", func(t *testing.T) {
		cleanDatabase(t)

		params := newTestUser("ext-lang-2", "lang2@example.com", "Lang2")
		params.PreferredLanguage = "" // simulates an old client that omitted the wire field

		user, err := repo.Create(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, "", user.PreferredLanguage, "NULL column should map to empty string in domain")

		// Verify the column is SQL NULL, not the literal empty string —
		// the NULL='not-yet-set' invariant lets the hydration backfill
		// path differentiate old-client rows from "client explicitly chose ''".
		var lang sql.NullString
		err = testDB.Pool.QueryRow(ctx,
			`SELECT preferred_language FROM users WHERE id = $1`,
			user.ID,
		).Scan(&lang)
		require.NoError(t, err)
		assert.False(t, lang.Valid, "preferred_language must be SQL NULL, got %q (Valid=%t)", lang.String, lang.Valid)

		// Round-trip via Get also yields empty.
		got, err := repo.Get(ctx, user.ID)
		require.NoError(t, err)
		assert.Equal(t, "", got.PreferredLanguage)
	})

	t.Run("idempotent Create does NOT overwrite existing preferred_language", func(t *testing.T) {
		cleanDatabase(t)

		// First Create: persists "ja".
		first := newTestUser("ext-lang-3", "lang3@example.com", "Lang3")
		first.PreferredLanguage = "ja"
		created, err := repo.Create(ctx, first)
		require.NoError(t, err)
		require.Equal(t, "ja", created.PreferredLanguage)

		// Second Create: duplicate provider_user_id returns AlreadyExists at repo layer.
		// The use case handles idempotency; at the repo level we just confirm the
		// existing row's preferred_language is still "ja" after the conflict.
		second := newTestUser("ext-lang-3", "lang3@example.com", "Lang3")
		second.PreferredLanguage = "en"
		_, err = repo.Create(ctx, second)
		require.Error(t, err)
		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)

		// Verify the stored preferred_language was NOT changed to "en".
		got, err := repo.GetByProviderUserID(ctx, "ext-lang-3")
		require.NoError(t, err)
		assert.Equal(t, "ja", got.PreferredLanguage, "idempotent Create must not overwrite existing preferred_language")
	})

	t.Run("UpdatePreferredLanguage round-trips and persists", func(t *testing.T) {
		cleanDatabase(t)

		user, err := repo.Create(ctx, newTestUser("ext-lang-4", "lang4@example.com", "Lang4"))
		require.NoError(t, err)
		// newTestUser sets PreferredLanguage = "ja"; confirm initial value.
		assert.Equal(t, "ja", user.PreferredLanguage)

		// Update to "en".
		updated, err := repo.UpdatePreferredLanguage(ctx, user.ID, "en")
		require.NoError(t, err)
		assert.Equal(t, "en", updated.PreferredLanguage)

		// Verify persistence via independent Get.
		got, err := repo.Get(ctx, user.ID)
		require.NoError(t, err)
		assert.Equal(t, "en", got.PreferredLanguage)
	})

	t.Run("UpdatePreferredLanguage — non-existent user returns NotFound", func(t *testing.T) {
		cleanDatabase(t)

		_, err := repo.UpdatePreferredLanguage(ctx, "00000000-0000-0000-0000-000000000000", "en")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("UpdatePreferredLanguage — empty ID returns InvalidArgument", func(t *testing.T) {
		_, err := repo.UpdatePreferredLanguage(ctx, "", "en")

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("UpdatePreferredLanguage — empty language returns InvalidArgument", func(t *testing.T) {
		// The RPC contract requires a non-empty ISO 639-1 code. An empty
		// value at the repo boundary is a programmer error; we reject it
		// rather than write NULL (which would falsely re-arm the
		// hydration-backfill loop).
		cleanDatabase(t)
		user, err := repo.Create(ctx, newTestUser("ext-lang-5", "lang5@example.com", "Lang5"))
		require.NoError(t, err)

		_, err = repo.UpdatePreferredLanguage(ctx, user.ID, "")

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)

		// Confirm the stored language wasn't touched.
		got, err := repo.Get(ctx, user.ID)
		require.NoError(t, err)
		assert.Equal(t, "ja", got.PreferredLanguage)
	})
}

// TestUserRepository_ScanNULLColumns is the regression gate for the
// 2026-05-23 incident: scanUser scanned the nullable preferred_language,
// country, and time_zone columns directly into Go *string fields. With
// every row populated (the historical state under DEFAULT 'en' and
// before country / time_zone backfill stopped) the bug was invisible.
// The persist-user-language migration set every existing
// preferred_language to NULL on prod, and pgx then failed every Scan,
// which the use-case layer's idempotent-retry path mistranslated into
// AlreadyExists at the wire — masking the root cause for hours.
//
// This test inserts a row whose three nullable columns are NULL and
// asserts every code path that shares scanUser succeeds, returning the
// entity fields at zero value (empty string). It SHALL fail loudly if a
// future refactor reverts any column to a raw *string scan.
func TestUserRepository_ScanNULLColumns(t *testing.T) {
	repo := rdb.NewUserRepository(testDB)
	ctx := context.Background()
	cleanDatabase(t)

	// Insert a NewUser with empty-string PreferredLanguage / Country /
	// TimeZone. The repo's nullStringFromEmpty write boundary maps "" to
	// SQL NULL, so the row lands with NULL in all three columns —
	// equivalent to the legacy-row state after the persist-user-language
	// migration.
	const (
		providerUserID = "ext-null-scan"
		email          = "null-scan@example.com"
		name           = "NullScan"
	)
	created, err := repo.Create(ctx, &entity.NewUser{
		ProviderUserID:    providerUserID,
		Email:             email,
		Name:              name,
		PreferredLanguage: "",
		TimeZone:          "",
	})
	require.NoError(t, err)

	// Sanity: the in-memory entity returned by Create is built from
	// params, so it reflects empty strings rather than DB-side NULLs.
	// The subsequent SELECT-and-scan paths are what actually exercise the
	// regression — see asserts below.
	require.NotEmpty(t, created.ID)
	assert.Empty(t, created.PreferredLanguage)
	assert.Empty(t, created.TimeZone)

	assertEmptyNullableFields := func(t *testing.T, u *entity.User) {
		t.Helper()
		assert.Empty(t, u.PreferredLanguage, "preferred_language NULL must scan to empty string")
		assert.Empty(t, u.TimeZone, "time_zone NULL must scan to empty string")
	}

	t.Run("Get scans NULL columns without error", func(t *testing.T) {
		got, err := repo.Get(ctx, created.ID)
		require.NoError(t, err)
		assertEmptyNullableFields(t, got)
	})

	t.Run("GetByProviderUserID scans NULL columns without error", func(t *testing.T) {
		got, err := repo.GetByProviderUserID(ctx, providerUserID)
		require.NoError(t, err)
		assertEmptyNullableFields(t, got)
	})

	t.Run("UpdatePreferredLanguage backfills NULL → 'ja' and round-trips", func(t *testing.T) {
		// Mirrors the hydration-backfill path the frontend takes when it
		// observes user.preferredLanguage absent.
		updated, err := repo.UpdatePreferredLanguage(ctx, created.ID, "ja")
		require.NoError(t, err)
		assert.Equal(t, "ja", updated.PreferredLanguage)
		// country / time_zone remain NULL — scan must still succeed.
		assert.Empty(t, updated.TimeZone)
	})

	t.Run("UpdateHome re-fetch handles row with country/time_zone NULL", func(t *testing.T) {
		// UpdateHome runs an UPDATE then re-fetches via scanUser. The
		// re-fetch is the load-bearing scan — same code path as Get.
		updated, err := repo.UpdateHome(ctx, created.ID, &entity.Home{
			CountryCode: "JP",
			Level1:      "JP-13",
		})
		require.NoError(t, err)
		require.NotNil(t, updated.Home)
		assert.Equal(t, "JP-13", updated.Home.Level1)
		// users.country / users.time_zone are still NULL (not affected by
		// UpdateHome). The re-fetch scan must not crash.
		assert.Empty(t, updated.TimeZone)
	})
}

func TestUserRepository_UpdatePersonalDetails(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	repo := rdb.NewUserRepository(testDB)
	ctx := context.Background()

	t.Run("first details", func(t *testing.T) {
		// @spec components/entity/user/update-personal-details "First details"
		id := seedUser(t, "details", entity.NewID()+"@example.com", entity.NewID())
		before, err := repo.Get(ctx, id)
		require.NoError(t, err)
		require.Empty(t, before.FullName)

		updated, err := repo.UpdatePersonalDetails(ctx, id, "山田 花子", "+819012345678")

		require.NoError(t, err)
		assert.Equal(t, "山田 花子", updated.FullName)
		assert.Equal(t, "+819012345678", updated.PhoneNumber)
		assert.Equal(t, before.Email, updated.Email, "other attributes are unchanged")
		assert.Equal(t, before.Name, updated.Name)
		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, updated, got)

		again, err := repo.UpdatePersonalDetails(ctx, id, "山田 花子", "+819012345678")
		require.NoError(t, err, "repeating the same values succeeds")
		assert.Equal(t, updated, again)
	})

	t.Run("correction", func(t *testing.T) {
		// @spec components/entity/user/update-personal-details "Correction"
		id := seedUser(t, "details-fix", entity.NewID()+"@example.com", entity.NewID())
		_, err := repo.UpdatePersonalDetails(ctx, id, "山田 花子", "+819012345678")
		require.NoError(t, err)
		eventID := seedAdmissionEvent(t)
		seedIssuedTickets(t, eventID, id, 2)

		_, err = repo.UpdatePersonalDetails(ctx, id, "山田 花子", "+818098765432")

		require.NoError(t, err)
		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, "+818098765432", got.PhoneNumber)
		// The ticket face reads the User's current values: the tickets are the
		// User's, and no copy of the old phone number is kept on them.
		tickets, err := rdb.NewTicketRepository(testDB).ListByUserAndEvent(ctx, entity.UserID(id), eventID)
		require.NoError(t, err)
		require.Len(t, tickets, 2)
		for _, tk := range tickets {
			assert.Equal(t, entity.UserID(id), tk.UserID)
			assert.Equal(t, entity.TicketStatusIssued, tk.Status)
		}
	})

	t.Run("invalid phone number", func(t *testing.T) {
		// @spec components/entity/user/update-personal-details "Invalid phone number"
		id := seedUser(t, "details-bad", entity.NewID()+"@example.com", entity.NewID())
		_, err := repo.UpdatePersonalDetails(ctx, id, "山田 花子", "+819012345678")
		require.NoError(t, err)

		_, err = repo.UpdatePersonalDetails(ctx, id, "山田 花子", "090-1234-5678")

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, "+819012345678", got.PhoneNumber, "nothing changes")
	})

	t.Run("unknown user", func(t *testing.T) {
		// @spec components/entity/user/update-personal-details "Unknown user"
		_, err := repo.UpdatePersonalDetails(ctx, entity.NewID(), "山田 花子", "+819012345678")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("an empty value is stored as absent", func(t *testing.T) {
		id := seedUser(t, "details-clear", entity.NewID()+"@example.com", entity.NewID())
		_, err := repo.UpdatePersonalDetails(ctx, id, "山田 花子", "+819012345678")
		require.NoError(t, err)

		updated, err := repo.UpdatePersonalDetails(ctx, id, "", "+819012345678")

		require.NoError(t, err)
		assert.Empty(t, updated.FullName)
		var fullNameIsNull bool
		require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT full_name IS NULL FROM users WHERE id = $1`, id).Scan(&fullNameIsNull))
		assert.True(t, fullNameIsNull)
	})
}
