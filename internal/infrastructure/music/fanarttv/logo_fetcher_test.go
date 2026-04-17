package fanarttv_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/music/fanarttv"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogoFetcher_FetchImage(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantNil    bool
		wantErr    error
		// check performs additional assertions beyond wantErr matching.
		check func(t *testing.T, err error)
	}{
		{
			name:       "return nil image without error when server returns 404",
			statusCode: http.StatusNotFound,
			wantNil:    true,
			wantErr:    nil,
		},
		{
			name:       "return ErrUnavailable when server returns 500",
			statusCode: http.StatusInternalServerError,
			wantErr:    apperr.ErrUnavailable,
		},
		{
			name:       "return ErrUnavailable with responseBody attr when server returns 403 with body",
			statusCode: http.StatusForbidden,
			body:       "Access denied by CDN policy",
			wantErr:    apperr.ErrUnavailable,
			check: func(t *testing.T, err error) {
				t.Helper()
				var appErr *apperr.AppErr
				if assert.ErrorAs(t, err, &appErr) {
					assert.Contains(t, appErr.Msg, "403", "error message should include status code")
					assert.NotEmpty(t, appErr.Attrs, "responseBody attr should be captured for non-200/non-404 responses with body")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				if tt.body != "" {
					_, _ = w.Write([]byte(tt.body))
				}
			}))
			defer server.Close()

			// The URL must pass the fanart.tv allowlist check. We rewrite the
			// host via a custom transport that redirects fanart.tv requests to
			// the test server, but the simpler approach is to call FetchImage
			// with the server URL directly after bypassing validation via the
			// exported ValidateLogoURL shim.
			//
			// Since the fetcher validates the URL before making the request, we
			// cannot point it at 127.0.0.1 (the httptest server) with a
			// fanart.tv hostname via the normal path. Instead we test the HTTP
			// response handling in isolation by creating a fetcher whose
			// underlying http.Client is the test server's client, and providing
			// a valid fanart.tv URL. The client's Transport will be a custom
			// RoundTripper that ignores the host and always connects to the
			// test server.
			transport := &redirectTransport{target: server.URL, inner: server.Client().Transport}
			httpClient := &http.Client{Transport: transport}

			fetcher := fanarttv.NewLogoFetcher(httpClient)

			img, err := fetcher.FetchImage(context.Background(), "https://assets.fanart.tv/fanart/music/logo.png")

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, img)
			} else {
				require.NoError(t, err)
				if tt.wantNil {
					assert.Nil(t, img)
				}
			}

			if tt.check != nil {
				tt.check(t, err)
			}
		})
	}
}

// redirectTransport is a test helper that rewrites the request host to the
// target URL so that requests to fanart.tv domain names are served by the
// httptest server.
type redirectTransport struct {
	target string
	inner  http.RoundTripper
}

func (r *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request so we do not mutate the original.
	clone := req.Clone(req.Context())
	clone.URL.Host = req.URL.Host
	// Point the request at the test server.
	targetURL, _ := http.NewRequest(http.MethodGet, r.target, nil)
	clone.URL.Scheme = targetURL.URL.Scheme
	clone.URL.Host = targetURL.URL.Host
	if r.inner != nil {
		return r.inner.RoundTrip(clone)
	}
	return http.DefaultTransport.RoundTrip(clone)
}

func TestValidateLogoURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		url     string
		wantErr error
	}{
		{
			name: "valid assets.fanart.tv URL",
			url:  "https://assets.fanart.tv/fanart/music/logo.png",
		},
		{
			name: "valid fanart.tv root domain",
			url:  "https://fanart.tv/some/path",
		},
		{
			name: "valid subdomain of fanart.tv",
			url:  "https://cdn.assets.fanart.tv/img.png",
		},
		{
			name:    "HTTP scheme rejected",
			url:     "http://assets.fanart.tv/logo.png",
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "internal IP rejected",
			url:     "https://169.254.169.254/metadata",
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "different host rejected",
			url:     "https://evil.com/logo.png",
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "suffix trick rejected",
			url:     "https://notfanart.tv/logo.png",
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "empty URL rejected",
			url:     "",
			wantErr: apperr.ErrInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := fanarttv.ValidateLogoURL(tt.url)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}

			assert.NoError(t, err)
		})
	}
}
