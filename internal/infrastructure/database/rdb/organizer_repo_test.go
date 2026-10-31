package rdb_test

import (
	"context"
	"strings"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

// uniqueShortName returns a valid short name no other test uses.
func uniqueShortName() string {
	id := strings.ReplaceAll(uuid.NewV7().String(), "-", "")
	return "t" + id[len(id)-20:]
}

// uniqueEmail returns an owner email no other test uses.
func uniqueEmail() string {
	return "owner-" + uuid.NewV7().String() + "@example.com"
}

func TestOrganizerRepository_Create(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	repo := rdb.NewOrganizerRepository(testDB, testOutbox)
	ctx := context.Background()

	t.Run("stores a new provisioning Organizer with its short name", func(t *testing.T) {
		// @spec components/entity/organizer/create "Organizer is stored"
		o := entity.NewOrganizer("XX Records", uniqueShortName(), uniqueEmail())

		created, err := repo.Create(ctx, o)

		require.NoError(t, err)
		assert.Equal(t, o.ID, created.ID)
		assert.Equal(t, "XX Records", created.Name)
		assert.Equal(t, o.ShortName, created.ShortName)
		assert.Equal(t, o.OwnerEmail, created.OwnerEmail)
		assert.Equal(t, entity.OrganizerStatusProvisioning, created.Status)
		assert.Empty(t, created.ProviderOrganizerID)
		got, err := repo.Get(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, created, got, "the returned Organizer is the stored one")
	})

	t.Run("gives an Organizer without an id a new one", func(t *testing.T) {
		o := entity.NewOrganizer("No Id Label", uniqueShortName(), uniqueEmail())
		o.ID = ""

		created, err := repo.Create(ctx, o)

		require.NoError(t, err)
		assert.NotEmpty(t, created.ID)
	})

	t.Run("stores a second Organizer with the same name and owner email", func(t *testing.T) {
		// @spec components/entity/organizer/create "Same name and owner email twice"
		email := uniqueEmail()
		first, err := repo.Create(ctx, entity.NewOrganizer("Twin Label", uniqueShortName(), email))
		require.NoError(t, err)

		second, err := repo.Create(ctx, entity.NewOrganizer("Twin Label", uniqueShortName(), email))

		require.NoError(t, err)
		assert.NotEqual(t, first.ID, second.ID)
	})

	t.Run("fails with AlreadyExists for the short name of a deactivated Organizer", func(t *testing.T) {
		// @spec components/entity/organizer/create "Short name already used"
		shortName := uniqueShortName()
		existing, err := repo.Create(ctx, entity.NewOrganizer("Old Label", shortName, uniqueEmail()))
		require.NoError(t, err)
		require.NoError(t, repo.SetStatus(ctx, existing.ID, entity.OrganizerStatusDeactivated))
		dup := entity.NewOrganizer("New Label", shortName, uniqueEmail())

		_, err = repo.Create(ctx, dup)

		assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
		_, err = repo.Get(ctx, dup.ID)
		assert.ErrorIs(t, err, apperr.ErrNotFound, "nothing is stored")
	})

	t.Run("refuses a malformed short name", func(t *testing.T) {
		_, err := repo.Create(ctx, entity.NewOrganizer("Bad Label", "XX Records", uniqueEmail()))

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("refuses an owner email over 165 characters", func(t *testing.T) {
		email := strings.Repeat("a", 166-len("@example.com")) + "@example.com"

		_, err := repo.Create(ctx, entity.NewOrganizer("Long Label", uniqueShortName(), email))

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestOrganizerRepository_ListByOwnerEmail(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	repo := rdb.NewOrganizerRepository(testDB, testOutbox)
	ctx := context.Background()

	// create stores an Organizer with the owner email and moves it to status.
	create := func(t *testing.T, name, email string, status entity.OrganizerStatus) *entity.Organizer {
		t.Helper()
		o, err := repo.Create(ctx, entity.NewOrganizer(name, uniqueShortName(), email))
		require.NoError(t, err)
		require.NoError(t, repo.SetProviderOrganizerID(ctx, o.ID, "zitadel-"+o.ID))
		if status != entity.OrganizerStatusProvisioning {
			require.NoError(t, repo.SetStatus(ctx, o.ID, status))
		}
		got, err := repo.Get(ctx, o.ID)
		require.NoError(t, err)
		return got
	}

	t.Run("returns both active Organizers of an owner, ordered by name, ignoring case", func(t *testing.T) {
		// @spec components/entity/organizer/list-by-owner-email "Owner of two Organizers"
		local := "band-" + uuid.NewV7().String()
		email := local + "@example.com"
		yy := create(t, "YY Promotion", email, entity.OrganizerStatusActive)
		xx := create(t, "XX Records", email, entity.OrganizerStatusActive)
		create(t, "ZZ Pending", email, entity.OrganizerStatusProvisioning)

		got, err := repo.ListByOwnerEmail(ctx, strings.ToUpper(local[:1])+local[1:]+"@Example.com")

		require.NoError(t, err)
		assert.Equal(t, []*entity.Organizer{xx, yy}, got)
		assert.NotEmpty(t, got[0].ProviderOrganizerID)
	})

	t.Run("returns an empty list when the only Organizer is deactivated", func(t *testing.T) {
		// @spec components/entity/organizer/list-by-owner-email "Deactivated Organizer"
		email := uniqueEmail()
		create(t, "Closed Label", email, entity.OrganizerStatusDeactivated)

		got, err := repo.ListByOwnerEmail(ctx, email)

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("returns an empty list for an unknown email", func(t *testing.T) {
		// @spec components/entity/organizer/list-by-owner-email "Unknown email"
		got, err := repo.ListByOwnerEmail(ctx, uniqueEmail())

		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})
}
