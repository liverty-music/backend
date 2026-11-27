package usecase_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/stretchr/testify/assert"
)

// stored is a stored event for the decision table: id, venue, start ("" for
// unknown), listed name, other listed names, publish state ("" for none) and
// performers.
func stored(id, venue, start, listed string, others []string, state string, artists ...string) *entity.Concert {
	c := &entity.Concert{Event: entity.Event{ID: id, VenueID: venue, LocalDate: sameConcertDate, OtherListedVenueNames: others}}
	if start != "" {
		var h, m int
		if _, err := fmt.Sscanf(start, "%d:%d", &h, &m); err != nil {
			panic(err)
		}
		c.StartTime = todAt(h, m)
	}
	if listed != "" {
		c.ListedVenueName = &listed
	}
	if state != "" {
		s := entity.PublishState(state)
		c.PublishState = &s
	}
	for _, a := range artists {
		c.Artists = append(c.Artists, &entity.Artist{ID: a})
	}
	return c
}

var sameConcertDate = time.Date(2026, 12, 19, 0, 0, 0, 0, time.UTC)

// TestDecideFoundEvent has one row per branch of the decision (design D6 of
// recognize-same-discovered-concert).
func TestDecideFoundEvent(t *testing.T) {
	t.Parallel()

	const (
		a, b   = "artist-a", "artist-b"
		venue  = "venue-v"
		other  = "venue-w"
		kyoJa  = "京セラドーム大阪"
		kyoEn  = "Kyocera Dome Osaka"
		zeppHa = "Zepp Haneda(TOKYO)"
	)
	at17, at18, at13, at19 := todAt(17, 0), todAt(18, 0), todAt(13, 0), todAt(19, 0)
	found := func(name string, start *entity.TimeOfDay) usecase.FoundEvent {
		return usecase.FoundEvent{ArtistID: a, ListedVenueName: name, VenueID: venue, LocalDate: sameConcertDate, StartTime: start}
	}

	tests := []struct {
		name         string
		found        usecase.FoundEvent
		artistEvents []*entity.Concert
		venueEvents  []*entity.Concert
		claimed      map[string]bool
		wantOutcome  string
		wantEvent    string
		wantSource   string
	}{
		{
			name:        "own event, same start, listed under the name: nothing",
			found:       found(kyoEn, at17),
			venueEvents: []*entity.Concert{stored("e1", venue, "17:00", kyoJa, []string{kyoEn}, "", a)},
			wantOutcome: "nothing", wantEvent: "e1",
		},
		{
			name:        "own event, both unknown, listed under the name: nothing",
			found:       found(kyoJa, nil),
			venueEvents: []*entity.Concert{stored("e1", venue, "", kyoJa, nil, "", a)},
			wantOutcome: "nothing", wantEvent: "e1",
		},
		{
			name:        "unknown found start, own timed event listed under the name: nothing",
			found:       found(zeppHa, nil),
			venueEvents: []*entity.Concert{stored("e1", venue, "18:00", zeppHa, nil, "", a)},
			wantOutcome: "nothing", wantEvent: "e1",
		},
		{
			name:        "unknown found start, own timed event under another name: stage",
			found:       found(kyoEn, nil),
			venueEvents: []*entity.Concert{stored("e1", venue, "17:00", kyoJa, nil, "", a)},
			wantOutcome: "stage", wantEvent: "e1",
		},
		{
			name:        "own event, same start, under another name: stage",
			found:       found(kyoEn, at17),
			venueEvents: []*entity.Concert{stored("e1", venue, "17:00", kyoJa, nil, "", a)},
			wantOutcome: "stage", wantEvent: "e1",
		},
		{
			name:        "own unknown-start event under another name: fill",
			found:       found(kyoJa, at17),
			venueEvents: []*entity.Concert{stored("e1", venue, "", kyoEn, nil, "", a)},
			wantOutcome: "fill", wantEvent: "e1",
		},
		{
			name:  "slot held by another Artist's event under the same name: merge into it",
			found: found(zeppHa, at18),
			venueEvents: []*entity.Concert{
				stored("e1", venue, "", zeppHa, nil, "", a),
				stored("e2", venue, "18:00", "ZEPP HANEDA (TOKYO)", nil, "", b),
			},
			wantOutcome: "merge_into", wantEvent: "e2", wantSource: "e1",
		},
		{
			name:  "slot held by another Artist's event under another name: stage",
			found: found(zeppHa, at18),
			venueEvents: []*entity.Concert{
				stored("e1", venue, "", zeppHa, nil, "", a),
				stored("e2", venue, "18:00", "羽田", nil, "", b),
			},
			wantOutcome: "stage", wantEvent: "e2",
		},
		{
			name:  "slot held by an Organizer's event under the same name: stage",
			found: found(zeppHa, at18),
			venueEvents: []*entity.Concert{
				stored("e1", venue, "", zeppHa, nil, "", a),
				stored("e2", venue, "18:00", zeppHa, nil, "PUBLISHED", b),
			},
			wantOutcome: "stage", wantEvent: "e2",
		},
		{
			name:  "own unknown-start event already claimed: a new event",
			found: found(zeppHa, at18),
			venueEvents: []*entity.Concert{
				stored("e1", venue, "13:00", zeppHa, nil, "", a),
				stored("e3", venue, "", zeppHa, nil, "", a),
			},
			claimed:     map[string]bool{"e3": true},
			wantOutcome: "publish",
		},
		{
			name:         "same Artist at another venue at the same time: stage",
			found:        found("クロコくんホール (旧 日本ガイシホール)", at18),
			artistEvents: []*entity.Concert{stored("e9", other, "18:00", "日本ガイシホール", nil, "", a)},
			wantOutcome:  "stage", wantEvent: "e9",
		},
		{
			name:         "own unknown-start event, but the same Artist at another venue at the found time: stage, not fill",
			found:        found(zeppHa, at18),
			artistEvents: []*entity.Concert{stored("e9", other, "18:00", "Arena", nil, "", a)},
			venueEvents:  []*entity.Concert{stored("e1", venue, "", zeppHa, nil, "", a)},
			wantOutcome:  "stage", wantEvent: "e9",
		},
		{
			name:         "same Artist at another venue at the same time on another date: a new event",
			found:        found("Hall", at18),
			artistEvents: []*entity.Concert{{Event: entity.Event{ID: "e9", VenueID: other, LocalDate: sameConcertDate.AddDate(0, 0, 1), StartTime: at18}}},
			wantOutcome:  "publish",
		},
		{
			name:         "same Artist at another venue at another time: a new event",
			found:        found("Hall", at19),
			artistEvents: []*entity.Concert{stored("e9", other, "13:00", "Arena", nil, "", a)},
			wantOutcome:  "publish",
		},
		{
			name:        "another Artist's event, same known start, same name: add performer",
			found:       found("Zepp Haneda (TOKYO)", at18),
			venueEvents: []*entity.Concert{stored("e2", venue, "18:00", zeppHa, nil, "", b)},
			wantOutcome: "add_performer", wantEvent: "e2",
		},
		{
			name:        "an Organizer's event at the same slot and name: stage",
			found:       found(zeppHa, at18),
			venueEvents: []*entity.Concert{stored("e2", venue, "18:00", zeppHa, nil, "PUBLISHED", b)},
			wantOutcome: "stage", wantEvent: "e2",
		},
		{
			name:        "another Artist's event, same start, another name: stage",
			found:       found(zeppHa, at18),
			venueEvents: []*entity.Concert{stored("e2", venue, "18:00", "羽田", nil, "", b)},
			wantOutcome: "stage", wantEvent: "e2",
		},
		{
			name:        "another Artist's event, both unknown: stage",
			found:       found(zeppHa, nil),
			venueEvents: []*entity.Concert{stored("e2", venue, "", zeppHa, nil, "", b)},
			wantOutcome: "stage", wantEvent: "e2",
		},
		{
			name:        "unknown found start next to another Artist's timed event: stage",
			found:       found(zeppHa, nil),
			venueEvents: []*entity.Concert{stored("e2", venue, "18:00", zeppHa, nil, "", b)},
			wantOutcome: "stage", wantEvent: "e2",
		},
		{
			name:        "known found start next to another Artist's unknown-start event: publish",
			found:       found(zeppHa, at18),
			venueEvents: []*entity.Concert{stored("e2", venue, "", zeppHa, nil, "", b)},
			wantOutcome: "publish",
		},
		{
			name:        "nothing at the venue and date: publish",
			found:       found(zeppHa, at13),
			wantOutcome: "publish",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			claimed := tt.claimed
			if claimed == nil {
				claimed = map[string]bool{}
			}
			outcome, eventID, sourceID := usecase.ExportedDecideFoundEvent(tt.found, tt.artistEvents, tt.venueEvents, claimed)
			assert.Equal(t, tt.wantOutcome, outcome)
			assert.Equal(t, tt.wantEvent, eventID)
			assert.Equal(t, tt.wantSource, sourceID)
		})
	}
}
