package usecase

import (
	"fmt"
	"strings"
	"time"

	"github.com/liverty-music/backend/internal/entity"
)

// Copy keys of the sales-phase notifications. An announcement is keyed by the
// phase's method alone; a reminder by its stage and method.
const (
	copyAnnounceFirstCome    = "announce_first_come"
	copyAnnounceLottery      = "announce_lottery"
	copyApplyOpenFirstCome   = "apply_open_first_come"
	copyApplyOpenLottery     = "apply_open_lottery"
	copyApplyClose24HLottery = "apply_close_24h_lottery"
	copyResultDayLottery     = "result_day_lottery"
)

// salesPhaseCopy is one notification's title and sentence. The sentence holds
// one %s for a formatted time ({start}, {end} or {result}).
type salesPhaseCopy struct {
	title    string
	sentence string
}

// salesPhaseCopies holds the fixed copy by language and key.
var salesPhaseCopies = map[string]map[string]salesPhaseCopy{
	"ja": {
		copyAnnounceFirstCome:    {"チケット先着販売のお知らせ", "%sからチケットの先着販売スタート!!"},
		copyAnnounceLottery:      {"チケット抽選受付のお知らせ", "%sからチケットの抽選申し込みスタート!!"},
		copyApplyOpenFirstCome:   {"まもなく先着販売開始", "%sからチケットの先着販売スタート!!"},
		copyApplyOpenLottery:     {"チケット申し込み受付開始", "チケットの抽選申し込みがスタートしました!! 締切は%s"},
		copyApplyClose24HLottery: {"抽選の申し込み締切が近づいています", "チケットの抽選申し込み締切は%s!!"},
		copyResultDayLottery:     {"本日 抽選結果発表", "本日%sにチケットの抽選結果発表!!"},
	},
	"en": {
		copyAnnounceFirstCome:    {"First-Come Ticket Sale", "Ticket sale (first come) starts %s!"},
		copyAnnounceLottery:      {"Ticket Lottery", "Ticket lottery entry starts %s!"},
		copyApplyOpenFirstCome:   {"First-Come Sale Starting Soon", "Ticket sale (first come) starts %s!"},
		copyApplyOpenLottery:     {"Ticket Lottery Open", "Ticket lottery entry is open! Closes %s."},
		copyApplyClose24HLottery: {"Ticket Lottery Closing Soon", "Ticket lottery entry closes %s!"},
		copyResultDayLottery:     {"Lottery Results Today", "Ticket lottery results are out today at %s!"},
	},
}

// weekdaysJA are the Japanese weekday abbreviations, indexed by time.Weekday.
var weekdaysJA = [7]string{"日", "月", "火", "水", "木", "金", "土"}

// copyLanguage returns "ja" for Japanese and "en" for any other or no language.
func copyLanguage(preferred string) string {
	if preferred == "ja" {
		return "ja"
	}
	return "en"
}

// formatSalesTime formats t in tz for the notification text:
// "10/22(木) 23:59" in Japanese and "Oct 22 (Thu) 23:59" in English.
func formatSalesTime(t time.Time, tz *time.Location, lang string) string {
	local := t.In(tz)
	if lang == "ja" {
		return fmt.Sprintf("%d/%d(%s) %s", int(local.Month()), local.Day(), weekdaysJA[local.Weekday()], local.Format("15:04"))
	}
	return local.Format("Jan 2 (Mon) 15:04")
}

// salesPhaseText renders a notification's title and text: the sentence with
// the formatted time, then a line break and the series title.
func salesPhaseText(lang, key, timeStr, seriesTitle string) (title, text string) {
	c := salesPhaseCopies[lang][key]
	return c.title, fmt.Sprintf(c.sentence, timeStr) + "\n" + seriesTitle
}

// concertLinkURL is the deep link that opens an event's detail sheet.
func concertLinkURL(eventID string) string {
	return "/concerts/" + eventID
}

// timeZoneOf parses an IANA time zone name. It never returns nil: an empty or
// unrecognised name falls back to Asia/Tokyo, then UTC when tzdata is missing.
func timeZoneOf(name string) *time.Location {
	if strings.TrimSpace(name) != "" {
		if tz, err := time.LoadLocation(name); err == nil {
			return tz
		}
	}
	if tz, err := time.LoadLocation(fallbackTimeZone); err == nil {
		return tz
	}
	return time.UTC
}

// buildAnnouncementPayload builds a recipient's announcement of a newly
// discovered phase, in the recipient's language and time zone, linking to the
// recipient's tracked event.
func buildAnnouncementPayload(
	phase *entity.SalesPhase,
	user *entity.User,
	seriesTitle string,
	linkEventID string,
) *entity.NotificationPayload {
	lang := copyLanguage(user.PreferredLanguage)
	key := copyAnnounceLottery
	if phase.Method == entity.SalesMethodFirstCome {
		key = copyAnnounceFirstCome
	}
	title, text := salesPhaseText(lang, key, formatSalesTime(phase.ApplyStartTime, timeZoneOf(user.TimeZone), lang), seriesTitle)
	return entity.NewNotificationPayload(title, text, concertLinkURL(linkEventID), fmt.Sprintf("sales-phase-%s", phase.ID))
}
