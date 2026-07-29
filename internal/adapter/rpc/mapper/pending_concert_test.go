package mapper_test

import (
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestPendingConcertToProto(t *testing.T) {
	t.Parallel()

	discoveredAt := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	localDate := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	startTime := time.Date(2026, 7, 15, 19, 0, 0, 0, time.UTC)
	openTime := time.Date(2026, 7, 15, 18, 0, 0, 0, time.UTC)

	performer := &entity.Artist{ID: "artist-1", Name: "Test Band"}

	baseConcert := func() *entity.StagedConcert {
		return &entity.StagedConcert{
			ID:              "staged-1",
			Title:           "Summer Tour 2026",
			LocalDate:       localDate,
			ListedVenueName: "Nippon Budokan",
			DiscoveredTime:  discoveredAt,
		}
	}

	t.Run("nil returns nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, mapper.PendingConcertToProto(nil, performer))
	})

	t.Run("open_time present maps to proto OpenTime", func(t *testing.T) {
		t.Parallel()
		sc := baseConcert()
		sc.OpenTime = &openTime

		got := mapper.PendingConcertToProto(sc, performer)
		require.NotNil(t, got)
		require.NotNil(t, got.OpenTime, "expected OpenTime to be set")
		assert.Equal(t, timestamppb.New(openTime), got.OpenTime.Value)
	})

	t.Run("open_time absent leaves proto OpenTime nil", func(t *testing.T) {
		t.Parallel()
		sc := baseConcert()
		sc.OpenTime = nil

		got := mapper.PendingConcertToProto(sc, performer)
		require.NotNil(t, got)
		assert.Nil(t, got.OpenTime, "expected OpenTime to be nil when source has no open time")
	})

	t.Run("start_time present maps independently of open_time", func(t *testing.T) {
		t.Parallel()
		sc := baseConcert()
		sc.StartTime = &startTime
		sc.OpenTime = &openTime

		got := mapper.PendingConcertToProto(sc, performer)
		require.NotNil(t, got)
		require.NotNil(t, got.StartTime)
		require.NotNil(t, got.OpenTime)
		assert.Equal(t, timestamppb.New(startTime), got.StartTime.Value)
		assert.Equal(t, timestamppb.New(openTime), got.OpenTime.Value)
	})

	t.Run("required fields are always populated", func(t *testing.T) {
		t.Parallel()
		sc := baseConcert()

		got := mapper.PendingConcertToProto(sc, performer)
		require.NotNil(t, got)
		assert.Equal(t, "staged-1", got.StagedId.GetValue())
		assert.Equal(t, "Summer Tour 2026", got.Title.GetValue())
		assert.Equal(t, "Nippon Budokan", got.ListedVenueName.GetValue())
		assert.Equal(t, timestamppb.New(discoveredAt), got.DiscoveredTime)
		assert.Equal(t, &entityv1.LocalDate{Value: mapper.TimeToDate(localDate)}, got.LocalDate)
	})
}
