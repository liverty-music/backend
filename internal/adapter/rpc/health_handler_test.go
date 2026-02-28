package rpc_test

import (
	"context"
	"testing"

	"connectrpc.com/grpchealth"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
)

func TestHealthCheckHandler_SetShuttingDown(t *testing.T) {
	tests := []struct {
		name           string
		shutdownCalled bool
		wantStatus     grpchealth.Status
	}{
		{
			name:           "returns NOT_SERVING after SetShuttingDown",
			shutdownCalled: true,
			wantStatus:     grpchealth.StatusNotServing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, err := logging.New()
			assert.NoError(t, err)

			// Pass nil DB — shutdown flag is checked before DB ping.
			handler := rpc.NewHealthCheckHandler(nil, logger)

			if tt.shutdownCalled {
				handler.SetShuttingDown()
			}

			resp, err := handler.Check(context.Background(), &grpchealth.CheckRequest{})
			assert.NoError(t, err)
			assert.Equal(t, tt.wantStatus, resp.Status)
		})
	}
}
