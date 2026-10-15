package usecase_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestOrganizerUseCase_SendSignInLinks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const email = "band@example.com"
	xx := &entity.Organizer{ID: "org-xx", Name: "XX Records", ShortName: "xx-records", OwnerEmail: email, Status: entity.OrganizerStatusActive}

	// newDeps returns the dependencies with the background send run before
	// SendSignInLinks returns.
	newDeps := func(t *testing.T) *organizerTestDeps {
		t.Helper()
		d := newOrganizerTestDeps(t)
		usecase.RunSignInLinksSynchronously(d.uc)
		return d
	}

	// @spec components/usecase/organizer/send-sign-in-links "Owner asks for links"
	t.Run("sends the links of the active Organizers the address owns", func(t *testing.T) {
		t.Parallel()
		d := newDeps(t)
		d.orgRepo.EXPECT().ListByOwnerEmail(mock.Anything, email).Return([]*entity.Organizer{xx}, nil).Once()
		d.signInLinks.EXPECT().SendSignInLinks(mock.Anything, email, []*entity.Organizer{xx}).Return(nil).Once()

		d.uc.SendSignInLinks(ctx, email)
	})

	t.Run("sends to the owner email as stored when the request spells it in other letter case", func(t *testing.T) {
		t.Parallel()
		d := newDeps(t)
		d.orgRepo.EXPECT().ListByOwnerEmail(mock.Anything, "Band@Example.com").Return([]*entity.Organizer{xx}, nil).Once()
		d.signInLinks.EXPECT().SendSignInLinks(mock.Anything, email, []*entity.Organizer{xx}).Return(nil).Once()

		d.uc.SendSignInLinks(ctx, "Band@Example.com")
	})

	// @spec components/usecase/organizer/send-sign-in-links "Address that owns nothing"
	t.Run("sends nothing for an address that owns no active Organizer", func(t *testing.T) {
		t.Parallel()
		d := newDeps(t)
		d.orgRepo.EXPECT().ListByOwnerEmail(mock.Anything, "nobody@example.com").Return([]*entity.Organizer{}, nil).Once()
		// The strict SignInLinkSender mock fails the test if anything is sent.

		d.uc.SendSignInLinks(ctx, "nobody@example.com")
	})

	// @spec components/usecase/organizer/send-sign-in-links "Mail service unavailable"
	t.Run("still succeeds when the mail cannot be sent", func(t *testing.T) {
		t.Parallel()
		d := newDeps(t)
		d.orgRepo.EXPECT().ListByOwnerEmail(mock.Anything, email).Return([]*entity.Organizer{xx}, nil).Once()
		d.signInLinks.EXPECT().SendSignInLinks(mock.Anything, email, []*entity.Organizer{xx}).
			Return(apperr.New(codes.Internal, "sign-in links cannot be sent")).Once()

		d.uc.SendSignInLinks(ctx, email)
	})

	t.Run("still succeeds when the Organizers cannot be read", func(t *testing.T) {
		t.Parallel()
		d := newDeps(t)
		d.orgRepo.EXPECT().ListByOwnerEmail(mock.Anything, email).Return(nil, apperr.ErrInternal).Once()

		d.uc.SendSignInLinks(ctx, email)
	})

	// @spec components/usecase/organizer/send-sign-in-links "Fourth request for one address"
	t.Run("sends at most 3 emails per address, comparing it without regard to letter case", func(t *testing.T) {
		t.Parallel()
		d := newDeps(t)
		d.orgRepo.EXPECT().ListByOwnerEmail(mock.Anything, mock.Anything).Return([]*entity.Organizer{xx}, nil).Times(3)
		d.signInLinks.EXPECT().SendSignInLinks(mock.Anything, mock.Anything, []*entity.Organizer{xx}).Return(nil).Times(3)

		d.uc.SendSignInLinks(ctx, email)
		d.uc.SendSignInLinks(ctx, "Band@Example.com")
		d.uc.SendSignInLinks(ctx, email)
		// The fourth request within the window returns without a lookup or
		// a send (the mocks allow exactly three of each).
		d.uc.SendSignInLinks(ctx, "BAND@example.com")

		// Another address has its own allowance.
		d.orgRepo.EXPECT().ListByOwnerEmail(mock.Anything, "other@example.com").Return([]*entity.Organizer{}, nil).Once()
		d.uc.SendSignInLinks(ctx, "other@example.com")
	})

	t.Run("answers before the lookup and the send run", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerTestDeps(t)
		var tasks []func()
		usecase.RunSignInLinksWith(d.uc, func(task func()) { tasks = append(tasks, task) })

		// No repository or sender call happens before SendSignInLinks returns.
		d.uc.SendSignInLinks(ctx, email)

		assert.Len(t, tasks, 1)
		d.orgRepo.EXPECT().ListByOwnerEmail(mock.Anything, email).Return([]*entity.Organizer{}, nil).Once()
		tasks[0]()
	})
}

func TestOrganizerUseCase_Create_validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                          string
		orgName, shortName, ownerMail string
	}{
		{name: "a malformed short name", orgName: "XX Records", shortName: "XX Records", ownerMail: "band@example.com"},
		{name: "a reserved short name", orgName: "XX Records", shortName: "admin", ownerMail: "band@example.com"},
		{name: "a malformed owner email", orgName: "XX Records", shortName: "xx-records", ownerMail: "band@"},
		{name: "an empty name", orgName: "", shortName: "xx-records", ownerMail: "band@example.com"},
	}
	for _, tt := range tests {
		t.Run("refuses "+tt.name+" with InvalidArgument and stores nothing", func(t *testing.T) {
			t.Parallel()
			// The strict mocks fail the test if anything is stored or provisioned.
			d := newOrganizerTestDeps(t)

			got, err := d.uc.Create(context.Background(), tt.orgName, tt.shortName, tt.ownerMail)

			assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			assert.Nil(t, got)
		})
	}
}
