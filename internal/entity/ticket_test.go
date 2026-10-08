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
