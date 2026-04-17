package httpx_test

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/liverty-music/backend/pkg/httpx"
	"github.com/stretchr/testify/assert"
)

func TestCaptureResponseBody(t *testing.T) {
	t.Parallel()

	const maxBytes = 1024

	tests := []struct {
		name string
		args struct {
			r io.Reader
		}
		want string
	}{
		{
			name: "return empty string when body is empty",
			args: struct{ r io.Reader }{r: strings.NewReader("")},
			want: "",
		},
		{
			name: "return exact string when body is smaller than max bytes",
			args: struct{ r io.Reader }{r: strings.NewReader("error message")},
			want: "error message",
		},
		{
			name: "return string without truncation indicator when body is exactly max bytes",
			args: struct{ r io.Reader }{r: strings.NewReader(strings.Repeat("a", maxBytes))},
			want: strings.Repeat("a", maxBytes),
		},
		{
			name: "return first 1024 bytes with ellipsis when body exceeds max bytes",
			args: struct{ r io.Reader }{r: strings.NewReader(strings.Repeat("b", 2000))},
			want: strings.Repeat("b", maxBytes) + "…",
		},
		{
			name: "replace non-printable bytes with replacement character",
			args: struct{ r io.Reader }{r: strings.NewReader("\x00\x01\x02\xff")},
			want: "\uFFFD\uFFFD\uFFFD\uFFFD",
		},
		{
			name: "return empty string when reader returns an error",
			args: struct{ r io.Reader }{r: iotest.ErrReader(errors.New("broken"))},
			want: "",
		},
		{
			name: "preserve printable whitespace characters such as tab newline and space",
			args: struct{ r io.Reader }{r: strings.NewReader("line1\nline2\ttabbed line3 with space")},
			want: "line1\nline2\ttabbed line3 with space",
		},
		{
			name: "replace high non-ASCII bytes and keep printable ASCII intact",
			args: struct{ r io.Reader }{r: strings.NewReader("ok\x7fnotok")},
			want: "ok\uFFFDnotok",
		},
		{
			name: "valid UTF-8 multi-byte characters are preserved",
			args: struct{ r io.Reader }{r: strings.NewReader("権限がありません: FCM rejected")},
			want: "権限がありません: FCM rejected",
		},
		{
			name: "mixed valid UTF-8 and control characters",
			args: struct{ r io.Reader }{r: strings.NewReader("error\x00日本語\x01ok")},
			want: "error\uFFFD日本語\uFFFDok",
		},
		{
			name: "truncate with ellipsis and replace non-printable bytes when body is large with binary data",
			args: struct{ r io.Reader }{r: strings.NewReader("\x00" + strings.Repeat("a", 2000))},
			// First byte becomes replacement char, next 1023 are 'a', then ellipsis
			want: "\uFFFD" + strings.Repeat("a", maxBytes-1) + "…",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := httpx.CaptureResponseBody(tt.args.r)

			assert.Equal(t, tt.want, got)
		})
	}
}
