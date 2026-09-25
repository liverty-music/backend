//go:build vips

package event_test

import (
	"bytes"
	"context"
	"image"
	"os"
	"testing"

	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVipsProcessor_ProcessImage exercises the real libvips-backed
// MediaProcessor. It only runs when built with `-tags vips` (see
// media_processor_vips.go), since it links libvips via CGO.
func TestVipsProcessor_ProcessImage(t *testing.T) {
	// Not parallel: vips.Startup(nil) in NewMediaProcessor mutates
	// process-global libvips state.

	webpOriginal, err := os.ReadFile("testdata/tiny_lossless.webp")
	require.NoError(t, err)

	type args struct {
		data []byte
	}
	tests := []struct {
		name    string
		args    args
		wantErr error
		check   func(t *testing.T, thumb, large []byte)
	}{
		{
			name:    "produces WebP thumb and large variants from a WebP original",
			args:    args{data: webpOriginal},
			wantErr: nil,
			check: func(t *testing.T, thumb, large []byte) {
				t.Helper()
				assertValidWebP(t, thumb)
				assertValidWebP(t, large)
			},
		},
		{
			name:    "rejects corrupt data",
			args:    args{data: []byte("not an image")},
			wantErr: event.ErrUnsupportedMedia,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := newTestLogger(t)
			processor := event.NewMediaProcessor(logger)

			thumb, large, err := processor.ProcessImage(context.Background(), tt.args.data)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			tt.check(t, thumb, large)
		})
	}
}

// assertValidWebP asserts that data decodes as a non-empty WebP image.
func assertValidWebP(t *testing.T, data []byte) {
	t.Helper()
	assert.NotEmpty(t, data)
	_, format, err := image.DecodeConfig(bytes.NewReader(data))
	require.NoError(t, err)
	assert.Equal(t, "webp", format)
}
