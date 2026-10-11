package entity_test

import (
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestTicket_IsAdmissible(t *testing.T) {
	t.Parallel()

	admittedAt := at(18, 32, 0)
	tests := []struct {
		name     string
		status   entity.TicketStatus
		admitted *time.Time
		want     bool
	}{
		// @spec components/entity/ticket "Issued and not yet admitted"
		{name: "issued, not admitted", status: entity.TicketStatusIssued, want: true},
		// @spec components/entity/ticket "Already admitted"
		{name: "issued, admitted at 18:32", status: entity.TicketStatusIssued, admitted: &admittedAt, want: false},
		// @spec components/entity/ticket "Voided"
		{name: "voided, not admitted", status: entity.TicketStatusVoided, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ticket := &entity.Ticket{Status: tt.status, AdmittedTime: tt.admitted}
			assert.Equal(t, tt.want, ticket.IsAdmissible())
		})
	}
}

func TestTicket_Face(t *testing.T) {
	t.Parallel()

	ticket := &entity.Ticket{
		UserID:                         "user-1",
		EventID:                        "event-1",
		ResaleWithoutConsentProhibited: true,
		Status:                         entity.TicketStatusIssued,
	}

	t.Run("issued ticket face", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/ticket "Issued ticket face"
		user := &entity.User{ID: "user-1", FullName: "山田 花子", PhoneNumber: "+819012345678"}

		assert.Equal(t, entity.TicketFace{
			ResaleWithoutConsentProhibited: true,
			EventID:                        "event-1",
			UserID:                         "user-1",
			FullName:                       "山田 花子",
			PhoneNumber:                    "+819012345678",
		}, ticket.Face(user))
	})

	t.Run("user corrects their details", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/ticket "User corrects their details"
		user := &entity.User{ID: "user-1", FullName: "山田 花子", PhoneNumber: "+819012345678"}
		user.PhoneNumber = "+818011112222"

		assert.Equal(t, "+818011112222", ticket.Face(user).PhoneNumber)
	})
}

func TestTicket_Validate(t *testing.T) {
	t.Parallel()

	// @spec components/entity/ticket "Resale flag cannot be false"
	assert.Error(t, (&entity.Ticket{ResaleWithoutConsentProhibited: false}).Validate())
	assert.NoError(t, (&entity.Ticket{ResaleWithoutConsentProhibited: true}).Validate())
}
