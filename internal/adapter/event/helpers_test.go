package event_test

import (
	"testing"

	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/require"
)

func newTestLogger(t *testing.T) *logging.Logger {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return logger
}
