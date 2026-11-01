package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// organizerTestDeps holds all dependencies for OrganizerUseCase tests.
type organizerTestDeps struct {
	orgRepo          *mocks.MockOrganizerRepository
	signInLinks      *mocks.MockSignInLinkSender
	artistRepo       *mocks.MockArtistRepository
	organizationRepo *mocks.MockOrganizationRepository
	seriesImageRepo  *mocks.MockSeriesImageRepository
	imageStorer      *ucmocks.MockImageStorer
	metrics          *ucmocks.MockOrganizerMetrics
	uc               usecase.OrganizerUseCase
}

func newOrganizerTestDeps(t *testing.T) *organizerTestDeps {
	t.Helper()
	d := &organizerTestDeps{
		orgRepo:          mocks.NewMockOrganizerRepository(t),
		signInLinks:      mocks.NewMockSignInLinkSender(t),
		artistRepo:       mocks.NewMockArtistRepository(t),
		organizationRepo: mocks.NewMockOrganizationRepository(t),
		seriesImageRepo:  mocks.NewMockSeriesImageRepository(t),
		imageStorer:      ucmocks.NewMockImageStorer(t),
		metrics:          ucmocks.NewMockOrganizerMetrics(t),
	}
	d.uc = usecase.NewOrganizerUseCase(
		d.orgRepo,
		d.signInLinks,
		d.artistRepo,
		d.organizationRepo,
		d.seriesImageRepo,
		d.imageStorer,
		usecase.StorageBuckets{Internal: testInternalBucket, Served: testServedBucket},
		d.metrics,
		newTestLogger(t),
	)
	return d
}

const (
	testInternalBucket = "liverty-music-test-internal"
	testServedBucket   = "liverty-music-test"
)

func TestOrganizerUseCase_Create(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const (
		orgName   = "Acme Music"
		shortName = "acme-music"
		email     = "band@example.com"
	)
	errEmailRejected := apperr.New(codes.InvalidArgument, "owner email was rejected by the identity provider")

	// isNewOrganizer matches the provisioning Organizer Create inserts.
	isNewOrganizer := mock.MatchedBy(func(o *entity.Organizer) bool {
		return o.Name == orgName && o.ShortName == shortName && o.OwnerEmail == email &&
			o.Status == entity.OrganizerStatusProvisioning
	})
	// expectInsert expects the provisioning row insert, returning the
	// inserted row.
	expectInsert := func(d *organizerTestDeps) {
		d.orgRepo.EXPECT().
			Create(ctx, isNewOrganizer).
			Return(&entity.Organizer{ID: "org-1", Name: orgName, ShortName: shortName, OwnerEmail: email, Status: entity.OrganizerStatusProvisioning}, nil).
			Once()
	}
	// expectOrganization expects the Organization creation and the immediate
	// recording of its id.
	expectOrganization := func(d *organizerTestDeps, calls *[]string) {
		d.organizationRepo.EXPECT().Create(mock.Anything, shortName).Run(func(context.Context, string) {
			*calls = append(*calls, "create-org")
		}).Return("zitadel-org-1", nil).Once()
		d.orgRepo.EXPECT().SetProviderOrganizerID(mock.Anything, "org-1", "zitadel-org-1").Run(func(context.Context, string, string) {
			*calls = append(*calls, "store-org-id")
		}).Return(nil).Once()
	}
	// expectCompletion expects the provisioning, the activation and its
	// announcement of a successful Create.
	expectCompletion := func(d *organizerTestDeps, calls *[]string) {
		d.organizationRepo.EXPECT().Provision(mock.Anything, "zitadel-org-1", shortName, email).Run(func(context.Context, string, string, string) {
			*calls = append(*calls, "provision")
		}).Return(nil).Once()
		d.orgRepo.EXPECT().
			CompareAndSetStatus(mock.Anything, "org-1", entity.OrganizerStatusProvisioning, entity.OrganizerStatusActive).
			Return(true, nil).
			Once()
		d.metrics.EXPECT().RecordOrganizerProvisioning(mock.Anything, "success").Return().Once()
	}
	// expectDiscard expects the compensation of a permanent failure: Deactivate
	// then Delete of the row and its Organization. deleteErr fails the final
	// record deletion.
	expectDiscard := func(d *organizerTestDeps, calls *[]string, deleteErr error) {
		withOrg := &entity.Organizer{ID: "org-1", ProviderOrganizerID: "zitadel-org-1", Status: entity.OrganizerStatusProvisioning}
		deactivated := &entity.Organizer{ID: "org-1", ProviderOrganizerID: "zitadel-org-1", Status: entity.OrganizerStatusDeactivated}
		d.orgRepo.EXPECT().Get(mock.Anything, "org-1").Return(withOrg, nil).Once()
		d.organizationRepo.EXPECT().DeactivateIdentities(mock.Anything, "zitadel-org-1").Return(nil).Once()
		d.orgRepo.EXPECT().FreeArtists(mock.Anything, "org-1").Return(nil).Once()
		d.orgRepo.EXPECT().SetStatus(mock.Anything, "org-1", entity.OrganizerStatusDeactivated).Run(func(context.Context, string, entity.OrganizerStatus) {
			*calls = append(*calls, "deactivate")
		}).Return(nil).Once()
		d.orgRepo.EXPECT().Get(mock.Anything, "org-1").Return(deactivated, nil).Once()
		d.orgRepo.EXPECT().Delete(mock.Anything, "org-1", true).Return(nil).Once()
		d.seriesImageRepo.EXPECT().ListByOrganizer(mock.Anything, "org-1").Return(nil, nil).Once()
		d.organizationRepo.EXPECT().Delete(mock.Anything, "zitadel-org-1").Run(func(context.Context, string) {
			*calls = append(*calls, "delete-org")
		}).Return(nil).Once()
		d.orgRepo.EXPECT().Delete(mock.Anything, "org-1", false).Run(func(context.Context, string, bool) {
			*calls = append(*calls, "delete-row")
		}).Return(deleteErr).Once()
	}

	tests := []struct {
		name      string
		setup     func(d *organizerTestDeps, calls *[]string)
		want      func(t *testing.T, got *entity.Organizer)
		wantCalls []string
		wantErr   error
	}{
		{
			// @spec components/usecase/organizer/create "Organizer is created"
			name: "return active organizer with ProviderOrganizerID set when all steps succeed, recording the org id before the remaining steps",
			setup: func(d *organizerTestDeps, calls *[]string) {
				expectInsert(d)
				expectOrganization(d, calls)
				expectCompletion(d, calls)
			},
			want: func(t *testing.T, got *entity.Organizer) {
				t.Helper()
				assert.Equal(t, entity.OrganizerStatusActive, got.Status)
				assert.Equal(t, "zitadel-org-1", got.ProviderOrganizerID)
				assert.Equal(t, "org-1", got.ID)
				assert.Equal(t, shortName, got.ShortName)
			},
			wantCalls: []string{"create-org", "store-org-id", "provision"},
		},
		{
			// @spec components/usecase/organizer/create "Owner email of a fan account"
			name: "create the Organizer for an owner email a fan account uses, without checking the email against other Organizations",
			setup: func(d *organizerTestDeps, calls *[]string) {
				// The strict Organization repository mock fails the test on any
				// email check: the fan's email goes straight to Provision,
				// which creates a separate identity in the new Organization.
				expectInsert(d)
				expectOrganization(d, calls)
				expectCompletion(d, calls)
			},
			want: func(t *testing.T, got *entity.Organizer) {
				t.Helper()
				assert.Equal(t, entity.OrganizerStatusActive, got.Status)
				assert.Equal(t, email, got.OwnerEmail)
			},
			wantCalls: []string{"create-org", "store-org-id", "provision"},
		},
		{
			// @spec components/usecase/organizer/create "Short name already used"
			name: "fail with AlreadyExists and create no Organization when the short name is already used",
			setup: func(d *organizerTestDeps, _ *[]string) {
				// The insert refuses the short name; no Organization is created.
				d.orgRepo.EXPECT().
					Create(ctx, isNewOrganizer).
					Return(nil, apperr.New(codes.AlreadyExists, "short name already used")).
					Once()
			},
			wantErr: apperr.ErrAlreadyExists,
		},
		{
			// @spec components/usecase/organizer/create "Owner email rejected during provisioning"
			name: "remove the Organization and the row when the identity provider rejects the owner email",
			setup: func(d *organizerTestDeps, calls *[]string) {
				expectInsert(d)
				expectOrganization(d, calls)
				d.organizationRepo.EXPECT().Provision(mock.Anything, "zitadel-org-1", shortName, email).Return(errEmailRejected).Once()
				d.metrics.EXPECT().RecordOrganizerProvisioning(mock.Anything, "failed").Return().Once()
				expectDiscard(d, calls, nil)
			},
			wantCalls: []string{"create-org", "store-org-id", "deactivate", "delete-org", "delete-row"},
			wantErr:   apperr.ErrInvalidArgument,
		},
		{
			name: "still return InvalidArgument, leaving a deactivated row, when the compensation cannot delete the row",
			setup: func(d *organizerTestDeps, calls *[]string) {
				expectInsert(d)
				expectOrganization(d, calls)
				d.organizationRepo.EXPECT().Provision(mock.Anything, "zitadel-org-1", shortName, email).Return(errEmailRejected).Once()
				d.metrics.EXPECT().RecordOrganizerProvisioning(mock.Anything, "failed").Return().Once()
				expectDiscard(d, calls, apperr.New(codes.Internal, "db down"))
			},
			wantCalls: []string{"create-org", "store-org-id", "deactivate", "delete-org", "delete-row"},
			wantErr:   apperr.ErrInvalidArgument,
		},
		{
			// @spec components/usecase/organizer/create "Provisioning fails"
			name: "leave the row provisioning with its org id recorded when a step after org creation fails transiently",
			setup: func(d *organizerTestDeps, calls *[]string) {
				expectInsert(d)
				expectOrganization(d, calls)
				d.organizationRepo.EXPECT().Provision(mock.Anything, "zitadel-org-1", shortName, email).Return(apperr.ErrInternal).Once()
				d.metrics.EXPECT().RecordOrganizerProvisioning(mock.Anything, "failed").Return().Once()
				// No compensation: the reconciler retries with the recorded id.
			},
			wantCalls: []string{"create-org", "store-org-id"},
			wantErr:   apperr.ErrInternal,
		},
		{
			name: "reuse the Organization an earlier attempt created when Create reports AlreadyExists, resolving it by name",
			setup: func(d *organizerTestDeps, calls *[]string) {
				// AlreadyExists from Organization.Create is not a permanent
				// failure: the strict mocks expect no Deactivate or Delete.
				expectInsert(d)
				d.organizationRepo.EXPECT().Create(mock.Anything, shortName).Run(func(context.Context, string) {
					*calls = append(*calls, "create-org")
				}).Return("", apperr.New(codes.AlreadyExists, "organization name already used")).Once()
				d.organizationRepo.EXPECT().GetByName(mock.Anything, shortName).Run(func(context.Context, string) {
					*calls = append(*calls, "get-org-by-name")
				}).Return("zitadel-org-1", nil).Once()
				d.orgRepo.EXPECT().SetProviderOrganizerID(mock.Anything, "org-1", "zitadel-org-1").Run(func(context.Context, string, string) {
					*calls = append(*calls, "store-org-id")
				}).Return(nil).Once()
				expectCompletion(d, calls)
			},
			want: func(t *testing.T, got *entity.Organizer) {
				t.Helper()
				assert.Equal(t, entity.OrganizerStatusActive, got.Status)
				assert.Equal(t, "zitadel-org-1", got.ProviderOrganizerID)
			},
			wantCalls: []string{"create-org", "get-org-by-name", "store-org-id", "provision"},
		},
		{
			name: "fail with Internal and leave the row provisioning when the existing Organization is not found by name",
			setup: func(d *organizerTestDeps, calls *[]string) {
				expectInsert(d)
				d.organizationRepo.EXPECT().Create(mock.Anything, shortName).Run(func(context.Context, string) {
					*calls = append(*calls, "create-org")
				}).Return("", apperr.New(codes.AlreadyExists, "organization name already used")).Once()
				d.organizationRepo.EXPECT().GetByName(mock.Anything, shortName).Run(func(context.Context, string) {
					*calls = append(*calls, "get-org-by-name")
				}).Return("", apperr.New(codes.NotFound, "organization not found")).Once()
				d.metrics.EXPECT().RecordOrganizerProvisioning(mock.Anything, "failed").Return().Once()
				// Internal is transient: no compensation, nothing recorded, and
				// the reconciler retries on the next sweep.
			},
			wantCalls: []string{"create-org", "get-org-by-name"},
			wantErr:   apperr.ErrInternal,
		},
		{
			name: "return error and record failed metric when the Organization cannot be created",
			setup: func(d *organizerTestDeps, _ *[]string) {
				expectInsert(d)
				d.organizationRepo.EXPECT().Create(mock.Anything, shortName).Return("", apperr.ErrInternal).Once()
				d.metrics.EXPECT().RecordOrganizerProvisioning(mock.Anything, "failed").Return().Once()
			},
			wantErr: apperr.ErrInternal,
		},
		{
			// @spec components/usecase/organizer/create "Deactivated meanwhile"
			name: "do not clobber a concurrent deactivation when activation is superseded",
			setup: func(d *organizerTestDeps, calls *[]string) {
				expectInsert(d)
				expectOrganization(d, calls)
				d.organizationRepo.EXPECT().Provision(mock.Anything, "zitadel-org-1", shortName, email).Return(nil).Once()
				// A concurrent Deactivate already moved the row out of provisioning,
				// so the CAS does not apply. The saga must NOT record success, emit
				// organizer.created, or force the row back to active.
				d.orgRepo.EXPECT().
					CompareAndSetStatus(mock.Anything, "org-1", entity.OrganizerStatusProvisioning, entity.OrganizerStatusActive).
					Return(false, nil).
					Once()
			},
			want: func(t *testing.T, got *entity.Organizer) {
				t.Helper()
				assert.Equal(t, entity.OrganizerStatusProvisioning, got.Status)
			},
			wantCalls: []string{"create-org", "store-org-id"},
		},
		{
			// @spec components/usecase/organizer/create "Activation fails"
			name: "leave the row provisioning when the activation cannot be recorded",
			setup: func(d *organizerTestDeps, calls *[]string) {
				expectInsert(d)
				expectOrganization(d, calls)
				d.organizationRepo.EXPECT().Provision(mock.Anything, "zitadel-org-1", shortName, email).Return(nil).Once()
				// Organizer.CompareAndSetStatus fails with Unavailable when its
				// announcement cannot be recorded, and changes nothing.
				d.orgRepo.EXPECT().
					CompareAndSetStatus(mock.Anything, "org-1", entity.OrganizerStatusProvisioning, entity.OrganizerStatusActive).
					Return(false, apperr.New(codes.Unavailable, "failed to record the announcement")).
					Once()
			},
			wantCalls: []string{"create-org", "store-org-id"},
			wantErr:   apperr.ErrUnavailable,
		},
		{
			name: "return error when repository Create fails",
			setup: func(d *organizerTestDeps, _ *[]string) {
				d.orgRepo.EXPECT().
					Create(ctx, isNewOrganizer).
					Return(nil, apperr.ErrInternal).
					Once()
			},
			wantErr: apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newOrganizerTestDeps(t)
			var calls []string
			tt.setup(d, &calls)

			got, err := d.uc.Create(ctx, orgName, shortName, email)

			assert.Equal(t, tt.wantCalls, calls)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}
			assert.NoError(t, err)
			if tt.want != nil {
				tt.want(t, got)
			}
		})
	}
}

// @spec components/usecase/organizer/create "Same name again"
func TestOrganizerUseCase_Create_sameNameTwice(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const orgName = "Acme Music"
	d := newOrganizerTestDeps(t)

	// Create never looks up an existing Organizer by name: each call with its
	// own short name inserts its own row and creates its own Organization,
	// named by that short name.
	const email = "band@example.com"
	creations := []struct{ id, shortName string }{
		{id: "org-1", shortName: "acme-music"},
		{id: "org-2", shortName: "acme-music-2"},
	}
	for _, c := range creations {
		d.orgRepo.EXPECT().
			Create(ctx, mock.MatchedBy(func(o *entity.Organizer) bool { return o.ShortName == c.shortName })).
			Return(&entity.Organizer{ID: c.id, Name: orgName, ShortName: c.shortName, OwnerEmail: email, Status: entity.OrganizerStatusProvisioning}, nil).
			Once()
		d.organizationRepo.EXPECT().Create(mock.Anything, c.shortName).Return("zitadel-"+c.id, nil).Once()
		d.orgRepo.EXPECT().SetProviderOrganizerID(mock.Anything, c.id, "zitadel-"+c.id).Return(nil).Once()
		d.organizationRepo.EXPECT().Provision(mock.Anything, "zitadel-"+c.id, c.shortName, email).Return(nil).Once()
		d.orgRepo.EXPECT().
			CompareAndSetStatus(mock.Anything, c.id, entity.OrganizerStatusProvisioning, entity.OrganizerStatusActive).
			Return(true, nil).
			Once()
	}
	d.metrics.EXPECT().RecordOrganizerProvisioning(mock.Anything, "success").Return().Times(2)

	first, err := d.uc.Create(ctx, orgName, creations[0].shortName, email)
	require.NoError(t, err)
	second, err := d.uc.Create(ctx, orgName, creations[1].shortName, email)
	require.NoError(t, err)

	assert.NotEqual(t, first.ID, second.ID, "two Organizers exist")
	assert.NotEqual(t, first.ProviderOrganizerID, second.ProviderOrganizerID, "each Organizer has its own Organization")
	assert.Equal(t, entity.OrganizerStatusActive, first.Status)
	assert.Equal(t, entity.OrganizerStatusActive, second.Status)
}

func TestOrganizerUseCase_Get(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name    string
		id      string
		setup   func(t *testing.T, d *organizerTestDeps)
		want    *entity.Organizer
		wantErr error
	}{
		{
			name: "return organizer when found",
			id:   "org-1",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-1").
					Return(&entity.Organizer{ID: "org-1", Name: "Acme"}, nil).
					Once()
			},
			want: &entity.Organizer{ID: "org-1", Name: "Acme"},
		},
		{
			name: "return NotFound error when organizer does not exist",
			id:   "missing-org",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "missing-org").
					Return(nil, apperr.New(codes.NotFound, "not found")).
					Once()
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newOrganizerTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			got, err := d.uc.Get(ctx, tt.id)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestOrganizerUseCase_List(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("return all organizers from repository", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		want := []*entity.Organizer{
			{ID: "org-1", Name: "Acme"},
			{ID: "org-2", Name: "Beta"},
		}
		d.orgRepo.EXPECT().List(ctx).Return(want, nil).Once()

		got, err := d.uc.List(ctx)

		assert.NoError(t, err)
		assert.Equal(t, want, got)
	})
}

func TestArtistUseCase_ListByOrganizer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name        string
		organizerID string
		setup       func(t *testing.T, d *organizerTestDeps)
		want        []*entity.Artist
		wantErr     error
	}{
		{
			// @spec components/usecase/artist/list-by-organizer "Organizer with Artists"
			name:        "return artists for existing organizer",
			organizerID: "org-1",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-1").
					Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusActive}, nil).
					Once()
				want := []*entity.Artist{{ID: "artist-1"}, {ID: "artist-2"}}
				d.orgRepo.EXPECT().
					ListArtists(ctx, "org-1").
					Return(want, nil).
					Once()
			},
			want: []*entity.Artist{{ID: "artist-1"}, {ID: "artist-2"}},
		},
		{
			// @spec components/usecase/artist/list-by-organizer "Deactivated Organizer"
			name:        "return an empty roster for a deactivated organizer",
			organizerID: "org-2",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-2").
					Return(&entity.Organizer{ID: "org-2", Status: entity.OrganizerStatusDeactivated}, nil).
					Once()
				d.orgRepo.EXPECT().
					ListArtists(ctx, "org-2").
					Return([]*entity.Artist{}, nil).
					Once()
			},
			want: []*entity.Artist{},
		},
		{
			// @spec components/usecase/artist/list-by-organizer "Unknown Organizer"
			name:        "return NotFound error when organizer does not exist",
			organizerID: "missing-org",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "missing-org").
					Return(nil, apperr.New(codes.NotFound, "not found")).
					Once()
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newOrganizerTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			got, err := usecase.NewArtistUseCase(nil, d.orgRepo, nil, nil, nil, newTestLogger(t)).ListByOrganizer(ctx, tt.organizerID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestOrganizerUseCase_ResolveCaller(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name                string
		providerOrganizerID string
		setup               func(t *testing.T, d *organizerTestDeps)
		want                *entity.Organizer
		wantErr             error
	}{
		{
			// @spec components/usecase/organizer/resolve-caller "Active Organizer"
			name:                "return the organizer when active",
			providerOrganizerID: "zitadel-org-1",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					GetByProviderOrganizerID(ctx, "zitadel-org-1").
					Return(&entity.Organizer{ID: "org-1", ProviderOrganizerID: "zitadel-org-1", Status: entity.OrganizerStatusActive}, nil).
					Once()
			},
			want: &entity.Organizer{ID: "org-1", ProviderOrganizerID: "zitadel-org-1", Status: entity.OrganizerStatusActive},
		},
		{
			// @spec components/usecase/organizer/resolve-caller "Deactivated Organizer"
			name:                "FailedPrecondition when the organizer is deactivated",
			providerOrganizerID: "zitadel-org-deactivated",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					GetByProviderOrganizerID(ctx, "zitadel-org-deactivated").
					Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusDeactivated}, nil).
					Once()
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			// Non-revealing per spec D3: any non-Active, non-Deactivated
			// status (e.g. still provisioning) collapses into the same
			// PermissionDenied as "no such organizer."
			// @spec components/usecase/organizer/resolve-caller "Provisioning Organizer"
			name:                "PermissionDenied when the organizer is still provisioning",
			providerOrganizerID: "zitadel-org-provisioning",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					GetByProviderOrganizerID(ctx, "zitadel-org-provisioning").
					Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusProvisioning}, nil).
					Once()
			},
			wantErr: apperr.ErrPermissionDenied,
		},
		{
			// @spec components/usecase/organizer/resolve-caller "Organization with no Organizer"
			name:                "PermissionDenied (non-revealing) when no organizer is linked",
			providerOrganizerID: "zitadel-org-unknown",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					GetByProviderOrganizerID(ctx, "zitadel-org-unknown").
					Return(nil, apperr.New(codes.NotFound, "not found")).
					Once()
			},
			wantErr: apperr.ErrPermissionDenied,
		},
		{
			// @spec components/usecase/organizer/resolve-caller "Organizer unreadable"
			name:                "propagates a non-NotFound repository failure unchanged",
			providerOrganizerID: "zitadel-org-broken",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					GetByProviderOrganizerID(ctx, "zitadel-org-broken").
					Return(nil, apperr.New(codes.Internal, "db down")).
					Once()
			},
			wantErr: apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newOrganizerTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			got, err := d.uc.ResolveCaller(ctx, tt.providerOrganizerID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestOrganizerUseCase_AssociateArtist(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	type args struct {
		organizerID string
		artistID    string
	}

	tests := []struct {
		name    string
		args    args
		setup   func(t *testing.T, d *organizerTestDeps)
		wantErr error
	}{
		{
			name: "success when organizer is active and artist exists",
			args: args{organizerID: "org-1", artistID: "artist-1"},
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-1").
					Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusActive}, nil).
					Once()
				d.artistRepo.EXPECT().
					Get(ctx, "artist-1").
					Return(&entity.Artist{ID: "artist-1"}, nil).
					Once()
				d.orgRepo.EXPECT().
					AssociateArtist(ctx, "org-1", "artist-1").
					Return(nil).
					Once()
			},
		},
		{
			name: "return NotFound error when organizer does not exist",
			args: args{organizerID: "missing-org", artistID: "artist-1"},
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "missing-org").
					Return(nil, apperr.New(codes.NotFound, "not found")).
					Once()
				// artistRepo.Get must NOT be called.
			},
			wantErr: apperr.ErrNotFound,
		},
		{
			name: "return FailedPrecondition when organizer is deactivated without calling artistRepo",
			args: args{organizerID: "org-1", artistID: "artist-1"},
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-1").
					Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusDeactivated}, nil).
					Once()
				// artistRepo.Get must NOT be called.
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name: "return NotFound error when artist does not exist",
			args: args{organizerID: "org-1", artistID: "missing-artist"},
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-1").
					Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusActive}, nil).
					Once()
				d.artistRepo.EXPECT().
					Get(ctx, "missing-artist").
					Return(nil, apperr.New(codes.NotFound, "artist not found")).
					Once()
			},
			wantErr: apperr.ErrNotFound,
		},
		{
			name: "return AlreadyExists when artist is already associated with an organizer",
			args: args{organizerID: "org-1", artistID: "artist-1"},
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-1").
					Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusActive}, nil).
					Once()
				d.artistRepo.EXPECT().
					Get(ctx, "artist-1").
					Return(&entity.Artist{ID: "artist-1"}, nil).
					Once()
				d.orgRepo.EXPECT().
					AssociateArtist(ctx, "org-1", "artist-1").
					Return(apperr.New(codes.AlreadyExists, "artist already associated")).
					Once()
			},
			wantErr: apperr.ErrAlreadyExists,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newOrganizerTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			err := d.uc.AssociateArtist(ctx, tt.args.organizerID, tt.args.artistID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestOrganizerUseCase_DisassociateArtist(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	type args struct {
		organizerID string
		artistID    string
	}

	tests := []struct {
		name    string
		args    args
		setup   func(t *testing.T, d *organizerTestDeps)
		wantErr error
	}{
		{
			name: "success when organizer is active",
			args: args{organizerID: "org-1", artistID: "artist-1"},
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-1").
					Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusActive}, nil).
					Once()
				d.orgRepo.EXPECT().
					DisassociateArtist(ctx, "org-1", "artist-1").
					Return(nil).
					Once()
			},
		},
		{
			name: "return FailedPrecondition when organizer is deactivated",
			args: args{organizerID: "org-1", artistID: "artist-1"},
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-1").
					Return(&entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusDeactivated}, nil).
					Once()
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name: "return NotFound error when organizer does not exist",
			args: args{organizerID: "missing-org", artistID: "artist-1"},
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "missing-org").
					Return(nil, apperr.New(codes.NotFound, "not found")).
					Once()
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newOrganizerTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			err := d.uc.DisassociateArtist(ctx, tt.args.organizerID, tt.args.artistID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestOrganizerUseCase_Deactivate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name        string
		organizerID string
		setup       func(t *testing.T, d *organizerTestDeps)
		wantErr     error
	}{
		{
			// @spec components/usecase/organizer/deactivate "Unknown Organizer"
			name:        "return NotFound error when organizer does not exist",
			organizerID: "missing-org",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "missing-org").
					Return(nil, apperr.New(codes.NotFound, "not found")).
					Once()
			},
			wantErr: apperr.ErrNotFound,
		},
		{
			name:        "return nil without writes when organizer is already deactivated",
			organizerID: "org-1",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-1").
					Return(&entity.Organizer{
						ID:                  "org-1",
						Status:              entity.OrganizerStatusDeactivated,
						ProviderOrganizerID: "zitadel-org-1",
					}, nil).
					Once()
				// organizationRepo.DeactivateIdentities, orgRepo.FreeArtists, and
				// orgRepo.SetStatus must NOT be called (idempotent path).
			},
		},
		{
			// @spec components/usecase/organizer/deactivate "Active Organizer is deactivated"
			name:        "deactivate the identities, free artists, and set status when organizer is active with ProviderOrganizerID",
			organizerID: "org-1",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-1").
					Return(&entity.Organizer{
						ID:                  "org-1",
						Status:              entity.OrganizerStatusActive,
						ProviderOrganizerID: "zitadel-org-1",
					}, nil).
					Once()
				d.organizationRepo.EXPECT().
					DeactivateIdentities(ctx, "zitadel-org-1").
					Return(nil).
					Once()
				d.orgRepo.EXPECT().
					FreeArtists(ctx, "org-1").
					Return(nil).
					Once()
				d.orgRepo.EXPECT().
					SetStatus(ctx, "org-1", entity.OrganizerStatusDeactivated).
					Return(nil).
					Once()
			},
		},
		{
			// @spec components/usecase/organizer/deactivate "Unlinked Organization"
			name:        "deactivate the identities of a provisioning organizer whose unrecorded Organization is found by name",
			organizerID: "org-3",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-3").
					Return(&entity.Organizer{ID: "org-3", ShortName: "label-3", Status: entity.OrganizerStatusProvisioning}, nil).
					Once()
				d.organizationRepo.EXPECT().GetByName(ctx, "label-3").Return("zitadel-org-3", nil).Once()
				d.organizationRepo.EXPECT().DeactivateIdentities(ctx, "zitadel-org-3").Return(nil).Once()
				d.orgRepo.EXPECT().FreeArtists(ctx, "org-3").Return(nil).Once()
				d.orgRepo.EXPECT().SetStatus(ctx, "org-3", entity.OrganizerStatusDeactivated).Return(nil).Once()
			},
		},
		{
			name:        "return the error and keep the status when the unrecorded Organization cannot be searched",
			organizerID: "org-3",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-3").
					Return(&entity.Organizer{ID: "org-3", ShortName: "label-3", Status: entity.OrganizerStatusProvisioning}, nil).
					Once()
				d.organizationRepo.EXPECT().GetByName(ctx, "label-3").Return("", apperr.ErrInternal).Once()
			},
			wantErr: apperr.ErrInternal,
		},
		{
			// @spec components/usecase/organizer/deactivate "Provisioning Organizer without an Organization"
			name:        "skip DeactivateIdentities when the organizer has no Organization but still free artists and set status",
			organizerID: "org-2",
			setup: func(t *testing.T, d *organizerTestDeps) {
				t.Helper()
				d.orgRepo.EXPECT().
					Get(ctx, "org-2").
					Return(&entity.Organizer{
						ID:                  "org-2",
						ShortName:           "label-2",
						Status:              entity.OrganizerStatusProvisioning,
						ProviderOrganizerID: "", // not yet provisioned
					}, nil).
					Once()
				d.organizationRepo.EXPECT().GetByName(ctx, "label-2").Return("", apperr.New(codes.NotFound, "organization not found")).Once()
				// organizationRepo.DeactivateIdentities must NOT be called.
				d.orgRepo.EXPECT().
					FreeArtists(ctx, "org-2").
					Return(nil).
					Once()
				d.orgRepo.EXPECT().
					SetStatus(ctx, "org-2", entity.OrganizerStatusDeactivated).
					Return(nil).
					Once()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newOrganizerTestDeps(t)
			if tt.setup != nil {
				tt.setup(t, d)
			}

			err := d.uc.Deactivate(ctx, tt.organizerID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// TestOrganizerUseCase_Deactivate_IdempotentIsDistinct verifies via errors.Is
// that the already-deactivated path truly returns nil (not a code-equal error).
func TestOrganizerUseCase_Deactivate_IdempotentIsDistinct(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	d := newOrganizerTestDeps(t)

	d.orgRepo.EXPECT().
		Get(ctx, "org-1").
		Return(&entity.Organizer{
			ID:     "org-1",
			Status: entity.OrganizerStatusDeactivated,
		}, nil).
		Once()

	err := d.uc.Deactivate(ctx, "org-1")

	// Must be exactly nil — not an error whose code is "success".
	assert.True(t, err == nil, "expected nil error for already-deactivated organizer")
	assert.False(t, errors.Is(err, apperr.ErrNotFound))
}

func TestOrganizerUseCase_ReconcileProvisioning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// expectActivation expects the tail of a successful completion.
	expectActivation := func(d *organizerTestDeps, id string) {
		d.orgRepo.EXPECT().CompareAndSetStatus(mock.Anything, id, entity.OrganizerStatusProvisioning, entity.OrganizerStatusActive).Return(true, nil).Once()
		d.metrics.EXPECT().RecordOrganizerProvisioning(mock.Anything, "success").Return().Once()
	}

	// @spec components/usecase/organizer/reconcile-provisioning "Organization already recorded"
	// @spec components/usecase/organizer/reconcile-provisioning "Organizer left provisioning"
	t.Run("completes every organizer stuck in provisioning, reusing a recorded org id and resolving a missing one", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		stuck := []*entity.Organizer{
			// Failed after the org id was recorded: the retry reuses the id and
			// never creates or searches for the org.
			{ID: "org-1", Name: "Acme", ShortName: "acme", OwnerEmail: "a@acme.com", ProviderOrganizerID: "z-org-1", Status: entity.OrganizerStatusProvisioning},
			// Left by an older release that never recorded the id: the org is
			// created and recorded.
			{ID: "org-2", Name: "Beta", ShortName: "beta", OwnerEmail: "b@beta.com", Status: entity.OrganizerStatusProvisioning},
		}
		d.orgRepo.EXPECT().ListByStatus(ctx, entity.OrganizerStatusProvisioning).Return(stuck, nil).Once()

		d.organizationRepo.EXPECT().Provision(mock.Anything, "z-org-1", "acme", "a@acme.com").Return(nil).Once()
		expectActivation(d, "org-1")

		d.organizationRepo.EXPECT().Create(mock.Anything, "beta").Return("z-org-2", nil).Once()
		d.orgRepo.EXPECT().SetProviderOrganizerID(mock.Anything, "org-2", "z-org-2").Return(nil).Once()
		d.organizationRepo.EXPECT().Provision(mock.Anything, "z-org-2", "beta", "b@beta.com").Return(nil).Once()
		expectActivation(d, "org-2")

		assert.NoError(t, d.uc.ReconcileProvisioning(ctx))
		// Create must not run for org-1 (mockery fails on unexpected calls).
	})

	// @spec components/usecase/organizer/reconcile-provisioning "Organization created but not recorded"
	t.Run("resolves by name and records the Organization an earlier attempt created but did not record", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		stuck := &entity.Organizer{ID: "org-2", Name: "Beta", ShortName: "beta", OwnerEmail: "b@beta.com", Status: entity.OrganizerStatusProvisioning}
		d.orgRepo.EXPECT().ListByStatus(ctx, entity.OrganizerStatusProvisioning).Return([]*entity.Organizer{stuck}, nil).Once()

		d.organizationRepo.EXPECT().Create(mock.Anything, "beta").Return("", apperr.New(codes.AlreadyExists, "organization name already used")).Once()
		d.organizationRepo.EXPECT().GetByName(mock.Anything, "beta").Return("z-org-2", nil).Once()
		d.orgRepo.EXPECT().SetProviderOrganizerID(mock.Anything, "org-2", "z-org-2").Return(nil).Once()
		d.organizationRepo.EXPECT().Provision(mock.Anything, "z-org-2", "beta", "b@beta.com").Return(nil).Once()
		expectActivation(d, "org-2")

		assert.NoError(t, d.uc.ReconcileProvisioning(ctx))
		// The strict mocks fail on a second Create: no second Organization.
	})

	// @spec components/usecase/organizer/reconcile-provisioning "Deactivated meanwhile"
	t.Run("leaves an Organizer deactivated during the sweep deactivated", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		stuck := &entity.Organizer{ID: "org-1", Name: "Acme", ShortName: "acme", OwnerEmail: "a@acme.com", ProviderOrganizerID: "z-org-1", Status: entity.OrganizerStatusProvisioning}
		d.orgRepo.EXPECT().ListByStatus(ctx, entity.OrganizerStatusProvisioning).Return([]*entity.Organizer{stuck}, nil).Once()
		d.organizationRepo.EXPECT().Provision(mock.Anything, "z-org-1", "acme", "a@acme.com").Return(nil).Once()
		// Deactivated meanwhile: the compare-and-set does not apply, and the
		// strict mocks fail on any write that would reactivate it.
		d.orgRepo.EXPECT().CompareAndSetStatus(mock.Anything, "org-1", entity.OrganizerStatusProvisioning, entity.OrganizerStatusActive).Return(false, nil).Once()

		assert.NoError(t, d.uc.ReconcileProvisioning(ctx))
	})

	t.Run("leaves the Organizer provisioning when the existing Organization is not found by name", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		stuck := &entity.Organizer{ID: "org-2", Name: "Beta", ShortName: "beta", OwnerEmail: "b@beta.com", Status: entity.OrganizerStatusProvisioning}
		d.orgRepo.EXPECT().ListByStatus(ctx, entity.OrganizerStatusProvisioning).Return([]*entity.Organizer{stuck}, nil).Once()

		d.organizationRepo.EXPECT().Create(mock.Anything, "beta").Return("", apperr.New(codes.AlreadyExists, "organization name already used")).Once()
		d.organizationRepo.EXPECT().GetByName(mock.Anything, "beta").Return("", apperr.New(codes.NotFound, "organization not found")).Once()
		d.metrics.EXPECT().RecordOrganizerProvisioning(mock.Anything, "failed").Return().Once()
		// Internal is not permanent: no Deactivate, no SetProviderOrganizerID.

		assert.NoError(t, d.uc.ReconcileProvisioning(ctx))
	})

	// @spec components/usecase/organizer/reconcile-provisioning "One Organizer fails"
	t.Run("skips a transiently failing organizer, leaving it provisioning, and continues the sweep", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		stuck := []*entity.Organizer{
			{ID: "org-bad", Name: "Bad", ShortName: "bad", OwnerEmail: "x@bad.com", ProviderOrganizerID: "z-bad", Status: entity.OrganizerStatusProvisioning},
			{ID: "org-ok", Name: "Ok", ShortName: "okay", OwnerEmail: "y@ok.com", ProviderOrganizerID: "z-ok", Status: entity.OrganizerStatusProvisioning},
		}
		d.orgRepo.EXPECT().ListByStatus(ctx, entity.OrganizerStatusProvisioning).Return(stuck, nil).Once()

		// org-bad: still failing transiently -> failed metric, no status change.
		d.organizationRepo.EXPECT().Provision(mock.Anything, "z-bad", "bad", "x@bad.com").Return(apperr.ErrInternal).Once()
		d.metrics.EXPECT().RecordOrganizerProvisioning(mock.Anything, "failed").Return().Once()

		d.organizationRepo.EXPECT().Provision(mock.Anything, "z-ok", "okay", "y@ok.com").Return(nil).Once()
		expectActivation(d, "org-ok")

		assert.NoError(t, d.uc.ReconcileProvisioning(ctx))
	})

	// @spec components/usecase/organizer/reconcile-provisioning "Owner email rejected"
	t.Run("deactivates an organizer whose owner email the sign-in service rejects so it is not retried forever", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		// The row never recorded its org id.
		stuck := &entity.Organizer{ID: "org-taken", Name: "Taken", ShortName: "taken", OwnerEmail: "band@example.com", Status: entity.OrganizerStatusProvisioning}
		d.orgRepo.EXPECT().ListByStatus(ctx, entity.OrganizerStatusProvisioning).Return([]*entity.Organizer{stuck}, nil).Once()

		d.organizationRepo.EXPECT().Create(mock.Anything, "taken").Return("394277002850336802", nil).Once()
		d.orgRepo.EXPECT().SetProviderOrganizerID(mock.Anything, "org-taken", "394277002850336802").Return(nil).Once()
		d.organizationRepo.EXPECT().Provision(mock.Anything, "394277002850336802", "taken", "band@example.com").
			Return(apperr.New(codes.InvalidArgument, "owner email was rejected by the identity provider")).Once()
		d.metrics.EXPECT().RecordOrganizerProvisioning(mock.Anything, "failed").Return().Once()

		// Terminal: Deactivate moves it out of provisioning; nothing is deleted.
		d.orgRepo.EXPECT().Get(ctx, "org-taken").Return(&entity.Organizer{ID: "org-taken", ProviderOrganizerID: "394277002850336802", Status: entity.OrganizerStatusProvisioning}, nil).Once()
		d.organizationRepo.EXPECT().DeactivateIdentities(ctx, "394277002850336802").Return(nil).Once()
		d.orgRepo.EXPECT().FreeArtists(ctx, "org-taken").Return(nil).Once()
		d.orgRepo.EXPECT().SetStatus(ctx, "org-taken", entity.OrganizerStatusDeactivated).Return(nil).Once()

		assert.NoError(t, d.uc.ReconcileProvisioning(ctx))
	})

	// @spec components/usecase/organizer/reconcile-provisioning "Listing fails"
	t.Run("returns error when listing stuck organizers fails", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		d.orgRepo.EXPECT().ListByStatus(ctx, entity.OrganizerStatusProvisioning).Return(nil, apperr.ErrInternal).Once()
		assert.ErrorIs(t, d.uc.ReconcileProvisioning(ctx), apperr.ErrInternal)
	})
}

func TestOrganizerUseCase_Delete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const orgID = "org-delete"
	deactivated := &entity.Organizer{ID: orgID, ShortName: "label-delete", ProviderOrganizerID: "zitadel-org-delete", Status: entity.OrganizerStatusDeactivated}
	seriesImage := []*entity.SeriesImage{{ID: "image-cover", OrganizerID: orgID}, {ID: "image-unused", OrganizerID: orgID}}
	errOrganization := apperr.New(codes.Internal, "remove organization")

	// expectRemoval expects every removal step of a deactivated organizer and
	// appends each step to calls as it runs.
	expectRemoval := func(d *organizerTestDeps, org *entity.Organizer, calls *[]string, organizationErr error) {
		d.orgRepo.EXPECT().Get(ctx, orgID).Return(org, nil).Once()
		d.orgRepo.EXPECT().Delete(ctx, orgID, true).Run(func(context.Context, string, bool) {
			*calls = append(*calls, "check")
		}).Return(nil).Once()
		d.seriesImageRepo.EXPECT().ListByOrganizer(ctx, orgID).Return(seriesImage, nil).Once()
		for _, m := range seriesImage {
			d.imageStorer.EXPECT().DeleteOriginal(ctx, testInternalBucket, orgID, m.ID).Run(func(context.Context, string, string, string) {
				*calls = append(*calls, "original:"+m.ID)
			}).Return(nil).Once()
			d.imageStorer.EXPECT().DeleteVariants(ctx, testServedBucket, orgID, m.ID).Run(func(context.Context, string, string, string) {
				*calls = append(*calls, "variants:"+m.ID)
			}).Return(nil).Once()
		}
		if org.ProviderOrganizerID != "" {
			d.organizationRepo.EXPECT().Delete(ctx, org.ProviderOrganizerID).Run(func(context.Context, string) {
				*calls = append(*calls, "organization")
			}).Return(organizationErr).Once()
		} else {
			// No recorded link: the Organization is searched by short name and absent.
			d.organizationRepo.EXPECT().GetByName(ctx, org.ShortName).Return("", apperr.New(codes.NotFound, "organization not found")).Once()
		}
		if organizationErr == nil {
			d.orgRepo.EXPECT().Delete(ctx, orgID, false).Run(func(context.Context, string, bool) {
				*calls = append(*calls, "records")
			}).Return(nil).Once()
		}
	}
	filesThen := func(tail ...string) []string {
		return append([]string{"check",
			"original:image-cover", "variants:image-cover",
			"original:image-unused", "variants:image-unused",
		}, tail...)
	}

	tests := []struct {
		name string
		// noStorage builds the usecase without an image storer or buckets.
		noStorage bool
		setup     func(d *organizerTestDeps, calls *[]string)
		wantCalls []string
		wantErr   error
	}{
		{
			// @spec components/usecase/organizer/delete "Deactivated test Organizer"
			name: "removes the image files, then the Organization, then the records",
			setup: func(d *organizerTestDeps, calls *[]string) {
				expectRemoval(d, deactivated, calls, nil)
			},
			wantCalls: filesThen("organization", "records"),
		},
		{
			// @spec components/usecase/organizer/delete "Refunded order"
			name: "removes an organizer whose only order is refunded, as the record check passes",
			setup: func(d *organizerTestDeps, calls *[]string) {
				expectRemoval(d, deactivated, calls, nil)
			},
			wantCalls: filesThen("organization", "records"),
		},
		{
			// @spec components/usecase/organizer/delete "Organizer without an Organization"
			name: "skips the Organization when the organizer has no Organization link",
			setup: func(d *organizerTestDeps, calls *[]string) {
				noOrganization := *deactivated
				noOrganization.ProviderOrganizerID = ""
				expectRemoval(d, &noOrganization, calls, nil)
			},
			wantCalls: filesThen("records"),
		},
		{
			name: "removes an Organization whose link was never recorded, found by its name",
			setup: func(d *organizerTestDeps, calls *[]string) {
				// A provisioning Organizer from before the org id was recorded
				// early, deactivated by the admin: its org must not be orphaned.
				unlinked := *deactivated
				unlinked.ProviderOrganizerID = ""
				d.orgRepo.EXPECT().Get(ctx, orgID).Return(&unlinked, nil).Once()
				d.orgRepo.EXPECT().Delete(ctx, orgID, true).Return(nil).Once()
				d.seriesImageRepo.EXPECT().ListByOrganizer(ctx, orgID).Return(nil, nil).Once()
				d.organizationRepo.EXPECT().GetByName(ctx, "label-delete").Return("394277002850336802", nil).Once()
				d.organizationRepo.EXPECT().Delete(ctx, "394277002850336802").Run(func(context.Context, string) {
					*calls = append(*calls, "organization")
				}).Return(nil).Once()
				d.orgRepo.EXPECT().Delete(ctx, orgID, false).Run(func(context.Context, string, bool) {
					*calls = append(*calls, "records")
				}).Return(nil).Once()
			},
			wantCalls: []string{"organization", "records"},
		},
		{
			name: "keeps the records when the unrecorded Organization cannot be searched",
			setup: func(d *organizerTestDeps, _ *[]string) {
				unlinked := *deactivated
				unlinked.ProviderOrganizerID = ""
				d.orgRepo.EXPECT().Get(ctx, orgID).Return(&unlinked, nil).Once()
				d.orgRepo.EXPECT().Delete(ctx, orgID, true).Return(nil).Once()
				d.seriesImageRepo.EXPECT().ListByOrganizer(ctx, orgID).Return(nil, nil).Once()
				d.organizationRepo.EXPECT().GetByName(ctx, "label-delete").Return("", apperr.ErrInternal).Once()
			},
			wantErr: apperr.ErrInternal,
		},
		{
			// @spec components/usecase/organizer/delete "Active Organizer"
			name: "refuses an active organizer and removes nothing",
			setup: func(d *organizerTestDeps, _ *[]string) {
				d.orgRepo.EXPECT().Get(ctx, orgID).Return(&entity.Organizer{ID: orgID, ProviderOrganizerID: "zitadel-org-delete", Status: entity.OrganizerStatusActive}, nil).Once()
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			// @spec components/usecase/organizer/delete "Unknown Organizer"
			name: "returns NotFound for an unknown organizer",
			setup: func(d *organizerTestDeps, _ *[]string) {
				d.orgRepo.EXPECT().Get(ctx, orgID).Return(nil, apperr.New(codes.NotFound, "organizer not found")).Once()
			},
			wantErr: apperr.ErrNotFound,
		},
		{
			// @spec components/usecase/organizer/delete "Paid order"
			name: "refuses before any file or the Organization is removed when the record check is blocked",
			setup: func(d *organizerTestDeps, _ *[]string) {
				d.orgRepo.EXPECT().Get(ctx, orgID).Return(deactivated, nil).Once()
				d.orgRepo.EXPECT().Delete(ctx, orgID, true).Return(apperr.New(codes.FailedPrecondition, "order not refunded")).Once()
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name:      "returns Internal before removing anything when image storage is not configured",
			noStorage: true,
			setup: func(d *organizerTestDeps, _ *[]string) {
				d.orgRepo.EXPECT().Get(ctx, orgID).Return(deactivated, nil).Once()
				d.orgRepo.EXPECT().Delete(ctx, orgID, true).Return(nil).Once()
				d.seriesImageRepo.EXPECT().ListByOrganizer(ctx, orgID).Return(seriesImage, nil).Once()
			},
			wantErr: apperr.ErrInternal,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newOrganizerTestDeps(t)
			if tt.noStorage {
				d.uc = usecase.NewOrganizerUseCase(d.orgRepo, d.signInLinks, d.artistRepo, d.organizationRepo, d.seriesImageRepo, nil,
					usecase.StorageBuckets{}, d.metrics, newTestLogger(t))
			}
			var calls []string
			tt.setup(d, &calls)

			err := d.uc.Delete(ctx, orgID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.wantCalls, calls)
		})
	}

	// @spec components/usecase/organizer/delete "Organization removal fails"
	t.Run("a failed Organization removal leaves the records and a second call completes the deletion", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		var calls []string
		expectRemoval(d, deactivated, &calls, errOrganization)

		err := d.uc.Delete(ctx, orgID)

		assert.ErrorIs(t, err, apperr.ErrInternal)
		assert.Equal(t, filesThen("organization"), calls, "the records must remain after the Organization removal fails")

		calls = nil
		expectRemoval(d, deactivated, &calls, nil)

		assert.NoError(t, d.uc.Delete(ctx, orgID))
		assert.Equal(t, filesThen("organization", "records"), calls)
	})
}

// TestOrganizerUseCase_CleanUpStuckProvisioningOrganizer walks the admin
// cleanup of an Organizer stuck in provisioning without a recorded
// Organization link (the prod shape of #564): Deactivate accepts it, and
// Delete then removes the Organization found by name, so nothing is orphaned in Zitadel.
func TestOrganizerUseCase_CleanUpStuckProvisioningOrganizer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const (
		orgID               = "01a11dec-968d-762a-a64d-17f4ce185b92"
		providerOrganizerID = "394277002850336802"
	)
	d := newOrganizerTestDeps(t)

	// Deactivate: the provisioning row is accepted and its Organization is resolved.
	d.orgRepo.EXPECT().Get(ctx, orgID).Return(&entity.Organizer{ID: orgID, ShortName: "stuck-label", Status: entity.OrganizerStatusProvisioning}, nil).Once()
	d.organizationRepo.EXPECT().GetByName(ctx, "stuck-label").Return(providerOrganizerID, nil).Once()
	d.organizationRepo.EXPECT().DeactivateIdentities(ctx, providerOrganizerID).Return(nil).Once()
	d.orgRepo.EXPECT().FreeArtists(ctx, orgID).Return(nil).Once()
	d.orgRepo.EXPECT().SetStatus(ctx, orgID, entity.OrganizerStatusDeactivated).Return(nil).Once()

	assert.NoError(t, d.uc.Deactivate(ctx, orgID))

	// Delete: the blocker check runs first, then the Organization found by name
	// is removed, then the records.
	var calls []string
	d.orgRepo.EXPECT().Get(ctx, orgID).Return(&entity.Organizer{ID: orgID, ShortName: "stuck-label", Status: entity.OrganizerStatusDeactivated}, nil).Once()
	d.orgRepo.EXPECT().Delete(ctx, orgID, true).Run(func(context.Context, string, bool) {
		calls = append(calls, "check")
	}).Return(nil).Once()
	d.seriesImageRepo.EXPECT().ListByOrganizer(ctx, orgID).Return(nil, nil).Once()
	d.organizationRepo.EXPECT().GetByName(ctx, "stuck-label").Return(providerOrganizerID, nil).Once()
	d.organizationRepo.EXPECT().Delete(ctx, providerOrganizerID).Run(func(context.Context, string) {
		calls = append(calls, "organization")
	}).Return(nil).Once()
	d.orgRepo.EXPECT().Delete(ctx, orgID, false).Run(func(context.Context, string, bool) {
		calls = append(calls, "records")
	}).Return(nil).Once()

	assert.NoError(t, d.uc.Delete(ctx, orgID))
	assert.Equal(t, []string{"check", "organization", "records"}, calls)
}
