package entity_test

import (
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			ticket := &entity.Ticket{Status: tt.status, AdmitTime: tt.admitted}
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
		user := &entity.User{ID: "user-1", FullName: new("山田 花子"), PhoneNumber: new("+819012345678")}

		assert.Equal(t, entity.TicketFace{
			ResaleWithoutConsentProhibited: true,
			EventID:                        "event-1",
			UserID:                         "user-1",
			FullName:                       new("山田 花子"),
			PhoneNumber:                    new("+819012345678"),
		}, ticket.Face(user))
	})

	t.Run("user corrects their details", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/ticket "User corrects their details"
		user := &entity.User{ID: "user-1", FullName: new("山田 花子"), PhoneNumber: new("+819012345678")}
		user.PhoneNumber = new("+818011112222")

		assert.Equal(t, new("+818011112222"), ticket.Face(user).PhoneNumber)
	})
}

func TestTicket_Validate(t *testing.T) {
	t.Parallel()

	// @spec components/entity/ticket "Resale flag cannot be false"
	assert.ErrorIs(t, (&entity.Ticket{ResaleWithoutConsentProhibited: false}).Validate(), apperr.ErrInvalidArgument)
	assert.NoError(t, (&entity.Ticket{ResaleWithoutConsentProhibited: true}).Validate())
}

func TestNewIssuedTicket(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	order := &entity.Order{ID: "order-1", UserID: "user-1"}

	ticket := entity.NewIssuedTicket(order, "event-1", "iv-1", now)

	assert.NotEmpty(t, ticket.ID)
	assert.Equal(t, order.ID, ticket.OrderID)
	assert.Equal(t, order.UserID, ticket.UserID)
	assert.Equal(t, "event-1", ticket.EventID)
	assert.Equal(t, "iv-1", ticket.IdentityVerificationID)
	assert.Equal(t, entity.TicketStatusIssued, ticket.Status)
	assert.Equal(t, now, ticket.IssueTime)
	assert.NoError(t, ticket.Validate(), "every issued ticket is a covered ticket")
	assert.NotEqual(t, ticket.ID, entity.NewIssuedTicket(order, "event-1", "", now).ID)
}

func TestTicket_BelongsTo(t *testing.T) {
	t.Parallel()
	ticket := &entity.Ticket{UserID: "user-1"}
	assert.True(t, ticket.BelongsTo("user-1"))
	assert.False(t, ticket.BelongsTo("user-2"))
}

func TestTicket_AdmitOutcome(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 11, 20, 18, 32, 0, 0, time.UTC)
	tests := []struct {
		name    string
		ticket  *entity.Ticket
		usable  bool
		want    entity.AdmitResult
		wantErr error
	}{
		{"scanner revoked", &entity.Ticket{Status: entity.TicketStatusIssued}, false, entity.AdmitResult{}, apperr.ErrPermissionDenied},
		{"scanner revoked, whatever the ticket", &entity.Ticket{Status: entity.TicketStatusVoided, AdmitTime: &at}, false, entity.AdmitResult{}, apperr.ErrPermissionDenied},
		{"already admitted", &entity.Ticket{Status: entity.TicketStatusIssued, AdmitTime: &at}, true, entity.AdmitResult{Outcome: entity.AdmitOutcomeAlreadyAdmitted, AdmitTime: at}, nil},
		{"admitted, then voided: still already admitted", &entity.Ticket{Status: entity.TicketStatusVoided, AdmitTime: &at}, true, entity.AdmitResult{Outcome: entity.AdmitOutcomeAlreadyAdmitted, AdmitTime: at}, nil},
		{"voided", &entity.Ticket{Status: entity.TicketStatusVoided}, true, entity.AdmitResult{Outcome: entity.AdmitOutcomeVoided}, nil},
		{"admissible but not admitted", &entity.Ticket{Status: entity.TicketStatusIssued}, true, entity.AdmitResult{}, apperr.ErrInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.ticket.AdmitOutcome(tt.usable)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
