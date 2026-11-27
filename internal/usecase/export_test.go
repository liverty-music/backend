package usecase

import (
	"time"

	"github.com/liverty-music/backend/internal/entity"
)

// ExportedScheduledFireTime exposes scheduledFireTime for black-box tests.
var ExportedScheduledFireTime = scheduledFireTime

// ExportedRecordPasskeyChangeID exposes recordPasskeyChangeID for black-box tests.
var ExportedRecordPasskeyChangeID = recordPasskeyChangeID

// ExportedDiscoveryWorkflowID exposes discoveryWorkflowID for black-box tests.
var ExportedDiscoveryWorkflowID = discoveryWorkflowID

// ExportedReminderWorkflowID exposes reminderWorkflowID for black-box tests.
var ExportedReminderWorkflowID = reminderWorkflowID

// ExportedUserTimezone exposes userTimezone for black-box tests.
var ExportedUserTimezone = userTimezone

// ReminderScanLookbackMargin exposes reminderScanLookbackMargin for black-box tests.
const ReminderScanLookbackMargin = reminderScanLookbackMargin

// ExportedOfficialSiteRefreshBatchSize exposes officialSiteRefreshBatchSize for black-box tests.
var ExportedOfficialSiteRefreshBatchSize = officialSiteRefreshBatchSize

// RunSignInLinksSynchronously makes the OrganizerUseCase run the background
// lookup and send of SendSignInLinks before SendSignInLinks returns.
func RunSignInLinksSynchronously(uc OrganizerUseCase) {
	RunSignInLinksWith(uc, func(task func()) { task() })
}

// RunSignInLinksWith makes the OrganizerUseCase hand the background lookup
// and send of SendSignInLinks to run.
func RunSignInLinksWith(uc OrganizerUseCase, run func(task func())) {
	uc.(*organizerUseCase).background = run
}

// RunPasskeyRecoverySynchronously makes the UserUseCase run the background
// lookup and announcement of RequestPasskeyRecovery before it returns.
func RunPasskeyRecoverySynchronously(uc UserUseCase) {
	RunPasskeyRecoveryWith(uc, func(task func()) { task() })
}

// RunPasskeyRecoveryWith makes the UserUseCase hand the background lookup and
// announcement of RequestPasskeyRecovery to run.
func RunPasskeyRecoveryWith(uc UserUseCase, run func(task func())) {
	uc.(*userUseCase).background = run
}

// FoundEvent is a found event for ExportedDecideFoundEvent.
type FoundEvent struct {
	ArtistID        string
	ListedVenueName string
	VenueID         string
	LocalDate       time.Time
	StartTime       *entity.TimeOfDay
}

// ExportedDecideFoundEvent exposes decideFoundEvent for black-box tests. It
// returns the outcome's name, the id of the event it is about and, for a
// merge, the id of the event merged away.
func ExportedDecideFoundEvent(found FoundEvent, artistEvents, venueEvents []*entity.Concert, claimed map[string]bool) (outcome, eventID, sourceID string) {
	d := decideFoundEvent(foundEvent{
		artistID: found.ArtistID, listedVenueName: found.ListedVenueName, venueID: found.VenueID,
		localDate: found.LocalDate, startTime: found.StartTime,
	}, artistEvents, venueEvents, claimed)
	if d.event != nil {
		eventID = d.event.ID
	}
	if d.source != nil {
		sourceID = d.source.ID
	}
	return d.outcome.String(), eventID, sourceID
}
