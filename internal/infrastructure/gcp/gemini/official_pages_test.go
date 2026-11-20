package gemini_test

import (
	"context"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/pannpers/go-apperr/apperr"
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

// sitePaths answers each path of paths with its handler and any other path
// with 404.
func sitePaths(paths map[string]http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h, ok := paths[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		h(w, r)
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
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name: "redirect within the site is followed",
			pages: map[string]http.HandlerFunc{
				"vaundy.jp": func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, "http://www.vaundy.jp/top/", http.StatusMovedPermanently)
				},
				"www.vaundy.jp": sitePaths(map[string]http.HandlerFunc{"/top/": htmlPage(anchors("live/"))}),
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

			assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
			assert.Empty(t, got)
		})
	}
	t.Cleanup(func() {
		assert.Zero(t, hits.Load(), "a refused address is never requested")
	})
}

// TestOfficialPageLinks_ConcertPages covers the second level: the links of
// the concert pages the top page links to.
func TestOfficialPageLinks_ConcertPages(t *testing.T) {
	t.Parallel()

	numbered := func(format string, from, to int) []string {
		var out []string
		for i := from; i <= to; i++ {
			out = append(out, fmt.Sprintf(format, i))
		}
		return out
	}
	fiveLive := numbered("/live/%d/", 1, 5)
	fiveLivePages := map[string]http.HandlerFunc{"/": htmlPage(anchors(fiveLive...))}
	for i, p := range fiveLive {
		fiveLivePages[p] = htmlPage(anchors(fmt.Sprintf("/contents/%d", i+1)))
	}
	thirtyPages := numbered("/contents/%d", 1, 30)

	tests := []struct {
		name  string
		paths map[string]http.HandlerFunc
		// other hosts besides example.jp
		hosts      map[string]http.HandlerFunc
		want       []string
		notFetched []string
	}{
		{
			// @spec components/entity/concert/search "Tour detail pages behind a live page"
			name: "Tour detail pages behind a live page",
			paths: map[string]http.HandlerFunc{
				"/":      htmlPage(anchors("http://example.jp/live/", "http://example.jp/goods/")),
				"/live/": htmlPage(anchors("http://example.jp/live/tour-a/", "http://example.jp/contents/123", "http://example.jp/goods/")),
			},
			want: []string{"http://example.jp/live/", "http://example.jp/live/tour-a/", "http://example.jp/contents/123"},
		},
		{
			// @spec components/entity/concert/search "Only the first 3 concert pages followed"
			name:  "Only the first 3 concert pages followed",
			paths: fiveLivePages,
			want: append(numbered("http://example.jp/live/%d/", 1, 5),
				"http://example.jp/contents/1", "http://example.jp/contents/2", "http://example.jp/contents/3"),
			notFetched: []string{"/live/4/", "/live/5/"},
		},
		{
			// @spec components/entity/concert/search "News page not followed"
			name: "News page not followed",
			paths: map[string]http.HandlerFunc{
				"/":      htmlPage(anchors("/live/", "/news/")),
				"/live/": htmlPage(anchors("/")),
				"/news/": htmlPage(anchors(numbered("/news/%d", 1, 10)...)),
			},
			want:       []string{"http://example.jp/live/", "http://example.jp/news/"},
			notFetched: []string{"/news/"},
		},
		{
			// @spec components/entity/concert/search "Concert page link to another domain ignored"
			name: "Concert page link to another domain ignored",
			paths: map[string]http.HandlerFunc{
				"/":      htmlPage(anchors("/live/")),
				"/live/": htmlPage(anchors("https://eplus.jp/sf/live/example-tour/", "/live/tour-a/")),
			},
			want: []string{"http://example.jp/live/", "http://example.jp/live/tour-a/"},
		},
		{
			// @spec components/entity/concert/search "At most 20 pages"
			name: "At most 20 pages",
			paths: map[string]http.HandlerFunc{
				"/":       htmlPage(anchors("/live/", "/tour/", "/news/1", "/news/2")),
				"/live/":  htmlPage(anchors(thirtyPages[:20]...)),
				"/tour/":  htmlPage(anchors(thirtyPages[20:]...)),
				"/news/1": htmlPage(""),
			},
			want: append([]string{"http://example.jp/live/", "http://example.jp/tour/", "http://example.jp/news/1", "http://example.jp/news/2"},
				numbered("http://example.jp/contents/%d", 1, 16)...),
		},
		{
			// @spec components/entity/concert/search "Concert page unavailable"
			name: "Concert page unavailable",
			paths: map[string]http.HandlerFunc{
				"/":          htmlPage(anchors("/live/", "/schedule/")),
				"/live/":     func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) },
				"/schedule/": htmlPage(anchors("/schedule/2027")),
			},
			want: []string{"http://example.jp/live/", "http://example.jp/schedule/", "http://example.jp/schedule/2027"},
		},
		{
			name: "concert page redirected to another domain skipped",
			paths: map[string]http.HandlerFunc{
				"/": htmlPage(anchors("/live/")),
				"/live/": func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, "http://linktr.ee/example", http.StatusFound)
				},
			},
			hosts: map[string]http.HandlerFunc{"linktr.ee": htmlPage(anchors("http://linktr.ee/tour/"))},
			want:  []string{"http://example.jp/live/"},
		},
		{
			name: "top page, top-page links and repeats dropped; detail pages not followed",
			paths: map[string]http.HandlerFunc{
				"/":       htmlPage(anchors("/live/", "/tour/", "/profile/")),
				"/live/":  htmlPage(anchors("/", "/#top", "/profile/", "/tour/a", "/tour/a#dates", "/tour/b")),
				"/tour/":  htmlPage(anchors("/tour/b", "https://fc.example.jp/tour/c")),
				"/tour/a": htmlPage(anchors("/tour/z")),
			},
			want: []string{
				"http://example.jp/live/", "http://example.jp/tour/",
				"http://example.jp/tour/a", "http://example.jp/tour/b", "https://fc.example.jp/tour/c",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			fetched := map[string]int{}
			site := sitePaths(tt.paths)
			hosts := map[string]http.HandlerFunc{"example.jp": func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				fetched[r.URL.Path]++
				mu.Unlock()
				site(w, r)
			}}
			maps.Copy(hosts, tt.hosts)
			client := newSiteClient(t, hosts)

			got, err := gemini.OfficialPageLinks(context.Background(), client, "http://example.jp/", gemini.ConcertSearchProfile)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			mu.Lock()
			defer mu.Unlock()
			for _, p := range tt.notFetched {
				assert.Zero(t, fetched[p], "%s is not read", p)
			}
		})
	}
}
