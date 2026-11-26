package pb

import (
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"google.golang.org/genproto/googleapis/type/datetime"
	"google.golang.org/genproto/googleapis/type/timeofday"

	"github.com/liverty-music/backend/internal/entity"
)

// TimeOfDayToProto maps an optional time of day to google.type.TimeOfDay; nil
// stays nil.
func TimeOfDayToProto(t *entity.TimeOfDay) *timeofday.TimeOfDay {
	if t == nil {
		return nil
	}
	return &timeofday.TimeOfDay{Hours: int32(t.Hour), Minutes: int32(t.Minute)}
}

// TimeOfDayFromProto maps google.type.TimeOfDay to a time of day in whole
// minutes; nil stays nil. It fails with InvalidArgument for 24:00, seconds or
// a fraction of a second, which the schema's rule refuses first.
func TimeOfDayFromProto(p *timeofday.TimeOfDay) (*entity.TimeOfDay, error) {
	if p == nil {
		return nil, nil
	}
	if p.GetSeconds() != 0 || p.GetNanos() != 0 {
		return nil, apperr.New(codes.InvalidArgument, "a time of day must be in whole minutes")
	}
	t, err := entity.NewTimeOfDay(int(p.GetHours()), int(p.GetMinutes()))
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// SaleTimeToProto maps the instant of a sale's start or end to the
// google.type.DateTime of its wall-clock time in loc, with loc's IANA name as
// its time zone.
func SaleTimeToProto(instant time.Time, loc *time.Location) *datetime.DateTime {
	if loc == nil {
		loc = time.UTC
	}
	l := instant.In(loc)
	return &datetime.DateTime{
		Year:       int32(l.Year()),
		Month:      int32(l.Month()),
		Day:        int32(l.Day()),
		Hours:      int32(l.Hour()),
		Minutes:    int32(l.Minute()),
		TimeOffset: &datetime.DateTime_TimeZone{TimeZone: &datetime.TimeZone{Id: loc.String()}},
	}
}

// SaleTimeFromProto reads a sale's wall-clock time and its IANA time zone from
// google.type.DateTime and returns the instant. It fails with InvalidArgument
// when the time carries no time zone or a UTC offset in its place, is not a
// date and a time in whole minutes (a day that does not exist in its month,
// 30 February, is refused), or does not exist in the zone because the clocks
// skip it.
func SaleTimeFromProto(p *datetime.DateTime) (instant time.Time, zone string, err error) {
	if p == nil {
		return time.Time{}, "", apperr.New(codes.InvalidArgument, "a sale time is required")
	}
	zone = p.GetTimeZone().GetId()
	if zone == "" {
		return time.Time{}, "", apperr.New(codes.InvalidArgument, "a sale time must name its IANA time zone, not a UTC offset")
	}
	loc, err := entity.LoadZone(zone)
	if err != nil {
		return time.Time{}, "", err
	}
	if p.GetSeconds() != 0 || p.GetNanos() != 0 {
		return time.Time{}, "", apperr.New(codes.InvalidArgument, "a sale time must be in whole minutes")
	}
	// time.Date would carry an hour or minute out of range into another time.
	tod, err := entity.NewTimeOfDay(int(p.GetHours()), int(p.GetMinutes()))
	if err != nil {
		return time.Time{}, "", err
	}
	civil := time.Date(int(p.GetYear()), time.Month(p.GetMonth()), int(p.GetDay()), tod.Hour, tod.Minute, 0, 0, time.UTC)
	if civil.Year() != int(p.GetYear()) || civil.Month() != time.Month(p.GetMonth()) || civil.Day() != int(p.GetDay()) {
		return time.Time{}, "", apperr.New(codes.InvalidArgument, "a sale time must be a date that exists")
	}
	instant, err = entity.LocalDateTimeInstant(civil, loc)
	if err != nil {
		return time.Time{}, "", err
	}
	return instant, zone, nil
}
