package rdb_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
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
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewUserRepository(db, testOutbox)
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
			setup: func() { cleanTables(t, db) },
			args: args{
				params: newTestUser("ext-001", "alice@example.com", "Alice"),
			},
			wantErr: nil,
		},
		{
			name:  "creates a user with home",
			setup: func() { cleanTables(t, db) },
			args: args{
				params: newTestUserWithHome("ext-home-1", "withhome@example.com", "WithHome"),
			},
			wantErr: nil,
		},
		{
			name:  "nil params returns error",
			setup: func() { cleanTables(t, db) },
			args: args{
				params: nil,
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "duplicate email returns already exists",
			setup: func() {
				cleanTables(t, db)
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
				assert.Equal(t, tt.args.params.Home.CountryCode, got.Home.CountryCode)
				assert.Equal(t, tt.args.params.Home.Level1, got.Home.Level1)
			} else {
				assert.Nil(t, got.Home)
			}
		})
	}
}

// TestUserRepository_Create_TakenIdentityOrEmail covers the rule that one
// provider user ID and one email address, letter case ignored, belong to at
// most one User.
func TestUserRepository_Create_TakenIdentityOrEmail(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewUserRepository(db, testOutbox)
	ctx := context.Background()

	countUsers := func(t *testing.T) int {
		t.Helper()
		var n int
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n))
		return n
	}

	tests := []struct {
		name   string
		stored *entity.NewUser
		params *entity.NewUser
	}{
		{
			// @spec components/entity/user/create "Same provider user ID"
			name:   "same provider user ID",
			stored: newTestUser("ext-taken-1", "first@example.com", "First"),
			params: newTestUser("ext-taken-1", "second@example.com", "Second"),
		},
		{
			// @spec components/entity/user/create "Same email"
			name:   "same email",
			stored: newTestUser("ext-taken-2", "fan@example.com", "First"),
			params: newTestUser("ext-taken-3", "fan@example.com", "Second"),
		},
		{
			// @spec components/entity/user/create "Same email in another letter case"
			name:   "same email in another letter case",
			stored: newTestUser("ext-taken-4", "fan@example.com", "First"),
			params: newTestUser("ext-taken-5", "Fan@Example.com", "Second"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanTables(t, db)
			first, err := repo.Create(ctx, tt.stored)
			require.NoError(t, err)

			_, err = repo.Create(ctx, tt.params)

			assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
			assert.Equal(t, 1, countUsers(t), "the rejected user is not stored")
			kept, err := repo.Get(ctx, first.ID)
			require.NoError(t, err)
			assert.Equal(t, tt.stored.Email, kept.Email)
		})
	}

	// @spec components/entity/user/create "Letter case kept"
	t.Run("the email is stored in its own letter case", func(t *testing.T) {
		cleanTables(t, db)

		created, err := repo.Create(ctx, newTestUser("ext-taken-6", "Fan@Example.com", "Fan"))
		require.NoError(t, err)

		stored, err := repo.Get(ctx, created.ID)
		require.NoError(t, err)
		assert.Equal(t, "Fan@Example.com", stored.Email)
	})
}

func TestUserRepository_Get(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewUserRepository(db, testOutbox)
	ctx := context.Background()

	tests := []struct {
		name     string
		setup    func() string // returns user ID
		wantHome bool
		wantErr  error
	}{
		{
			// @spec components/entity/user/get "User without a home"
			name: "retrieves existing user",
			setup: func() string {
				cleanTables(t, db)
				user, err := repo.Create(ctx, newTestUser("ext-get-1", "get@example.com", "GetUser"))
				require.NoError(t, err)
				return user.ID
			},
			wantErr: nil,
		},
		{
			name: "retrieves user with home",
			setup: func() string {
				cleanTables(t, db)
				user, err := repo.Create(ctx, newTestUserWithHome("ext-get-home", "gethome@example.com", "GetHome"))
				require.NoError(t, err)
				return user.ID
			},
			wantHome: true,
			wantErr:  nil,
		},
		{
			// @spec components/entity/user/get "Empty id"
			name: "empty ID returns error",
			setup: func() string {
				return ""
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			// @spec components/entity/user/get "Unknown id"
			name: "non-existent user returns not found",
			setup: func() string {
				cleanTables(t, db)
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
			} else {
				assert.Nil(t, got.Home)
			}
		})
	}
}

func TestUserRepository_GetByProviderUserID(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewUserRepository(db, testOutbox)
	ctx := context.Background()

	tests := []struct {
		name    string
		setup   func() string // returns provider user ID
		wantErr error
		// wantHome is the Home of the User found, nil for a User without one.
		wantHome func() *entity.Home
	}{
		{
			// @spec components/entity/user/get-by-provider-user-id "Registered identity"
			name: "retrieves user by provider user ID",
			setup: func() string {
				cleanTables(t, db)
				_, err := repo.Create(ctx, newTestUser("ext-by-ext-1", "byext@example.com", "ByExt"))
				require.NoError(t, err)
				_, err = repo.Create(ctx, newTestUserWithHome("ext-by-ext-2", "byext2@example.com", "ByExt2"))
				require.NoError(t, err)
				return "ext-by-ext-1"
			},
		},
		{
			// @spec components/entity/user/get-by-provider-user-id "Registered identity"
			name: "retrieves user with its home by provider user ID",
			setup: func() string {
				cleanTables(t, db)
				_, err := repo.Create(ctx, newTestUser("ext-by-ext-3", "byext3@example.com", "ByExt3"))
				require.NoError(t, err)
				_, err = repo.Create(ctx, newTestUserWithHome("ext-by-ext-4", "byext4@example.com", "ByExt4"))
				require.NoError(t, err)
				return "ext-by-ext-4"
			},
			wantHome: func() *entity.Home {
				return &entity.Home{CountryCode: "JP", Level1: "JP-13", ReferencePoint: areaPoint(t, db, "JP-13")}
			},
		},
		{
			// @spec components/entity/user/get-by-provider-user-id "Empty provider user ID"
			name: "empty provider user ID returns error",
			setup: func() string {
				return ""
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			// @spec components/entity/user/get-by-provider-user-id "Unregistered identity"
			name: "non-existent provider user ID returns not found",
			setup: func() string {
				cleanTables(t, db)
				_, err := repo.Create(ctx, newTestUser("ext-by-ext-5", "byext5@example.com", "ByExt5"))
				require.NoError(t, err)
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
			if tt.wantHome == nil {
				assert.Nil(t, got.Home)
				return
			}
			assert.Equal(t, tt.wantHome(), got.Home)
		})
	}
}

func TestUserRepository_GetByEmail(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewUserRepository(db, testOutbox)
	ctx := context.Background()

	// @spec components/entity/user/get-by-email "Registered address"
	t.Run("returns the user whose email is the address", func(t *testing.T) {
		cleanTables(t, db)
		created, err := repo.Create(ctx, newTestUser("ext-by-email-1", "fan@example.com", "Fan"))
		require.NoError(t, err)

		got, err := repo.GetByEmail(ctx, "fan@example.com")

		require.NoError(t, err)
		assert.Equal(t, created.ID, got.ID)
		assert.Equal(t, "fan@example.com", got.Email)
	})

	// @spec components/entity/user/get-by-email "Different letter case"
	t.Run("returns the user when the address differs only in letter case", func(t *testing.T) {
		cleanTables(t, db)
		created, err := repo.Create(ctx, newTestUser("ext-by-email-2", "fan@example.com", "Fan"))
		require.NoError(t, err)

		got, err := repo.GetByEmail(ctx, "Fan@Example.com")

		require.NoError(t, err)
		assert.Equal(t, created.ID, got.ID)
		assert.Equal(t, "fan@example.com", got.Email, "the stored address, not the typed one")
	})

	// @spec components/entity/user/get-by-email "Unregistered address"
	t.Run("fails with NotFound when no user has the address", func(t *testing.T) {
		cleanTables(t, db)
		_, err := repo.Create(ctx, newTestUser("ext-by-email-3", "fan@example.com", "Fan"))
		require.NoError(t, err)

		_, err = repo.GetByEmail(ctx, "nobody@example.com")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	// @spec components/entity/user/get-by-email "Empty address"
	t.Run("fails with InvalidArgument for an empty address", func(t *testing.T) {
		_, err := repo.GetByEmail(ctx, "")

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestUserRepository_Delete(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewUserRepository(db, testOutbox)
	ctx := context.Background()

	t.Run("existing user", func(t *testing.T) {
		// @spec components/entity/user/delete "Existing user"
		cleanTables(t, db)
		user, err := repo.Create(ctx, newTestUser("ext-del-1", "del@example.com", "Delete"))
		require.NoError(t, err)
		artistID := seedArtist(t, db, "del-artist", entity.NewID())
		_, err = db.Pool.Exec(ctx, `INSERT INTO followed_artists (user_id, artist_id) VALUES ($1, $2)`, user.ID, artistID)
		require.NoError(t, err)
		_, err = rdb.NewIdentityVerificationRepository(db).Create(ctx, entity.NewIdentityVerification(user.ID, "ps-"+user.ID, entity.VerificationMethodJPKI, time.Now()))
		require.NoError(t, err)

		require.NoError(t, repo.Delete(ctx, user.ID))

		_, err = repo.Get(ctx, user.ID)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.False(t, rowExists(t, db, `SELECT 1 FROM followed_artists WHERE user_id = $1`, user.ID), "follow")
		assert.False(t, rowExists(t, db, `SELECT 1 FROM identity_verifications WHERE user_id = $1`, user.ID), "identity_verification")
	})

	t.Run("home goes with the user", func(t *testing.T) {
		// @spec components/entity/user/delete "Home goes with the user"
		cleanTables(t, db)
		deleted, err := repo.Create(ctx, newTestUser("ext-del-h1", "delh1@example.com", "DelH1"))
		require.NoError(t, err)
		other, err := repo.Create(ctx, newTestUser("ext-del-h2", "delh2@example.com", "DelH2"))
		require.NoError(t, err)
		for _, id := range []string{deleted.ID, other.ID} {
			_, err = repo.UpdateHome(ctx, id, &entity.Home{CountryCode: "JP", Level1: "JP-40"})
			require.NoError(t, err)
		}

		require.NoError(t, repo.Delete(ctx, deleted.ID))

		assert.False(t, rowExists(t, db, `SELECT 1 FROM user_home_areas WHERE user_id = $1`, deleted.ID))
		got, err := repo.Get(ctx, other.ID)
		require.NoError(t, err)
		assert.Equal(t, "JP-40", got.Home.Level1)
	})

	t.Run("purchases outlive the user", func(t *testing.T) {
		// @spec components/entity/user/delete "Purchases outlive the user"
		cleanTables(t, db)
		user, err := repo.Create(ctx, newTestUser("ext-del-2", "del2@example.com", "Delete"))
		require.NoError(t, err)
		ticketID := seedIssuedTickets(t, db, seedAdmissionEvent(t, db), user.ID, 1)[0]

		require.NoError(t, repo.Delete(ctx, user.ID))

		assert.True(t, rowExists(t, db, `SELECT 1 FROM tickets WHERE id = $1`, string(ticketID)), "ticket")
		assert.True(t, rowExists(t, db, `SELECT 1 FROM orders o JOIN tickets t ON t.order_id = o.id WHERE t.id = $1`, string(ticketID)), "order")
		assert.True(t, rowExists(t, db, `SELECT 1 FROM lottery_entries le JOIN orders o ON o.id = le.order_id WHERE o.user_id = $1`, user.ID), "lottery entry")
	})

	t.Run("reminder records go with the user", func(t *testing.T) {
		// @spec components/entity/user/delete "Reminder records go with the user"
		cleanTables(t, db)
		user, err := repo.Create(ctx, newTestUser("ext-del-3", "del3@example.com", "Delete"))
		require.NoError(t, err)
		saleID := seedDiscoveredSale(t, db)
		require.NoError(t, rdb.NewTicketSaleReminderRepository(db).RecordSent(ctx, user.ID, saleID, entity.ReminderStageApplyOpen))

		require.NoError(t, repo.Delete(ctx, user.ID))

		assert.False(t, rowExists(t, db, `SELECT 1 FROM ticket_sale_reminders WHERE user_id = $1`, user.ID), "ticket sale reminder")
		assert.True(t, rowExists(t, db, `SELECT 1 FROM ticket_sales WHERE id = $1`, string(saleID)), "the sale stays")
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

// areaPoint reads the reference point of an Area from the seeded reference
// data.
func areaPoint(t *testing.T, db *rdb.Database, code string) *entity.Coordinates {
	t.Helper()
	var lat, lng float64
	require.NoError(t, db.Pool.QueryRow(context.Background(),
		`SELECT reference_latitude, reference_longitude FROM areas WHERE code = $1`, code).Scan(&lat, &lng))
	return &entity.Coordinates{Latitude: lat, Longitude: lng}
}

func TestUserRepository_CreateHome(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewUserRepository(db, testOutbox)
	ctx := context.Background()

	t.Run("Japanese prefecture", func(t *testing.T) {
		// @spec components/entity/user/create "Japanese prefecture"
		user, err := repo.Create(ctx, newTestUserWithHome("ext-ch-1", "ch1@example.com", "CH1"))
		require.NoError(t, err)
		assert.Equal(t, &entity.Home{CountryCode: "JP", Level1: "JP-13", ReferencePoint: areaPoint(t, db, "JP-13")}, user.Home)

		// @spec components/entity/user/get "User with a home"
		got, err := repo.Get(ctx, user.ID)
		require.NoError(t, err)
		assert.Equal(t, user.Home, got.Home)
	})
	t.Run("Area without a reference point", func(t *testing.T) {
		// @spec components/entity/user/create "Area without a reference point"
		_, err := db.Pool.Exec(ctx, `INSERT INTO areas (code, kind, country_code, name_en) VALUES ('XA', 'country', 'XA', 'Nowhere')`)
		require.NoError(t, err)
		params := newTestUser("ext-ch-2", "ch2@example.com", "CH2")
		params.Home = &entity.Home{CountryCode: "XA", Level1: "XA"}

		user, err := repo.Create(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, &entity.Home{CountryCode: "XA", Level1: "XA"}, user.Home)
	})
	t.Run("Level 1 that is no first-level Area", func(t *testing.T) {
		// @spec components/entity/user/create "Level 1 that is no first-level Area"
		params := newTestUser("ext-ch-3", "ch3@example.com", "CH3")
		params.Home = &entity.Home{CountryCode: "FR", Level1: "FR-33"}

		_, err := repo.Create(ctx, params)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		_, err = repo.GetByProviderUserID(ctx, "ext-ch-3")
		assert.ErrorIs(t, err, apperr.ErrNotFound, "no User is stored")
	})
}

func TestUserRepository_UpdateHome(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewUserRepository(db, testOutbox)
	ctx := context.Background()

	t.Run("First home", func(t *testing.T) {
		// @spec components/entity/user/update-home "First home"
		user, err := repo.Create(ctx, newTestUser("ext-uh-1", "uh1@example.com", "UH1"))
		require.NoError(t, err)
		assert.Nil(t, user.Home)

		updated, err := repo.UpdateHome(ctx, user.ID, &entity.Home{CountryCode: "JP", Level1: "JP-13"})

		require.NoError(t, err)
		assert.Equal(t, &entity.Home{CountryCode: "JP", Level1: "JP-13", ReferencePoint: areaPoint(t, db, "JP-13")}, updated.Home)
	})
	t.Run("Changing the home", func(t *testing.T) {
		// @spec components/entity/user/update-home "Changing the home"
		user, err := repo.Create(ctx, newTestUserWithHome("ext-uh-2", "uh2@example.com", "UH2"))
		require.NoError(t, err)

		updated, err := repo.UpdateHome(ctx, user.ID, &entity.Home{CountryCode: "KR", Level1: "KR-11"})

		require.NoError(t, err)
		assert.Equal(t, &entity.Home{CountryCode: "KR", Level1: "KR-11", ReferencePoint: areaPoint(t, db, "KR-11")}, updated.Home)
		var homes int
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM user_home_areas WHERE user_id = $1`, user.ID).Scan(&homes))
		assert.Equal(t, 1, homes, "the User has no other Home")
	})
	t.Run("Level 1 that is no first-level Area", func(t *testing.T) {
		// @spec components/entity/user/update-home "Level 1 that is no first-level Area"
		user, err := repo.Create(ctx, newTestUserWithHome("ext-uh-3", "uh3@example.com", "UH3"))
		require.NoError(t, err)

		_, err = repo.UpdateHome(ctx, user.ID, &entity.Home{CountryCode: "JP", Level1: "JP-99"})

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		got, err := repo.Get(ctx, user.ID)
		require.NoError(t, err)
		assert.Equal(t, "JP-13", got.Home.Level1)
	})
	t.Run("Area's reference point changed", func(t *testing.T) {
		// @spec components/entity/user "Area's reference point changed"
		user, err := repo.Create(ctx, newTestUser("ext-uh-4", "uh4@example.com", "UH4"))
		require.NoError(t, err)
		_, err = repo.UpdateHome(ctx, user.ID, &entity.Home{CountryCode: "KR", Level1: "KR-11"})
		require.NoError(t, err)
		_, err = db.Pool.Exec(ctx, `UPDATE areas SET reference_latitude = 37.5, reference_longitude = 127.0 WHERE code = 'KR-11'`)
		require.NoError(t, err)

		got, err := repo.Get(ctx, user.ID)
		require.NoError(t, err)
		assert.Equal(t, &entity.Coordinates{Latitude: 37.5, Longitude: 127.0}, got.Home.ReferencePoint)
	})
	t.Run("unknown user", func(t *testing.T) {
		_, err := repo.UpdateHome(ctx, entity.NewID(), &entity.Home{CountryCode: "JP", Level1: "JP-13"})
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
	t.Run("empty ID returns error", func(t *testing.T) {
		_, err := repo.UpdateHome(ctx, "", &entity.Home{CountryCode: "JP", Level1: "JP-13"})
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestUserRepository_PreferredLanguage(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewUserRepository(db, testOutbox)
	ctx := context.Background()

	t.Run("Create persists preferred_language", func(t *testing.T) {
		cleanTables(t, db)

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

	t.Run("Create with empty preferred_language returns InvalidArgument and stores nothing", func(t *testing.T) {
		cleanTables(t, db)

		params := newTestUser("ext-lang-2", "lang2@example.com", "Lang2")
		params.PreferredLanguage = ""

		_, err := repo.Create(ctx, params)
		assertAppErrCode(t, err, codes.InvalidArgument)

		_, err = repo.GetByProviderUserID(ctx, "ext-lang-2")
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("idempotent Create does NOT overwrite existing preferred_language", func(t *testing.T) {
		cleanTables(t, db)

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
		cleanTables(t, db)

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
		cleanTables(t, db)

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
		cleanTables(t, db)
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
// 2026-05-23 incident: scanUser scanned nullable columns directly into Go
// *string fields, so pgx failed every Scan of a row holding NULL, which the
// use-case layer's idempotent-retry path mistranslated into AlreadyExists at
// the wire.
//
// This test inserts a row whose nullable time_zone is NULL and asserts every
// code path that shares scanUser succeeds, returning the entity field at zero
// value (empty string). It SHALL fail loudly if a future refactor reverts the
// column to a raw *string scan.
func TestUserRepository_ScanNULLColumns(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewUserRepository(db, testOutbox)
	ctx := context.Background()

	// The repo's nullStringFromEmpty write boundary maps an empty TimeZone to
	// SQL NULL.
	const (
		providerUserID = "ext-null-scan"
		email          = "null-scan@example.com"
		name           = "NullScan"
	)
	created, err := repo.Create(ctx, &entity.NewUser{
		ProviderUserID:    providerUserID,
		Email:             email,
		Name:              name,
		PreferredLanguage: "ja",
		TimeZone:          "",
	})
	require.NoError(t, err)

	// Sanity: the in-memory entity returned by Create is built from
	// params, so it reflects an empty string rather than the DB-side NULL.
	// The subsequent SELECT-and-scan paths are what actually exercise the
	// regression — see asserts below.
	require.NotEmpty(t, created.ID)
	assert.Empty(t, created.TimeZone)

	var stored sql.NullString
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT time_zone FROM users WHERE id = $1`, created.ID).Scan(&stored))
	require.False(t, stored.Valid, "time_zone must be SQL NULL, got %q", stored.String)

	assertEmptyNullableFields := func(t *testing.T, u *entity.User) {
		t.Helper()
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

	t.Run("UpdatePreferredLanguage re-fetch handles row with time_zone NULL", func(t *testing.T) {
		updated, err := repo.UpdatePreferredLanguage(ctx, created.ID, "en")
		require.NoError(t, err)
		assert.Equal(t, "en", updated.PreferredLanguage)
		assertEmptyNullableFields(t, updated)
	})

	t.Run("UpdateHome re-fetch handles row with time_zone NULL", func(t *testing.T) {
		// UpdateHome runs an UPDATE then re-fetches via scanUser. The
		// re-fetch is the load-bearing scan — same code path as Get.
		updated, err := repo.UpdateHome(ctx, created.ID, &entity.Home{
			CountryCode: "JP",
			Level1:      "JP-13",
		})
		require.NoError(t, err)
		require.NotNil(t, updated.Home)
		assert.Equal(t, "JP-13", updated.Home.Level1)
		// users.time_zone is still NULL (not affected by UpdateHome). The
		// re-fetch scan must not crash.
		assertEmptyNullableFields(t, updated)
		assert.Empty(t, updated.TimeZone)
	})
}

func TestUserRepository_UpdatePersonalDetails(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	repo := rdb.NewUserRepository(db, testOutbox)
	ctx := context.Background()

	t.Run("first details", func(t *testing.T) {
		// @spec components/entity/user/update-personal-details "First details"
		id := seedUser(t, db, "details", entity.NewID()+"@example.com", entity.NewID())
		before, err := repo.Get(ctx, id)
		require.NoError(t, err)
		require.Nil(t, before.FullName)

		updated, err := repo.UpdatePersonalDetails(ctx, id, new("山田 花子"), new("+819012345678"))

		require.NoError(t, err)
		assert.Equal(t, new("山田 花子"), updated.FullName)
		assert.Equal(t, new("+819012345678"), updated.PhoneNumber)
		assert.Equal(t, before.Email, updated.Email, "other attributes are unchanged")
		assert.Equal(t, before.Name, updated.Name)
		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, updated, got)

		again, err := repo.UpdatePersonalDetails(ctx, id, new("山田 花子"), new("+819012345678"))
		require.NoError(t, err, "repeating the same values succeeds")
		assert.Equal(t, updated, again)
	})

	t.Run("correction", func(t *testing.T) {
		// @spec components/entity/user/update-personal-details "Correction"
		id := seedUser(t, db, "details-fix", entity.NewID()+"@example.com", entity.NewID())
		_, err := repo.UpdatePersonalDetails(ctx, id, new("山田 花子"), new("+819012345678"))
		require.NoError(t, err)
		eventID := seedAdmissionEvent(t, db)
		seedIssuedTickets(t, db, eventID, id, 2)

		_, err = repo.UpdatePersonalDetails(ctx, id, new("山田 花子"), new("+818098765432"))

		require.NoError(t, err)
		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, new("+818098765432"), got.PhoneNumber)
		// The ticket face reads the User's current values: the tickets are the
		// User's, and no copy of the old phone number is kept on them.
		tickets, err := rdb.NewTicketRepository(db, testOutbox).ListByUserAndEvent(ctx, entity.UserID(id), eventID)
		require.NoError(t, err)
		require.Len(t, tickets, 2)
		for _, tk := range tickets {
			assert.Equal(t, entity.UserID(id), tk.UserID)
			assert.Equal(t, entity.TicketStatusIssued, tk.Status)
		}
	})

	t.Run("invalid phone number", func(t *testing.T) {
		// @spec components/entity/user/update-personal-details "Invalid phone number"
		id := seedUser(t, db, "details-bad", entity.NewID()+"@example.com", entity.NewID())
		_, err := repo.UpdatePersonalDetails(ctx, id, new("山田 花子"), new("+819012345678"))
		require.NoError(t, err)

		_, err = repo.UpdatePersonalDetails(ctx, id, new("山田 花子"), new("090-1234-5678"))

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		got, err := repo.Get(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, new("+819012345678"), got.PhoneNumber, "nothing changes")
	})

	t.Run("unknown user", func(t *testing.T) {
		// @spec components/entity/user/update-personal-details "Unknown user"
		_, err := repo.UpdatePersonalDetails(ctx, entity.NewID(), new("山田 花子"), new("+819012345678"))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("a value not given is stored as absent", func(t *testing.T) {
		id := seedUser(t, db, "details-clear", entity.NewID()+"@example.com", entity.NewID())
		_, err := repo.UpdatePersonalDetails(ctx, id, new("山田 花子"), new("+819012345678"))
		require.NoError(t, err)

		updated, err := repo.UpdatePersonalDetails(ctx, id, nil, new("+819012345678"))

		require.NoError(t, err)
		assert.Nil(t, updated.FullName)
		var fullNameIsNull bool
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT full_name IS NULL FROM users WHERE id = $1`, id).Scan(&fullNameIsNull))
		assert.True(t, fullNameIsNull)
	})
}
