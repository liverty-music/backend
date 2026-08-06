package usecase

import (
	"context"
	"encoding/json"

	"github.com/liverty-music/backend/internal/entity"
)

// ExportedBuildNewTicketEmail exposes buildNewTicketEmail for black-box tests.
var ExportedBuildNewTicketEmail = func(uc TicketEmailUseCase, userID string, emailType entity.TicketEmailType, rawBody string, parsedJSON json.RawMessage, parsed *entity.ParsedEmailData) *entity.NewTicketEmail {
	return uc.(*ticketEmailUseCase).buildNewTicketEmail(userID, emailType, rawBody, parsedJSON, parsed)
}

// ExportedDetermineJourneyStatus exposes determineJourneyStatus for black-box tests.
var ExportedDetermineJourneyStatus = func(uc TicketEmailUseCase, te *entity.TicketEmail) entity.TicketJourneyStatus {
	return uc.(*ticketEmailUseCase).determineJourneyStatus(te)
}

// ExportedProfileLogoColor exposes profileLogoColor for black-box tests.
var ExportedProfileLogoColor = func(uc ArtistImageSyncUseCase, ctx context.Context, fanart *entity.Fanart, artistID string) {
	uc.(*artistImageSyncUseCase).profileLogoColor(ctx, fanart, artistID)
}

// ExportedValidMerchURL exposes validMerchURL for black-box tests.
var ExportedValidMerchURL = validMerchURL

// ExportedScheduledFireTime exposes scheduledFireTime for black-box tests.
var ExportedScheduledFireTime = scheduledFireTime

// ExportedBuildReminderPayload exposes buildReminderPayload for black-box tests.
var ExportedBuildReminderPayload = buildReminderPayload

// ExportedChannelDisplayName exposes channelDisplayName for black-box tests.
var ExportedChannelDisplayName = channelDisplayName

// ReminderScanLookbackMargin exposes reminderScanLookbackMargin for black-box tests.
const ReminderScanLookbackMargin = reminderScanLookbackMargin
