package rdb

import (
	"fmt"
	"time"

	"github.com/liverty-music/backend/internal/entity"
)

// A TIME column is written and read as text in HH:MM form, so the driver needs
// no codec for it: queries cast `$n::text[]` to time and select
// `col::text`.

// timeOfDayText returns the HH:MM text of an optional time of day; nil when it
// is unknown.
func timeOfDayText(t *entity.TimeOfDay) *string {
	if t == nil {
		return nil
	}
	s := t.String()
	return &s
}

// timeOfDayTexts returns the HH:MM text of each optional time of day.
func timeOfDayTexts(ts []*entity.TimeOfDay) []*string {
	out := make([]*string, len(ts))
	for i, t := range ts {
		out[i] = timeOfDayText(t)
	}
	return out
}

// timeOfDayFromText reads the time of day of a TIME column selected as text
// (HH:MM:SS, or HH:MM); nil when the column is NULL or not a time of day.
func timeOfDayFromText(s *string) *entity.TimeOfDay {
	if s == nil {
		return nil
	}
	var h, m int
	if _, err := fmt.Sscanf(*s, "%d:%d", &h, &m); err != nil {
		return nil
	}
	t, err := entity.NewTimeOfDay(h, m)
	if err != nil {
		return nil
	}
	return &t
}

// eventStartInstant returns the instant an event starts and the time zone of
// its Venue, from its local date, its start time as selected text and the zone
// name; the instant is nil when the start is unknown or does not exist in the
// zone, and the zone is nil when its name is not an IANA name.
func eventStartInstant(localDate time.Time, startText *string, zone string) (*time.Time, *time.Location) {
	loc, err := entity.LoadZone(zone)
	if err != nil {
		return nil, nil
	}
	tod := timeOfDayFromText(startText)
	if tod == nil {
		return nil, loc
	}
	instant, err := entity.LocalInstant(localDate, *tod, loc)
	if err != nil {
		return nil, loc
	}
	return &instant, loc
}

// saleInstant returns the instant of a stored wall-clock sale time in loc. A
// time the zone's rules have since made nonexistent (its clocks now skip it)
// moves forward to the first minute that exists.
func saleInstant(civil time.Time, loc *time.Location) (time.Time, error) {
	for i := range 6 {
		instant, err := entity.LocalDateTimeInstant(civil.Add(time.Duration(i)*30*time.Minute), loc)
		if err == nil {
			return instant, nil
		}
	}
	return time.Time{}, fmt.Errorf("sale time %s does not exist in %s", civil.Format("2006-01-02 15:04"), loc)
}
