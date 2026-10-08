package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestSeries_HasEventPage(t *testing.T) {
	t.Parallel()

	organizerID := "019a0000-0000-7000-8000-000000000001"
	firstParty := func(visibility entity.SeriesVisibility, state entity.SeriesPublishState) *entity.Series {
		return &entity.Series{
			OrganizerID:  &organizerID,
			Visibility:   &visibility,
			PublishState: &state,
		}
	}

	tests := []struct {
		name          string
		series        *entity.Series
		wantEventPage bool
		wantVisible   bool
	}{
		{
			// @spec components/entity/series "Published public series"
			name:          "return true for a PUBLISHED PUBLIC first-party series",
			series:        firstParty(entity.SeriesVisibilityPublic, entity.SeriesPublishStatePublished),
			wantEventPage: true,
			wantVisible:   true,
		},
		{
			// @spec components/entity/series "Cancelled public series"
			name:          "return true for a CANCELLED PUBLIC first-party series that is not publicly visible",
			series:        firstParty(entity.SeriesVisibilityPublic, entity.SeriesPublishStateCancelled),
			wantEventPage: true,
			wantVisible:   false,
		},
		{
			// @spec components/entity/series "Unlisted series"
			name:          "return false for a PUBLISHED UNLISTED first-party series",
			series:        firstParty(entity.SeriesVisibilityUnlisted, entity.SeriesPublishStatePublished),
			wantEventPage: false,
			wantVisible:   false,
		},
		{
			// @spec components/entity/series "Draft series"
			name:          "return false for a DRAFT first-party series",
			series:        firstParty(entity.SeriesVisibilityPublic, entity.SeriesPublishStateDraft),
			wantEventPage: false,
			wantVisible:   false,
		},
		{
			// @spec components/entity/series "Discovered series"
			name:          "return false for a series with no organizer",
			series:        &entity.Series{Title: "Discovered Tour"},
			wantEventPage: false,
			wantVisible:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.wantEventPage, tt.series.HasEventPage())
			assert.Equal(t, tt.wantVisible, tt.series.IsPubliclyVisible())
		})
	}
}
