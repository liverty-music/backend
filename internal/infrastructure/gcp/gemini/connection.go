package gemini

import (
	"errors"
	"io"
	"net/http"
)

// connectionError is a failure of the connection to the model: the request
// could not be sent, or the answer was not received in full.
type connectionError struct{ err error }

// Error implements error.
func (e *connectionError) Error() string { return e.err.Error() }

// Unwrap returns the transport's error.
func (e *connectionError) Unwrap() error { return e.err }

// isConnectionError reports whether err is a failure of the connection.
func isConnectionError(err error) bool {
	_, ok := errors.AsType[*connectionError](err)
	return ok
}

// markConnectionErrors returns a copy of client, which may be nil, whose
// failures to send a request or to read an answer are connectionErrors. The
// transport's own error types cannot tell them apart from other failures: an
// HTTP/2 stream reset while the answer is read has an unexported type.
func markConnectionErrors(client *http.Client) *http.Client {
	var marked http.Client
	if client != nil {
		marked = *client
	}
	base := marked.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	marked.Transport = connectionTransport{base: base}
	return &marked
}

// connectionTransport marks the errors of base and of the bodies it returns.
type connectionTransport struct{ base http.RoundTripper }

// RoundTrip implements http.RoundTripper.
func (t connectionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, &connectionError{err: err}
	}
	resp.Body = connectionBody{resp.Body}
	return resp, nil
}

// connectionBody marks the errors of reading an answer.
type connectionBody struct{ io.ReadCloser }

// Read implements io.Reader. io.EOF, the end of a complete answer, is passed
// through unchanged.
func (b connectionBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && err != io.EOF {
		return n, &connectionError{err: err}
	}
	return n, err
}
