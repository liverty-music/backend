package entity

import (
	"fmt"
	"strings"
	"time"
	// Embed the IANA zone database so every binary that computes an instant
	// from a wall-clock time has it; distroless images carry none.
	_ "time/tzdata"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// TimeOfDay is a time of day in whole minutes, as a performance's start and
// door times are announced: 19:00, with no date and no zone.
type TimeOfDay struct {
	Hour   int
	Minute int
}

// NewTimeOfDay returns the time of day at hour and minute. It fails with
// InvalidArgument when hour is not 0 to 23 or minute is not 0 to 59.
func NewTimeOfDay(hour, minute int) (TimeOfDay, error) {
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return TimeOfDay{}, apperr.New(codes.InvalidArgument, fmt.Sprintf("%02d:%02d is not a time of day", hour, minute))
	}
	return TimeOfDay{Hour: hour, Minute: minute}, nil
}

// MarshalText implements encoding.TextMarshaler: HH:MM, the form a workflow's
// JSON input carries.
func (t TimeOfDay) MarshalText() ([]byte, error) { return []byte(t.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler for HH:MM on a 24-hour
// clock. Anything else, such as "7:00 PM" or a time with seconds, fails with
// InvalidArgument.
func (t *TimeOfDay) UnmarshalText(text []byte) error {
	parsed, err := time.Parse("15:04", string(text))
	if err != nil {
		return apperr.New(codes.InvalidArgument, fmt.Sprintf("%q is not a time of day", text))
	}
	*t = TimeOfDayOf(parsed)
	return nil
}

// SameTimeOfDay reports whether two optional times of day are equal, where two
// unknown (nil) ones are equal and an unknown one equals no known one.
func SameTimeOfDay(a, b *TimeOfDay) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// TimeOfDayKey returns the canonical dedup key of an optional time of day under
// NULLS NOT DISTINCT semantics: the empty string for an unknown one, HH:MM
// otherwise. Two events share a start time iff their keys are equal.
func TimeOfDayKey(t *TimeOfDay) string {
	if t == nil {
		return ""
	}
	return t.String()
}

// Before reports whether t is earlier in the day than u.
func (t TimeOfDay) Before(u TimeOfDay) bool {
	return t.Hour < u.Hour || (t.Hour == u.Hour && t.Minute < u.Minute)
}

// TimeOfDayOf returns the time of day on t's own clock, cut to the minute.
func TimeOfDayOf(t time.Time) TimeOfDay {
	return TimeOfDay{Hour: t.Hour(), Minute: t.Minute()}
}

// String returns the time of day as HH:MM.
func (t TimeOfDay) String() string {
	return fmt.Sprintf("%02d:%02d", t.Hour, t.Minute)
}

// LoadZone returns the time zone named name from the binary's embedded zone
// data. It fails with InvalidArgument when name is empty or not an IANA
// Area/Location name: an abbreviation such as EST, "UTC" or "Local" is not a
// zone a page or a Venue can have.
func LoadZone(name string) (*time.Location, error) {
	if name == "" {
		return nil, apperr.New(codes.InvalidArgument, "a time zone is required")
	}
	loc, err := time.LoadLocation(name)
	if err != nil || !strings.Contains(name, "/") {
		return nil, apperr.New(codes.InvalidArgument, fmt.Sprintf("%q is not an IANA time zone", name))
	}
	return loc, nil
}

// LocalInstant returns the instant of the wall-clock time of day tod on the
// calendar day of date (its year, month and day, whatever its location), read
// in loc. A wall-clock time that does not exist in loc, because the clocks
// skip it when they go forward, fails with InvalidArgument; one that occurs
// twice, because the clocks go back, is the earlier instant. time.Date leaves
// both to the implementation, so this decides them.
func LocalInstant(date time.Time, tod TimeOfDay, loc *time.Location) (time.Time, error) {
	return LocalDateTimeInstant(time.Date(date.Year(), date.Month(), date.Day(), tod.Hour, tod.Minute, 0, 0, time.UTC), loc)
}

// LocalDateTimeInstant returns the instant of a wall-clock date and time given
// as the fields of civil, which carries no zone of its own (its location is
// ignored), read in loc, with the rules of [LocalInstant].
func LocalDateTimeInstant(civil time.Time, loc *time.Location) (time.Time, error) {
	wall := time.Date(civil.Year(), civil.Month(), civil.Day(), civil.Hour(), civil.Minute(), civil.Second(), civil.Nanosecond(), time.UTC)
	// The offsets in force a day before and a day after bound every offset the
	// wall-clock time can have: a zone changes its offset at most once a day.
	var found []time.Time
	seen := map[int]bool{}
	for _, probe := range []time.Time{wall.Add(-24 * time.Hour), wall.Add(24 * time.Hour)} {
		_, off := probe.In(loc).Zone()
		if seen[off] {
			continue
		}
		seen[off] = true
		instant := wall.Add(-time.Duration(off) * time.Second)
		if local := instant.In(loc); sameWall(local, wall) {
			found = append(found, instant)
		}
	}
	switch len(found) {
	case 0:
		return time.Time{}, apperr.New(codes.InvalidArgument,
			fmt.Sprintf("%s does not exist in %s: the clocks skip it", wall.Format("2006-01-02 15:04"), loc))
	case 1:
		return found[0], nil
	default:
		if found[1].Before(found[0]) {
			return found[1], nil
		}
		return found[0], nil
	}
}

func sameWall(local, wall time.Time) bool {
	return local.Year() == wall.Year() && local.Month() == wall.Month() && local.Day() == wall.Day() &&
		local.Hour() == wall.Hour() && local.Minute() == wall.Minute() && local.Second() == wall.Second() &&
		local.Nanosecond() == wall.Nanosecond()
}

// LocalDateIn returns the calendar day that t falls on in loc, as midnight
// UTC like an Event's local date.
func LocalDateIn(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// CivilTime returns the wall-clock fields of t in loc as a time at that clock
// in UTC, the form a TIMESTAMP column without a zone reads back.
func CivilTime(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), l.Minute(), l.Second(), l.Nanosecond(), time.UTC)
}
