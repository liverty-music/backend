package usecase

import (
	"context"

	"github.com/liverty-music/backend/internal/entity"
)

// ExportedProfileLogoColor exposes profileLogoColor for black-box tests.
var ExportedProfileLogoColor = func(uc ArtistImageSyncUseCase, ctx context.Context, fanart *entity.Fanart, artistID string) {
	uc.(*artistImageSyncUseCase).profileLogoColor(ctx, fanart, artistID)
}

// ExportedScheduledFireTime exposes scheduledFireTime for black-box tests.
var ExportedScheduledFireTime = scheduledFireTime

// ExportedBuildReminderPayload exposes buildReminderPayload for black-box tests.
var ExportedBuildReminderPayload = buildReminderPayload

// ExportedBuildAnnouncementPayload exposes buildAnnouncementPayload for black-box tests.
var ExportedBuildAnnouncementPayload = buildAnnouncementPayload

// ExportedUserTimezone exposes userTimezone for black-box tests.
var ExportedUserTimezone = userTimezone

// ReminderScanLookbackMargin exposes reminderScanLookbackMargin for black-box tests.
const ReminderScanLookbackMargin = reminderScanLookbackMargin

// ExportedOfficialSiteRefreshBatchSize exposes officialSiteRefreshBatchSize for black-box tests.
var ExportedOfficialSiteRefreshBatchSize = officialSiteRefreshBatchSize
