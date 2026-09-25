//go:build vips

package event_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
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

	jpegOriginal, err := os.ReadFile("testdata/valid_photo_4000x3000.jpg")
	require.NoError(t, err)
	webpOriginal, err := os.ReadFile("testdata/valid_photo_4000x3000.webp")
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
		// @spec components/usecase/media/process-media "Valid photo"
		{
			name:    "produces an 800px thumb and 1920px large WebP without EXIF from a 4000x3000 JPEG",
			args:    args{data: jpegOriginal},
			wantErr: nil,
			check: func(t *testing.T, thumb, large []byte) {
				t.Helper()
				assertValidWebPVariant(t, thumb, 800)
				assertValidWebPVariant(t, large, 1920)
				assertNoEXIF(t, thumb)
				assertNoEXIF(t, large)
			},
		},
		// @spec components/usecase/media/process-media "WebP original"
		{
			name:    "produces an 800px thumb and 1920px large WebP from a 4000x3000 WebP, same as JPEG or PNG",
			args:    args{data: webpOriginal},
			wantErr: nil,
			check: func(t *testing.T, thumb, large []byte) {
				t.Helper()
				assertValidWebPVariant(t, thumb, 800)
				assertValidWebPVariant(t, large, 1920)
			},
		},
		// @spec components/usecase/media/process-media "Decompression bomb"
		{
			name:    "rejects a declared 10000x10000 image before full decode",
			args:    args{data: fakePNGHeader(10000, 10000)},
			wantErr: event.ErrUnsupportedMedia,
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
				assert.Nil(t, thumb)
				assert.Nil(t, large)
				return
			}

			require.NoError(t, err)
			tt.check(t, thumb, large)
		})
	}
}

// assertValidWebPVariant asserts that data decodes as a WebP image resized
// down to exactly maxWidth (the original in these tests is always wider, so
// the resize cap is what determines the output width; aspect ratio is
// preserved and there is no cropping).
func assertValidWebPVariant(t *testing.T, data []byte, maxWidth int) {
	t.Helper()
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	require.NoError(t, err)
	assert.Equal(t, "webp", format)
	assert.Equal(t, maxWidth, cfg.Width)
}

// assertNoEXIF asserts that the encoded WebP bytes carry no EXIF chunk, i.e.
// metadata was stripped before encoding. It parses the RIFF/VP8X container
// structure rather than substring-matching "EXIF" in the raw bytes, since
// compressed VP8 pixel data can coincidentally contain that byte sequence.
func assertNoEXIF(t *testing.T, data []byte) {
	t.Helper()
	require.False(t, hasWebPEXIFChunk(data), "output WebP unexpectedly carries an EXIF chunk")
}

// hasWebPEXIFChunk reports whether a WebP file's VP8X extended-format header
// declares an EXIF chunk (flags bit 3; see the WebP container spec). A
// simple (non-extended) "VP8 " or "VP8L" file never carries metadata.
func hasWebPEXIFChunk(data []byte) bool {
	const vp8xFlagsOffset = 20
	if len(data) <= vp8xFlagsOffset {
		return false
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" || string(data[12:16]) != "VP8X" {
		return false
	}
	const exifFlagBit = 0x08
	return data[vp8xFlagsOffset]&exifFlagBit != 0
}

// fakePNGHeader builds a minimal PNG (signature + IHDR chunk only, no pixel
// data) declaring the given width and height. image.DecodeConfig reads only
// the IHDR chunk, so this is enough to exercise the pre-decode safety check
// without needing a real (and enormous) decompression-bomb fixture.
func fakePNGHeader(width, height uint32) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})

	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], width)
	binary.BigEndian.PutUint32(ihdr[4:8], height)
	ihdr[8] = 8 // bit depth
	ihdr[9] = 2 // color type: truecolor

	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(ihdr)))
	buf.Write(length)
	buf.WriteString("IHDR")
	buf.Write(ihdr)
	crc := crc32.ChecksumIEEE(append([]byte("IHDR"), ihdr...))
	crcBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(crcBytes, crc)
	buf.Write(crcBytes)

	return buf.Bytes()
}
