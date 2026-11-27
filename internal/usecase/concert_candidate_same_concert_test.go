package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// approvalAt wires a pending candidate of the Artist listed as listed, on
// date at start, whose name resolves to the stored venue-1, and the events
// the two lookups of Approve find: those at venue-1 on date and the Artist's
// events on date.
func approvalAt(d *approvalTestDeps, listed string, date time.Time, start *entity.TimeOfDay, venueEvents, artistEvents []*entity.Concert) *entity.ConcertCandidate {
	d.venueRepo.EXPECT().GetByListedName(mock.Anything, listed, "JP-13").
		Return(&entity.Venue{ID: "venue-1", Name: listed, ListedVenueName: &listed}, nil).Once()
	d.concertRepo.EXPECT().FindEventsByVenueAndDate(mock.Anything, []string{"venue-1"}, []time.Time{date}).Return(venueEvents, nil).Once()
	d.findsArtistEvents(artistEvents...)
	sc := &entity.ConcertCandidate{
		ID: "candidate-1", ArtistID: d.artist.ID, SeriesID: "series-candidate", Title: "Candidate Title",
		LocalDate: date, StartTime: start, ListedVenueName: listed, AdminArea: new("JP-13"),
	}
	d.pending(sc)
	return sc
}

// showsConflict expects the conflict preview to read the existing event's
// Series and the candidate's Artist.
func (d *approvalTestDeps) showsConflict(seriesID string) {
	d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(&entity.Series{ID: seriesID, Title: "Existing"}, nil).Once()
	d.artistRepo.EXPECT().Get(mock.Anything, d.artist.ID).Return(d.artist, nil).Once()
}

func approveWith(d *approvalTestDeps, sc *entity.ConcertCandidate, resolution usecase.ApproveResolution) (*usecase.ApproveResult, error) {
	return d.uc.Approve(context.Background(), sc.ID, resolution, nil, nil, "reviewer-1")
}

func TestConcertCandidateUseCase_Approve_Collision(t *testing.T) {
	t.Parallel()

	artist := &entity.Artist{ID: "artist-a", Name: "A", MBID: "11111111-1111-1111-1111-111111111111"}
	aug1 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	at18 := todAt(18, 0)

	t.Run("Same slot", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert-candidate/approve "Same slot"
		d := newApprovalTestDeps(t, artist)
		existing := concertAt(entity.Event{ID: "event-b", SeriesID: "series-b", VenueID: "venue-1", LocalDate: aug1, StartTime: at18, ListedVenueName: new("Hall")}, "artist-b")
		sc := approvalAt(d, "Hall", aug1, at18, []*entity.Concert{existing}, nil)
		d.showsConflict("series-b")

		result, err := approveWith(d, sc, usecase.ApproveResolutionUnspecified)
		require.NoError(t, err)
		require.NotNil(t, result.Conflict)
		assert.Equal(t, "event-b", result.Conflict.Existing.EventID)
	})

	t.Run("Unknown start next to a known start", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert-candidate/approve "Unknown start next to a known start"
		d := newApprovalTestDeps(t, artist)
		existing := concertAt(entity.Event{ID: "event-b", SeriesID: "series-b", VenueID: "venue-1", LocalDate: aug1, StartTime: at18, ListedVenueName: new("Hall")}, "artist-b")
		sc := approvalAt(d, "Hall", aug1, nil, []*entity.Concert{existing}, nil)
		d.showsConflict("series-b")

		result, err := approveWith(d, sc, usecase.ApproveResolutionUnspecified)
		require.NoError(t, err)
		require.NotNil(t, result.Conflict)
		assert.Equal(t, "event-b", result.Conflict.Existing.EventID)
	})

	t.Run("Same artist at another venue", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert-candidate/approve "Same artist at another venue"
		aug28 := time.Date(2027, 8, 28, 0, 0, 0, 0, time.UTC)
		d := newApprovalTestDeps(t, artist)
		gaishi := concertAt(entity.Event{ID: "event-g", SeriesID: "series-g", VenueID: "venue-gaishi", LocalDate: aug28, StartTime: at18, ListedVenueName: new("日本ガイシホール")}, artist.ID)
		sc := approvalAt(d, "クロコくんホール", aug28, at18, nil, []*entity.Concert{gaishi})
		d.showsConflict("series-g")

		result, err := approveWith(d, sc, usecase.ApproveResolutionUnspecified)
		require.NoError(t, err)
		require.NotNil(t, result.Conflict)
		assert.Equal(t, "event-g", result.Conflict.Existing.EventID)
	})
}

func TestConcertCandidateUseCase_Approve_Resolutions(t *testing.T) {
	t.Parallel()

	artist := &entity.Artist{ID: "artist-a", Name: "A", MBID: "11111111-1111-1111-1111-111111111111"}
	aug1 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	at18 := todAt(18, 0)
	// own is the Artist's 18:00 Event at venue-1, listed under another name.
	own := func() *entity.Concert {
		return concertAt(entity.Event{ID: "event-a", SeriesID: "series-a", VenueID: "venue-1", LocalDate: aug1, StartTime: at18, ListedVenueName: new("Old Name")}, artist.ID)
	}

	t.Run("Admin keeps the existing event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert-candidate/approve "Admin keeps the existing event"
		d := newApprovalTestDeps(t, artist)
		sc := approvalAt(d, "New Name", aug1, at18, []*entity.Concert{own()}, []*entity.Concert{own()})
		d.logsRejection()
		d.concertRepo.EXPECT().AddOtherListedVenueName(mock.Anything, "event-a", "New Name").Return(nil).Once()
		d.deletes(sc.ID)

		// No UpdateEventListedVenueName, FillEventStartTimes or Create: the
		// Event keeps its name and times, and the Artist already performs.
		result, err := approveWith(d, sc, usecase.ApproveResolutionKeepExisting)
		require.NoError(t, err)
		assert.Nil(t, result.Conflict)
		require.Len(t, d.rejections, 1)
		assert.Equal(t, "duplicate of existing event event-a", d.rejections[0].Reason)
	})

	t.Run("Admin adopts the candidate's details", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert-candidate/approve "Admin adopts the candidate's details"
		d := newApprovalTestDeps(t, artist)
		unknownStart := concertAt(entity.Event{ID: "event-a", SeriesID: "series-a", VenueID: "venue-1", LocalDate: aug1, ListedVenueName: new("Old Name")}, artist.ID)
		sc := approvalAt(d, "New Name", aug1, nil, []*entity.Concert{unknownStart}, []*entity.Concert{unknownStart})
		sc.OpenTime = todAt(17, 0)
		d.concertRepo.EXPECT().UpdateEventListedVenueName(mock.Anything, "event-a", "New Name").Return(nil).Once()
		d.concertRepo.EXPECT().FillEventStartTimes(mock.Anything, []string{"event-a"}, []*entity.TimeOfDay{nil}, []*entity.TimeOfDay{todAt(17, 0)}).Return(nil).Once()
		d.deletes(sc.ID)

		result, err := approveWith(d, sc, usecase.ApproveResolutionAdoptCandidate)
		require.NoError(t, err)
		assert.Nil(t, result.Conflict)
	})

	t.Run("Known start time kept", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert-candidate/approve "Known start time kept"
		d := newApprovalTestDeps(t, artist)
		sc := approvalAt(d, "New Name", aug1, nil, []*entity.Concert{own()}, []*entity.Concert{own()})
		d.concertRepo.EXPECT().UpdateEventListedVenueName(mock.Anything, "event-a", "New Name").Return(nil).Once()
		// The fill supplies no start time, so the 18:00 start is kept.
		d.concertRepo.EXPECT().FillEventStartTimes(mock.Anything, []string{"event-a"}, []*entity.TimeOfDay{nil}, []*entity.TimeOfDay{nil}).Return(nil).Once()
		d.deletes(sc.ID)

		_, err := approveWith(d, sc, usecase.ApproveResolutionAdoptCandidate)
		require.NoError(t, err)
	})

	t.Run("Another artist's unknown-start event is not filled", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert-candidate/approve "Another artist's unknown-start event is not filled"
		d := newApprovalTestDeps(t, artist)
		other := concertAt(entity.Event{ID: "event-b", SeriesID: "series-b", VenueID: "venue-1", LocalDate: aug1, ListedVenueName: new("Hall")}, "artist-b")
		sc := approvalAt(d, "Hall", aug1, at18, []*entity.Concert{other}, nil)
		d.inserts()
		d.deletes(sc.ID)

		// No FillEventStartTimes expectation: the other Event keeps no start.
		_, err := approveWith(d, sc, usecase.ApproveResolutionUnspecified)
		require.NoError(t, err)
		require.Len(t, d.createdConcerts, 1)
		assert.Equal(t, at18, d.createdConcerts[0].StartTime)
		assert.Equal(t, []string{artist.ID}, d.createdConcerts[0].ArtistIDs())
		assert.Equal(t, "series-candidate", d.createdConcerts[0].SeriesID)
	})
}

func TestConcertCandidateUseCase_Approve_SharedBill(t *testing.T) {
	t.Parallel()

	sim := &entity.Artist{ID: "artist-sim", Name: "SiM", MBID: "22222222-2222-2222-2222-222222222222"}
	const horumon = "artist-horumon"
	aug1 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	at18 := todAt(18, 0)
	horumonEvent := func(state *entity.PublishState) *entity.Concert {
		return concertAt(entity.Event{ID: "event-h", SeriesID: "series-h", VenueID: "venue-1", LocalDate: aug1, StartTime: at18,
			ListedVenueName: new("Zepp Osaka Bayside"), PublishState: state}, horumon)
	}

	t.Run("Shared bill approved", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert-candidate/approve "Shared bill approved"
		d := newApprovalTestDeps(t, sim)
		sc := approvalAt(d, "ゼップ大阪ベイサイド", aug1, at18, []*entity.Concert{horumonEvent(nil)}, nil)
		sc.OpenTime = todAt(17, 0)
		d.logsRejection()
		d.concertRepo.EXPECT().AddOtherListedVenueName(mock.Anything, "event-h", "ゼップ大阪ベイサイド").Return(nil).Once()
		d.inserts()
		d.deletes(sc.ID)

		result, err := approveWith(d, sc, usecase.ApproveResolutionKeepExisting)
		require.NoError(t, err)
		assert.Nil(t, result.Conflict)
		// Concert.Create at the Event's slot adds SiM to its performers and
		// tells SiM's followers (CONCERT.created).
		require.Len(t, d.createdConcerts, 1)
		c := d.createdConcerts[0]
		assert.Equal(t, "venue-1", c.VenueID)
		assert.Equal(t, at18, c.StartTime)
		assert.Equal(t, "series-h", c.SeriesID)
		assert.Equal(t, []string{sim.ID}, c.ArtistIDs())
		assert.Nil(t, c.OpenTime, "keep existing leaves the Event's unknown open time unknown")
	})

	t.Run("Organizer's event kept as it is", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert-candidate/approve "Organizer's event kept as it is"
		d := newApprovalTestDeps(t, sim)
		sc := approvalAt(d, "ゼップ大阪ベイサイド", aug1, at18, []*entity.Concert{horumonEvent(published())}, nil)
		d.logsRejection()
		d.concertRepo.EXPECT().AddOtherListedVenueName(mock.Anything, "event-h", "ゼップ大阪ベイサイド").Return(nil).Once()
		d.deletes(sc.ID)

		// No Create or Merge expectation: the Event's performers are unchanged.
		result, err := approveWith(d, sc, usecase.ApproveResolutionKeepExisting)
		require.NoError(t, err)
		assert.Nil(t, result.Conflict)
	})

	t.Run("Artist's unknown-start event merged", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert-candidate/approve "Artist's unknown-start event merged"
		d := newApprovalTestDeps(t, sim)
		simUnknown := concertAt(entity.Event{ID: "event-s", SeriesID: "series-s", VenueID: "venue-1", LocalDate: aug1, ListedVenueName: new("ゼップ大阪ベイサイド")}, sim.ID)
		sc := approvalAt(d, "ゼップ大阪ベイサイド", aug1, at18, []*entity.Concert{simUnknown, horumonEvent(nil)}, []*entity.Concert{simUnknown})
		d.concertRepo.EXPECT().UpdateEventListedVenueName(mock.Anything, "event-h", "ゼップ大阪ベイサイド").Return(nil).Once()
		d.concertRepo.EXPECT().FillEventStartTimes(mock.Anything, []string{"event-h"}, mock.Anything, mock.Anything).Return(nil).Once()
		d.concertRepo.EXPECT().Merge(mock.Anything, "event-s", "event-h").Return(nil).Once()
		d.seriesRepo.EXPECT().DeleteOrphaned(mock.Anything, []string{"series-s"}).Return(1, nil).Once()
		d.deletes(sc.ID)

		// No Create expectation: nobody is told again.
		result, err := approveWith(d, sc, usecase.ApproveResolutionAdoptCandidate)
		require.NoError(t, err)
		assert.Nil(t, result.Conflict)
	})
}

func TestCreateFromDiscovered_AfterKeepExisting(t *testing.T) {
	t.Parallel()
	// @spec components/usecase/concert-candidate/approve "Same candidate found again"
	// After keep existing, the Event is also listed under the candidate's
	// name, so discovery finds the Artist's concert again under that name.
	dec19 := time.Date(2026, 12, 19, 0, 0, 0, 0, time.UTC)
	h := newCreationHarness(t)
	own := concertAt(entity.Event{ID: "event-a", SeriesID: "series-a", VenueID: "venue-1", LocalDate: dec19, StartTime: todAt(18, 0),
		ListedVenueName: new("Old Name"), OtherListedVenueNames: []string{"New Name"}}, "artist-a")
	h.findsArtistEvents("artist-a", own)
	h.venueHolds("venue-1", dec19, own)
	h.sweepsOrphans()

	require.NoError(t, h.uc.CreateFromDiscovered(context.Background(), discovered("artist-a", "Live", entity.SeriesTypeSingle, found("New Name", dec19, todAt(18, 0)))))

	assert.Empty(t, h.createdConcerts, "nothing is stored")
	assert.Empty(t, h.staged, "no ConcertCandidate is staged")
}
