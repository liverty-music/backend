package rpc_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-logging/logging"
)

func TestConcertHandler_List(t *testing.T) {
	logger, _ := logging.New()

	t.Run("success", func(t *testing.T) {
		artistUC := mocks.NewMockArtistUseCase(t)
		concertUC := mocks.NewMockConcertUseCase(t)
		h := rpc.NewConcertHandler(artistUC, concertUC, logger)
		_ = h // Basic test to ensure handler can be created with mocked UseCases
	})
}
