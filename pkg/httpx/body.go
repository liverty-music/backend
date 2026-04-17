package httpx

import (
	"bytes"
	"io"
)

// maxBodyCaptureBytes is the maximum number of bytes read from an error response body.
const maxBodyCaptureBytes = 1024

// CaptureResponseBody reads up to 1024 bytes from r, sanitizes non-printable
// bytes by replacing them with U+FFFD, and appends "…" (U+2026) when the
// original stream contained more than 1024 bytes.
//
// The caller retains responsibility for closing the underlying body. This
// function only reads from r and drains the remainder into [io.Discard] for
// connection reuse; it never closes r.
//
// If the read fails, CaptureResponseBody returns an empty string so the caller
// can proceed without masking the original error.
func CaptureResponseBody(r io.Reader) string {
	buf := make([]byte, maxBodyCaptureBytes+1)
	n, err := io.ReadFull(io.LimitReader(r, int64(len(buf))), buf)
	// io.ReadFull returns io.ErrUnexpectedEOF when fewer bytes than the buffer
	// length were available, which is the normal case. Any other error means
	// the read failed and we should not surface a partial body.
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return ""
	}

	truncated := n > maxBodyCaptureBytes
	if truncated {
		n = maxBodyCaptureBytes
	}

	sanitized := sanitizeBody(buf[:n])

	// Drain remaining bytes so the underlying TCP connection can be reused.
	_, _ = io.Copy(io.Discard, r)

	if truncated {
		return sanitized + "…"
	}
	return sanitized
}

// sanitizeBody replaces control characters and invalid UTF-8 with U+FFFD while
// preserving valid multi-byte Unicode (e.g., Japanese, accented Latin).
// Iterating via range string(b) auto-decodes UTF-8 runes; invalid byte
// sequences become U+FFFD during decoding.
func sanitizeBody(b []byte) string {
	var out bytes.Buffer
	out.Grow(len(b))
	for _, r := range string(b) {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			out.WriteRune(r)
		case r < 0x20 || r == 0x7F:
			out.WriteRune('\uFFFD')
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}
