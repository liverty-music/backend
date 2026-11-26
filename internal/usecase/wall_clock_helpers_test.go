package usecase_test

import (
	"time"

	"github.com/liverty-music/backend/internal/entity"
)

// tod returns the wall-clock time of day t reads on its own clock, as the
// announced start or door time of an event at a Venue in t's zone.
func tod(t time.Time) *entity.TimeOfDay {
	v := entity.TimeOfDayOf(t)
	return &v
}

// todAt returns the time of day at hour and minute.
func todAt(hour, minute int) *entity.TimeOfDay {
	return &entity.TimeOfDay{Hour: hour, Minute: minute}
}

// tokyoVenue returns a Venue in Asia/Tokyo carrying only what an event read
// back from the repository carries: its ID and time zone.
func tokyoVenue() *entity.Venue {
	return &entity.Venue{TimeZone: new("Asia/Tokyo")}
}

// eventStartIn returns the start of an event at a Venue in the time zone zone
// as the repository reads it: the wall-clock time and local date of the instant
// start in zone, or no start time when start is nil.
func eventStartIn(zone string, start *time.Time) *entity.EventStart {
	loc, err := entity.LoadZone(zone)
	if err != nil {
		panic(err)
	}
	s := &entity.EventStart{TimeZone: zone}
	if start != nil {
		s.LocalDate = entity.LocalDateIn(*start, loc)
		s.StartTime = tod(start.In(loc))
	}
	return s
}

// tokyoEventStart is [eventStartIn] for a Venue in Asia/Tokyo.
func tokyoEventStart(start *time.Time) *entity.EventStart {
	return eventStartIn("Asia/Tokyo", start)
}
