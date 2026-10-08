package gemini_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newSiteClient starts one local server for every host in pages and returns
// an official-page client that dials those hosts to it. A host missing from
// pages does not resolve (NXDOMAIN). Each handler is chosen by the request's
// Host.
func newSiteClient(t *testing.T, pages map[string]http.HandlerFunc) *http.Client {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, ok := pages[r.Host]
		if !ok {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}))
	t.Cleanup(ts.Close)
	addr := ts.Listener.Addr().String()

	return gemini.NewOfficialPageClientWithDial(func(ctx context.Context, network, hostPort string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(hostPort)
		if err != nil {
			return nil, err
		}
		if _, ok := pages[host]; !ok {
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	})
}

// htmlPage answers with an HTML page whose body is body.
func htmlPage(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, "<!doctype html><html><body>%s</body></html>", body)
	}
}

func anchors(hrefs ...string) string {
	var b strings.Builder
	for _, h := range hrefs {
		fmt.Fprintf(&b, `<a href="%s">link</a>`, h)
	}
	return b.String()
}

func TestOfficialPageLinks(t *testing.T) {
	t.Parallel()

	twelveLive := make([]string, 0, 12)
	for i := 1; i <= 12; i++ {
		twelveLive = append(twelveLive, fmt.Sprintf("/live/detail/%d", i))
	}

	type args struct {
		siteURL string
		profile gemini.LinkProfile
	}
	tests := []struct {
		name    string
		pages   map[string]http.HandlerFunc
		args    args
		want    []string
		wantErr error
	}{
		{
			// @spec components/entity/concert/search "Concert pages linked from the top page"
			name: "Concert pages linked from the top page",
			pages: map[string]http.HandlerFunc{
				"vaundy.jp": htmlPage(anchors(
					"https://vaundy.jp/live/",
					"https://member.vaundy.jp/feature/ASIAARENATOUR_2026",
					"https://vaundy.jp/news/",
				)),
			},
			args: args{siteURL: "http://vaundy.jp/", profile: gemini.ConcertSearchProfile},
			want: []string{
				"https://vaundy.jp/live/",
				"https://member.vaundy.jp/feature/ASIAARENATOUR_2026",
				"https://vaundy.jp/news/",
			},
		},
		{
			// @spec components/entity/concert/search "News pages after concert pages"
			name: "News pages after concert pages",
			pages: map[string]http.HandlerFunc{
				"vaundy.jp": htmlPage(anchors(append([]string{"/news/detail/11310"}, twelveLive[:8]...)...)),
			},
			args: args{siteURL: "http://vaundy.jp/", profile: gemini.ConcertSearchProfile},
			want: []string{
				"http://vaundy.jp/live/detail/1", "http://vaundy.jp/live/detail/2", "http://vaundy.jp/live/detail/3",
				"http://vaundy.jp/live/detail/4", "http://vaundy.jp/live/detail/5", "http://vaundy.jp/live/detail/6",
				"http://vaundy.jp/live/detail/7", "http://vaundy.jp/live/detail/8",
			},
		},
		{
			// @spec components/entity/concert/search "Link to another domain ignored"
			name: "Link to another domain ignored",
			pages: map[string]http.HandlerFunc{
				"vaundy.jp": htmlPage(anchors(
					"https://eplus.jp/sf/live/vaundy-tour/",
					"https://x.com/vaundy_live",
					"/live/",
				)),
			},
			args: args{siteURL: "http://vaundy.jp/", profile: gemini.ConcertSearchProfile},
			want: []string{"http://vaundy.jp/live/"},
		},
		{
			// @spec components/entity/concert/search "More than 8 concert links"
			name:  "More than 8 concert links",
			pages: map[string]http.HandlerFunc{"vaundy.jp": htmlPage(anchors(twelveLive...))},
			args:  args{siteURL: "http://vaundy.jp/", profile: gemini.ConcertSearchProfile},
			want: []string{
				"http://vaundy.jp/live/detail/1", "http://vaundy.jp/live/detail/2", "http://vaundy.jp/live/detail/3",
				"http://vaundy.jp/live/detail/4", "http://vaundy.jp/live/detail/5", "http://vaundy.jp/live/detail/6",
				"http://vaundy.jp/live/detail/7", "http://vaundy.jp/live/detail/8",
			},
		},
		{
			// @spec components/entity/concert/search "Top page larger than 2 MB"
			name: "Top page larger than 2 MB",
			pages: map[string]http.HandlerFunc{
				"vaundy.jp": htmlPage(anchors("https://vaundy.jp/live/") +
					strings.Repeat("<p>padding</p>", 3<<20/len("<p>padding</p>")) +
					anchors("https://vaundy.jp/tour/beyond-the-cap/")),
			},
			args: args{siteURL: "http://vaundy.jp/", profile: gemini.ConcertSearchProfile},
			want: []string{"https://vaundy.jp/live/"},
		},
		{
			// @spec components/entity/concert/search "Redirect to another domain"
			name: "Redirect to another domain",
			pages: map[string]http.HandlerFunc{
				"vaundy.jp": func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, "http://linktr.ee/vaundy", http.StatusFound)
				},
				"linktr.ee": htmlPage(anchors("http://linktr.ee/live/")),
			},
			args:    args{siteURL: "http://vaundy.jp/", profile: gemini.ConcertSearchProfile},
			wantErr: gemini.ErrOffSiteRedirect,
		},
		{
			name: "redirect within the site is followed",
			pages: map[string]http.HandlerFunc{
				"vaundy.jp": func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, "http://www.vaundy.jp/top/", http.StatusMovedPermanently)
				},
				"www.vaundy.jp": htmlPage(anchors("live/")),
			},
			args: args{siteURL: "http://vaundy.jp/", profile: gemini.ConcertSearchProfile},
			want: []string{"http://www.vaundy.jp/top/live/"},
		},
		{
			// @spec components/entity/concert/search "Top page unavailable"
			name: "Top page unavailable",
			pages: map[string]http.HandlerFunc{
				"vaundy.jp": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) },
			},
			args:    args{siteURL: "http://vaundy.jp/", profile: gemini.ConcertSearchProfile},
			wantErr: assert.AnError,
		},
		{
			name: "non-HTML top page",
			pages: map[string]http.HandlerFunc{
				"vaundy.jp": func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"href":"/live/"}`))
				},
			},
			args:    args{siteURL: "http://vaundy.jp/", profile: gemini.ConcertSearchProfile},
			wantErr: assert.AnError,
		},
		{
			// @spec components/entity/concert/search "www host without DNS"
			name: "www host without DNS",
			pages: map[string]http.HandlerFunc{
				"super-beaver.com": htmlPage(anchors("/live_information/list/", "/news/")),
			},
			args: args{siteURL: "http://www.super-beaver.com/", profile: gemini.ConcertSearchProfile},
			want: []string{"http://super-beaver.com/live_information/list/", "http://super-beaver.com/news/"},
		},
		{
			name:    "apex host without DNS has no fallback",
			pages:   map[string]http.HandlerFunc{},
			args:    args{siteURL: "http://super-beaver.com/", profile: gemini.ConcertSearchProfile},
			wantErr: assert.AnError,
		},
		{
			name: "fragments, default ports and language parameters dropped, repeats removed",
			pages: map[string]http.HandlerFunc{
				"gogovanillas.com": htmlPage(anchors(
					"/live/?lang=ja",
					"/live/#top",
					"/live/",
					"http://gogovanillas.com:80/live/",
					"https://sp.gogovanillas.com:443/feature/tour2728",
					"https://sp.gogovanillas.com/feature/tour2728?_normalbrowse_=1",
					"/schedule/?range=future&amp;sort=asc&amp;lang=en",
					"mailto:live@gogovanillas.com",
				)),
			},
			args: args{siteURL: "http://gogovanillas.com/", profile: gemini.ConcertSearchProfile},
			want: []string{
				"http://gogovanillas.com/live/",
				"https://sp.gogovanillas.com/feature/tour2728",
				"http://gogovanillas.com/schedule/?range=future&sort=asc",
			},
		},
		{
			name: "a lower tier never displaces a higher one",
			pages: map[string]http.HandlerFunc{
				"novelbright.jp": htmlPage(anchors(
					"/news/detail/1", "/news/detail/2", "/news/detail/3",
					"/schedule/list/", "/feature/arenatour2026",
				)),
			},
			args: args{
				siteURL: "http://novelbright.jp/",
				profile: gemini.NewLinkProfile(3, []string{"schedule", "tour"}, []string{"news"}),
			},
			want: []string{
				"http://novelbright.jp/schedule/list/",
				"http://novelbright.jp/feature/arenatour2026",
				"http://novelbright.jp/news/detail/1",
			},
		},
		{
			name:    "unsupported scheme",
			pages:   map[string]http.HandlerFunc{},
			args:    args{siteURL: "ftp://vaundy.jp/", profile: gemini.ConcertSearchProfile},
			wantErr: assert.AnError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := newSiteClient(t, tt.pages)

			got, err := gemini.OfficialPageLinks(context.Background(), client, tt.args.siteURL, tt.args.profile)

			if tt.wantErr != nil {
				require.Error(t, err)
				if tt.wantErr != assert.AnError {
					assert.ErrorIs(t, err, tt.wantErr)
				}
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestOfficialPageLinks_TooManyRedirects(t *testing.T) {
	t.Parallel()

	var hops atomic.Int32
	client := newSiteClient(t, map[string]http.HandlerFunc{
		"vaundy.jp": func(w http.ResponseWriter, r *http.Request) {
			n := hops.Add(1)
			http.Redirect(w, r, fmt.Sprintf("/hop/%d", n), http.StatusFound)
		},
	})

	got, err := gemini.OfficialPageLinks(context.Background(), client, "http://vaundy.jp/", gemini.ConcertSearchProfile)

	require.Error(t, err)
	assert.Empty(t, got)
	assert.Equal(t, int32(4), hops.Load(), "the first request and 3 redirects")
}

func TestNewOfficialPageClient_RefusesNonPublicAddresses(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<a href="/live/">live</a>`))
	}))
	t.Cleanup(local.Close)

	tests := []struct {
		name    string
		siteURL string
	}{
		{
			// @spec components/entity/concert/search "Private address refused"
			name:    "Private address refused",
			siteURL: "http://10.0.0.5/",
		},
		{name: "loopback", siteURL: local.URL + "/"},
		{name: "link-local metadata server", siteURL: "http://169.254.169.254/"},
		{name: "unspecified", siteURL: "http://0.0.0.0/"},
		{name: "IPv6 unique local", siteURL: "http://[fd00::1]/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := gemini.OfficialPageLinks(context.Background(), gemini.NewOfficialPageClient(), tt.siteURL, gemini.ConcertSearchProfile)

			assert.ErrorIs(t, err, gemini.ErrNonPublicAddress)
			assert.Empty(t, got)
		})
	}
	t.Cleanup(func() {
		assert.Zero(t, hits.Load(), "a refused address is never requested")
	})
}
