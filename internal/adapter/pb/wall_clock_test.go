package pb_test

import (
	"testing"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/datetime"
	"google.golang.org/genproto/googleapis/type/timeofday"

	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
)

func TestTimeOfDayFromProto(t *testing.T) {
	t.Parallel()

	t.Run("an absent time of day stays absent", func(t *testing.T) {
		t.Parallel()
		got, err := pb.TimeOfDayFromProto(nil)

		require.NoError(t, err)
		assert.Nil(t, got)
		assert.Nil(t, pb.TimeOfDayToProto(nil))
	})

	t.Run("hours and minutes round-trip", func(t *testing.T) {
		t.Parallel()
		got, err := pb.TimeOfDayFromProto(&timeofday.TimeOfDay{Hours: 18, Minutes: 30})

		require.NoError(t, err)
		assert.Equal(t, &entity.TimeOfDay{Hour: 18, Minute: 30}, got)
		assert.Equal(t, &timeofday.TimeOfDay{Hours: 18, Minutes: 30}, pb.TimeOfDayToProto(got))
	})

	for name, p := range map[string]*timeofday.TimeOfDay{
		"seconds are refused":  {Hours: 18, Seconds: 1},
		"nanos are refused":    {Hours: 18, Nanos: 1},
		"24:00 is refused":     {Hours: 24},
		"minute 60 is refused": {Hours: 1, Minutes: 60},
		"negative is refused":  {Hours: -1},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := pb.TimeOfDayFromProto(p)

			assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		})
	}
}

func TestSaleTimeFromProtoRefusesOutOfRange(t *testing.T) {
	t.Parallel()
	for _, c := range [][2]int32{{12, 60}, {12, -1}, {-1, 60}, {24, 0}} {
		p := &datetime.DateTime{
			Year: 2027, Month: 1, Day: 10, Hours: c[0], Minutes: c[1],
			TimeOffset: &datetime.DateTime_TimeZone{TimeZone: &datetime.TimeZone{Id: "Asia/Tokyo"}},
		}
		_, _, err := pb.SaleTimeFromProto(p)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument, "%d:%d", c[0], c[1])
	}
}
