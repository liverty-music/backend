package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
)

func TestRejectedScan_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		reason   entity.RejectedScanReason
		ticketID entity.TicketID
		wantErr  bool
	}{
		// @spec components/entity/rejected-scan "Forged scan"
		{name: "forged without a ticket", reason: entity.RejectedScanReasonForged, wantErr: false},
		// @spec components/entity/rejected-scan "Expired code"
		{name: "expired naming a presented ticket", reason: entity.RejectedScanReasonExpired, ticketID: "t-1", wantErr: false},
		// @spec components/entity/rejected-scan "Forged scan naming a ticket"
		{name: "forged naming a ticket", reason: entity.RejectedScanReasonForged, ticketID: "t-1", wantErr: true},
		{name: "not holder without a ticket", reason: entity.RejectedScanReasonNotHolder, wantErr: true},
		{name: "unspecified reason", reason: entity.RejectedScanReasonUnspecified, ticketID: "t-1", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			scan := entity.NewRejectedScan("event-1", "link-1", tt.ticketID, tt.reason, at(18, 40, 0))
			err := scan.Validate()
			if tt.wantErr {
				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
				return
			}
			assert.NoError(t, err)
		})
	}
}
