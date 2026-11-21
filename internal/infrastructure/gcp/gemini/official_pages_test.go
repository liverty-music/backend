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
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/encoding/japanese"
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

// link renders an <a> tag with text label; banner renders one wrapping an
// image whose alternative text is alt.
func link(href, label string) string { return fmt.Sprintf(`<a href="%s">%s</a>`, href, label) }
func banner(href, alt string) string {
	return fmt.Sprintf(`<a href="%s"><figure><img src="/b.jpg" alt="%s"/></figure></a>`, href, alt)
}

func TestOfficialPageLinks(t *testing.T) {
	t.Parallel()

	seventy := make([]string, 0, 70)
	var seventyWant []gemini.PageLink
	for i := 1; i <= 70; i++ {
		href := fmt.Sprintf("http://example.jp/news/detail/%d", i)
		seventy = append(seventy, link(href, fmt.Sprintf("news %d", i)))
		if i <= 60 {
			seventyWant = append(seventyWant, gemini.PageLink{URL: href, Label: fmt.Sprintf("news %d", i)})
		}
	}

	tests := []struct {
		name     string
		pages    map[string]http.HandlerFunc
		siteURL  string
		want     []gemini.PageLink
		wantRead string
		wantErr  error
	}{
		{
			// @spec components/entity/concert/search "Links listed with their labels"
			name: "Links listed with their labels",
			pages: map[string]http.HandlerFunc{
				"vaundy.jp": htmlPage(link("/live/", "LIVE") +
					banner("https://member.vaundy.jp/feature/ASIAARENATOUR_2026", "ASIA ARENA TOUR 2026") +
					link("/news/", " NEWS\n ")),
			},
			siteURL: "http://vaundy.jp/",
			want: []gemini.PageLink{
				{URL: "http://vaundy.jp/live/", Label: "LIVE"},
				{URL: "https://member.vaundy.jp/feature/ASIAARENATOUR_2026", Label: "ASIA ARENA TOUR 2026"},
				{URL: "http://vaundy.jp/news/", Label: "NEWS"},
			},
			wantRead: "http://vaundy.jp/",
		},
		{
			// @spec components/entity/concert/search "Special show page without a concert word in its path"
			name: "Special show page without a concert word in its path",
			pages: map[string]http.HandlerFunc{
				"super-beaver.com": htmlPage(banner("https://sp.super-beaver.com/feature/budokan2027?_normalbrowse_=1", "武道館2027")),
			},
			siteURL: "http://super-beaver.com/",
			want:    []gemini.PageLink{{URL: "https://sp.super-beaver.com/feature/budokan2027", Label: "武道館2027"}},
		},
		{
			// @spec components/entity/concert/search "Official tour site on another domain"
			name:    "Official tour site on another domain",
			pages:   map[string]http.HandlerFunc{"gogovanillas.com": htmlPage(banner("https://ggv-backhometour2027.jp", "BACK HOME TOUR 2027"))},
			siteURL: "http://gogovanillas.com/",
			want:    []gemini.PageLink{{URL: "https://ggv-backhometour2027.jp", Label: "BACK HOME TOUR 2027"}},
		},
		{
			// @spec components/entity/concert/search "Link to another domain ignored"
			name: "Link to another domain ignored",
			pages: map[string]http.HandlerFunc{
				"vaundy.jp": htmlPage(link("https://eplus.jp/sf/live/vaundy-tour/", "チケット") +
					link("https://x.com/vaundy_live", "X") + link("https://www.instagram.com/vaundy/", "Instagram") +
					link("/live/", "LIVE")),
			},
			siteURL: "http://vaundy.jp/",
			want:    []gemini.PageLink{{URL: "http://vaundy.jp/live/", Label: "LIVE"}},
		},
		{
			// @spec components/entity/concert/search "Site information page left out"
			name: "Site information page left out",
			pages: map[string]http.HandlerFunc{
				"vaundy.jp": htmlPage(link("/privacy/", "プライバシーポリシー") + link("/about/terms", "利用規約") +
					link("/discography/detail/58/", "人生") + link("/faq.html", "FAQ") + link("/schedule/", "SCHEDULE")),
			},
			siteURL: "http://vaundy.jp/",
			want:    []gemini.PageLink{{URL: "http://vaundy.jp/schedule/", Label: "SCHEDULE"}},
		},
		{
			// @spec components/entity/concert/search "More than 60 links"
			name:    "More than 60 links",
			pages:   map[string]http.HandlerFunc{"example.jp": htmlPage(strings.Join(seventy, ""))},
			siteURL: "http://example.jp/",
			want:    seventyWant,
		},
		{
			// @spec components/entity/concert/search "Top page larger than 2 MB"
			name: "Top page larger than 2 MB",
			pages: map[string]http.HandlerFunc{
				"vaundy.jp": htmlPage(link("https://vaundy.jp/live/", "LIVE") +
					strings.Repeat("<p>padding</p>", 3<<20/len("<p>padding</p>")) +
					link("https://vaundy.jp/tour/beyond-the-cap/", "TOUR")),
			},
			siteURL: "http://vaundy.jp/",
			want:    []gemini.PageLink{{URL: "https://vaundy.jp/live/", Label: "LIVE"}},
		},
		{
			// @spec components/entity/concert/search "Redirect to another domain"
			name: "Redirect to another domain",
			pages: map[string]http.HandlerFunc{
				"vaundy.jp": func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, "http://linktr.ee/vaundy", http.StatusFound)
				},
				"linktr.ee": htmlPage(link("http://linktr.ee/live/", "LIVE")),
			},
			siteURL: "http://vaundy.jp/",
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name: "redirect within the site is followed and its URL returned",
			pages: map[string]http.HandlerFunc{
				"vaundy.jp": func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, "http://www.vaundy.jp/top/", http.StatusMovedPermanently)
				},
				"www.vaundy.jp": htmlPage(link("live/", "LIVE")),
			},
			siteURL:  "http://vaundy.jp/",
			want:     []gemini.PageLink{{URL: "http://www.vaundy.jp/top/live/", Label: "LIVE"}},
			wantRead: "http://www.vaundy.jp/top/",
		},
		{
			// @spec components/entity/concert/search "Top page unavailable"
			name: "Top page unavailable",
			pages: map[string]http.HandlerFunc{
				"vaundy.jp": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) },
			},
			siteURL: "http://vaundy.jp/",
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
			siteURL: "http://vaundy.jp/",
			wantErr: assert.AnError,
		},
		{
			// @spec components/entity/concert/search "www host without DNS"
			name:     "www host without DNS",
			pages:    map[string]http.HandlerFunc{"super-beaver.com": htmlPage(link("/live_information/list/", "LIVE"))},
			siteURL:  "http://www.super-beaver.com/",
			want:     []gemini.PageLink{{URL: "http://super-beaver.com/live_information/list/", Label: "LIVE"}},
			wantRead: "http://super-beaver.com/",
		},
		{
			name:    "apex host without DNS has no fallback",
			pages:   map[string]http.HandlerFunc{},
			siteURL: "http://super-beaver.com/",
			wantErr: assert.AnError,
		},
		{
			name: "fragments, default ports and language parameters dropped, repeats and the page itself removed",
			pages: map[string]http.HandlerFunc{
				"gogovanillas.com": htmlPage(
					banner("/live/?lang=ja", "") + link("/live/#top", "LIVE") + link("/live/", "other label") +
						link("http://gogovanillas.com:80/live/", "LIVE") + link("#", "MENU") + `<a>no href</a>` + link("/", "TOP") +
						link("https://sp.gogovanillas.com:443/feature/tour2728?_normalbrowse_=1", "TOUR") +
						link("mailto:live@gogovanillas.com", "mail") + link("javascript:void(0)", "menu")),
			},
			siteURL: "http://gogovanillas.com/",
			want: []gemini.PageLink{
				{URL: "http://gogovanillas.com/live/", Label: "LIVE"},
				{URL: "https://sp.gogovanillas.com/feature/tour2728", Label: "TOUR"},
			},
		},
		{
			name: "Shift_JIS page decoded from its declared charset",
			pages: map[string]http.HandlerFunc{
				"example.jp": func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/html; charset=Shift_JIS")
					sjis, _ := japanese.ShiftJIS.NewEncoder().String(`<a href="/live/">ライブ情報</a>`)
					_, _ = w.Write([]byte(sjis))
				},
			},
			siteURL: "http://example.jp/",
			want:    []gemini.PageLink{{URL: "http://example.jp/live/", Label: "ライブ情報"}},
		},
		{
			name: "official site on a blocked platform keeps its own links",
			pages: map[string]http.HandlerFunc{
				"sites.google.com": htmlPage(link("/view/artist/live", "LIVE") + link("https://www.youtube.com/@artist", "YouTube")),
			},
			siteURL: "http://sites.google.com/view/artist/",
			want:    []gemini.PageLink{{URL: "http://sites.google.com/view/artist/live", Label: "LIVE"}},
		},
		{
			name: "long labels cut and over-long URLs left out",
			pages: map[string]http.HandlerFunc{
				"example.jp": htmlPage(link("/news/1", strings.Repeat("あ", 150)) +
					link("/r?u="+strings.Repeat("x", 600), "tracking")),
			},
			siteURL: "http://example.jp/",
			want:    []gemini.PageLink{{URL: "http://example.jp/news/1", Label: strings.Repeat("あ", 100)}},
		},
		{
			name:    "unsupported scheme",
			pages:   map[string]http.HandlerFunc{},
			siteURL: "ftp://vaundy.jp/",
			wantErr: assert.AnError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := newSiteClient(t, tt.pages)

			got, read, err := gemini.OfficialPageLinks(context.Background(), client, tt.siteURL)

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
			if tt.wantRead != "" {
				assert.Equal(t, tt.wantRead, read)
			}
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

	got, _, err := gemini.OfficialPageLinks(context.Background(), client, "http://vaundy.jp/")

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

			got, _, err := gemini.OfficialPageLinks(context.Background(), gemini.NewOfficialPageClient(), tt.siteURL)

			assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
			assert.Empty(t, got)
		})
	}
	t.Cleanup(func() {
		assert.Zero(t, hits.Load(), "a refused address is never requested")
	})
}
