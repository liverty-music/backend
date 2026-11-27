package route_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/infrastructure/zitadel/route"
)

const publicIssuer = "https://auth.liverty-music.test"

// recordingTransport answers every request with 204 and keeps the last one.
type recordingTransport struct {
	got *http.Request
}

func (t *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.got = req
	rec := httptest.NewRecorder()
	rec.WriteHeader(http.StatusNoContent)
	return rec.Result(), nil
}

func TestRoute_Transport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		internalURL string
		requestURL  string
		wantURL     string
		wantHeaders bool
	}{
		{
			name:        "issuer host goes to the internal address with both headers",
			internalURL: "http://zitadel-api.zitadel.svc.cluster.local:80",
			requestURL:  publicIssuer + "/oauth/v2/keys",
			wantURL:     "http://zitadel-api.zitadel.svc.cluster.local:80/oauth/v2/keys",
			wantHeaders: true,
		},
		{
			name:        "another host is left alone",
			internalURL: "http://zitadel-api.zitadel.svc.cluster.local:80",
			requestURL:  "https://example.test/x",
			wantURL:     "https://example.test/x",
		},
		{
			name:       "no internal address: the issuer is called",
			requestURL: publicIssuer + "/oauth/v2/token",
			wantURL:    publicIssuer + "/oauth/v2/token",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r, err := route.New(publicIssuer, tt.internalURL)
			require.NoError(t, err)
			next := &recordingTransport{}
			req, err := http.NewRequest(http.MethodGet, tt.requestURL, nil)
			require.NoError(t, err)

			resp, err := r.Transport(next).RoundTrip(req)
			require.NoError(t, err)
			_ = resp.Body.Close()

			assert.Equal(t, tt.wantURL, next.got.URL.String())
			if tt.wantHeaders {
				assert.Equal(t, "auth.liverty-music.test", next.got.Header.Get(route.InstanceHostHeader))
				assert.Equal(t, "auth.liverty-music.test", next.got.Header.Get(route.PublicHostHeader))
			} else {
				assert.Empty(t, next.got.Header.Get(route.InstanceHostHeader))
				assert.Empty(t, next.got.Header.Get(route.PublicHostHeader))
			}
			assert.Empty(t, req.Header.Get(route.InstanceHostHeader), "the caller's request is not changed")
		})
	}
}

func TestRoute_GRPC(t *testing.T) {
	t.Parallel()

	internal, err := route.New(publicIssuer, "http://zitadel-api.zitadel.svc.cluster.local:80")
	require.NoError(t, err)
	endpoint, opts := internal.GRPC()
	assert.Equal(t, "zitadel-api.zitadel.svc.cluster.local:80", endpoint)
	assert.Len(t, opts, 3, "both headers and plain text")

	direct, err := route.New(publicIssuer, "")
	require.NoError(t, err)
	endpoint, opts = direct.GRPC()
	assert.Equal(t, "auth.liverty-music.test:443", endpoint)
	assert.Empty(t, opts)
	assert.Equal(t, publicIssuer, direct.Issuer())
}

func TestNew_InvalidURL(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ issuer, internal string }{
		{issuer: "", internal: ""},
		{issuer: "auth.liverty-music.test", internal: ""},
		{issuer: publicIssuer, internal: "zitadel-api:80"},
	} {
		_, err := route.New(tt.issuer, tt.internal)
		assert.Error(t, err, "issuer %q internal %q", tt.issuer, tt.internal)
	}
}
