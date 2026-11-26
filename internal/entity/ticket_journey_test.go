package entity_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/liverty-music/backend/internal/entity"
)

func TestTicketJourneySetBy_IsValid(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		setBy entity.TicketJourneySetBy
		want  bool
	}{
		{entity.TicketJourneySetByFan, true},
		{entity.TicketJourneySetByPlatform, true},
		{entity.TicketJourneySetByInference, true},
		{entity.TicketJourneySetBy(""), false},
		{entity.TicketJourneySetBy("default"), false},
	} {
		t.Run(string(tt.setBy), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.setBy.IsValid())
		})
	}
}

func TestTicketJourney_ReplaceableBy(t *testing.T) {
	t.Parallel()

	journey := func(s entity.TicketJourneyStatus, by entity.TicketJourneySetBy) *entity.TicketJourney {
		return &entity.TicketJourney{UserID: "fan-1", EventID: "event-1", Status: s, StatusSetBy: by}
	}
	tests := []struct {
		name    string
		journey *entity.TicketJourney
		status  entity.TicketJourneyStatus
		setBy   entity.TicketJourneySetBy
		want    bool
	}{
		{"no journey takes inference", nil, entity.TicketJourneyStatusTracking, entity.TicketJourneySetByInference, true},
		{"no journey takes the platform", nil, entity.TicketJourneyStatusApplied, entity.TicketJourneySetByPlatform, true},
		// @spec components/entity/ticket-journey "Inference over an existing journey"
		{"inference over an existing journey", journey(entity.TicketJourneyStatusLost, entity.TicketJourneySetByFan), entity.TicketJourneyStatusTracking, entity.TicketJourneySetByInference, false},
		// @spec components/entity/ticket-journey "Entry after a lost presale"
		{"entry after a lost presale", journey(entity.TicketJourneyStatusLost, entity.TicketJourneySetByFan), entity.TicketJourneyStatusApplied, entity.TicketJourneySetByPlatform, true},
		// @spec components/entity/ticket-journey "Entry announced after the purchase"
		{"entry announced after the purchase", journey(entity.TicketJourneyStatusPaid, entity.TicketJourneySetByPlatform), entity.TicketJourneyStatusApplied, entity.TicketJourneySetByPlatform, false},
		// @spec components/entity/ticket-journey "Loss of an applied journey"
		{"loss of an applied journey", journey(entity.TicketJourneyStatusApplied, entity.TicketJourneySetByPlatform), entity.TicketJourneyStatusLost, entity.TicketJourneySetByPlatform, true},
		// @spec components/entity/ticket-journey "Loss after the fan secured a ticket elsewhere"
		{"loss after the fan secured a ticket elsewhere", journey(entity.TicketJourneyStatusPaid, entity.TicketJourneySetByFan), entity.TicketJourneyStatusLost, entity.TicketJourneySetByPlatform, false},
		// @spec components/entity/ticket-journey "Loss the fan already recorded"
		{"loss the fan already recorded", journey(entity.TicketJourneyStatusLost, entity.TicketJourneySetByFan), entity.TicketJourneyStatusLost, entity.TicketJourneySetByPlatform, false},
		{"platform Lost over Tracking", journey(entity.TicketJourneyStatusTracking, entity.TicketJourneySetByInference), entity.TicketJourneyStatusLost, entity.TicketJourneySetByPlatform, true},
		{"platform Lost over Unpaid", journey(entity.TicketJourneyStatusUnpaid, entity.TicketJourneySetByFan), entity.TicketJourneyStatusLost, entity.TicketJourneySetByPlatform, false},
		{"no journey takes a platform Lost", nil, entity.TicketJourneyStatusLost, entity.TicketJourneySetByPlatform, true},
		{"platform Applied over Tracking", journey(entity.TicketJourneyStatusTracking, entity.TicketJourneySetByInference), entity.TicketJourneyStatusApplied, entity.TicketJourneySetByPlatform, true},
		{"platform Applied over Applied", journey(entity.TicketJourneyStatusApplied, entity.TicketJourneySetByFan), entity.TicketJourneyStatusApplied, entity.TicketJourneySetByPlatform, true},
		{"platform Applied over Unpaid", journey(entity.TicketJourneyStatusUnpaid, entity.TicketJourneySetByFan), entity.TicketJourneyStatusApplied, entity.TicketJourneySetByPlatform, false},
		{"platform Paid over Paid set by the fan", journey(entity.TicketJourneyStatusPaid, entity.TicketJourneySetByFan), entity.TicketJourneyStatusPaid, entity.TicketJourneySetByPlatform, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.journey.ReplaceableBy(tt.status, tt.setBy))
		})
	}
}
