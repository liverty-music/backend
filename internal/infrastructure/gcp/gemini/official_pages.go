package gemini

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/net/html"
	"golang.org/x/net/publicsuffix"
)

const (
	// officialPageTimeout bounds one fetch of an official top page, body
	// included. Sampled top pages answered within 2.5 s.
	officialPageTimeout = 5 * time.Second

	// officialPageMaxBytes caps the part of a top page that is read and
	// parsed. Navigation links sit at the top; sampled pages were at most
	// 63 KB.
	officialPageMaxBytes = 2 << 20

	// officialPageMaxRedirects caps the redirects of one fetch.
	officialPageMaxRedirects = 3

	officialPageUserAgent = "LivertyMusic/1.0 (+https://liverty-music.app)"
)

var (
	// errNonPublicAddress is returned when a fetch would dial a loopback,
	// private, link-local, unspecified or multicast address.
	errNonPublicAddress = errors.New("refused to dial a non-public address")

	// errOffSiteRedirect is returned when the top page redirects to another
	// registrable domain than the official site's.
	errOffSiteRedirect = errors.New("official top page redirected to another domain")

	errTooManyRedirects = errors.New("official top page redirected too many times")
)

// linkProfile selects the links of an official top page for one searcher.
// Tiers are keyword lists in priority order: a link belongs to the first tier
// with a keyword its path contains, and the cap is filled tier by tier so a
// lower tier never displaces a higher one.
type linkProfile struct {
	tiers [][]string
	max   int
}

// concertSearchProfile selects the pages for ConcertSearcher: concert pages
// first, then news indexes and articles. News pages announce tours whose own
// pages sit on another domain (e.g. go!go!vanillas), and in evaluation the
// news tier cut search queries by a further 13% against concert pages alone.
var concertSearchProfile = linkProfile{
	tiers: [][]string{{"live", "schedule", "tour", "concert", "show"}, {"news"}},
	max:   8,
}

// NewOfficialPageClient returns the HTTP client for official top pages. It
// refuses to dial non-public addresses at dial time, so DNS answers and
// redirects are checked too, ignores proxy settings so the guard checks the
// site itself, follows at most 3 redirects and gives up after 5 s.
func NewOfficialPageClient() *http.Client {
	dialer := &net.Dialer{Timeout: officialPageTimeout, Control: refuseNonPublicAddress}
	return newOfficialPageClient(dialer.DialContext)
}

func newOfficialPageClient(dial func(ctx context.Context, network, addr string) (net.Conn, error)) *http.Client {
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dial,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   officialPageTimeout,
		ResponseHeaderTimeout: officialPageTimeout,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
	}
	return &http.Client{
		Transport: otelhttp.NewTransport(transport),
		Timeout:   officialPageTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// via holds the requests already sent: the original and earlier
			// redirects.
			if len(via) > officialPageMaxRedirects {
				return errTooManyRedirects
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("redirect to unsupported scheme %q", req.URL.Scheme)
			}
			return nil
		},
	}
}

// refuseNonPublicAddress is a net.Dialer Control hook. It runs after DNS
// resolution, on the address actually dialled.
func refuseNonPublicAddress(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return err
	}
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return fmt.Errorf("%w: %s", errNonPublicAddress, ip)
	}
	return nil
}

// officialPageLinks reads the official site's top page and returns the
// links profile selects, at most profile.max of them. When the site's
// www. host does not resolve, the apex host is read instead, once. An error
// means the page could not be read; callers search without links.
func officialPageLinks(ctx context.Context, client *http.Client, siteURL string, profile linkProfile) ([]string, error) {
	page, err := url.Parse(siteURL)
	if err != nil {
		return nil, err
	}
	if page.Scheme != "http" && page.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme %q", page.Scheme)
	}

	links, err := fetchPageLinks(ctx, client, page, profile)
	if dnsErr, ok := errors.AsType[*net.DNSError](err); ok && dnsErr.IsNotFound && strings.HasPrefix(page.Hostname(), "www.") {
		apex := *page
		apex.Host = strings.TrimPrefix(page.Host, "www.")
		return fetchPageLinks(ctx, client, &apex, profile)
	}
	return links, err
}

// fetchPageLinks fetches page and selects its links. The registrable domain
// of page, not of a redirect target, is the official site's domain.
func fetchPageLinks(ctx context.Context, client *http.Client, page *url.URL, profile linkProfile) ([]string, error) {
	domain := registrableDomain(page.Hostname())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, page.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", officialPageUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("official top page answered %d", resp.StatusCode)
	}
	if mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err != nil ||
		(mediaType != "text/html" && mediaType != "application/xhtml+xml") {
		return nil, fmt.Errorf("official top page is not HTML: %q", resp.Header.Get("Content-Type"))
	}
	final := resp.Request.URL
	if registrableDomain(final.Hostname()) != domain {
		return nil, fmt.Errorf("%w: %s", errOffSiteRedirect, final.Host)
	}

	return selectLinks(io.LimitReader(resp.Body, officialPageMaxBytes), final, domain, profile), nil
}

// selectLinks returns the <a href> links of an HTML document that point to
// domain and match profile, resolved against base, normalized, de-duplicated
// and filled tier by tier in page order up to profile.max.
func selectLinks(r io.Reader, base *url.URL, domain string, profile linkProfile) []string {
	seen := make(map[string]struct{})
	byTier := make([][]string, len(profile.tiers))

	z := html.NewTokenizer(r)
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		name, hasAttr := z.TagName()
		if string(name) != "a" || !hasAttr {
			continue
		}
		for {
			key, val, more := z.TagAttr()
			if string(key) == "href" {
				if link, tier, ok := matchLink(string(val), base, domain, profile); ok {
					if _, dup := seen[link]; !dup {
						seen[link] = struct{}{}
						byTier[tier] = append(byTier[tier], link)
					}
				}
			}
			if !more {
				break
			}
		}
	}

	var links []string
	for _, tier := range byTier {
		for _, link := range tier {
			if len(links) == profile.max {
				return links
			}
			links = append(links, link)
		}
	}
	return links
}

// matchLink resolves href against base and returns the normalized link and
// its tier when it is an http(s) link to domain whose path contains a
// keyword of profile. Normalizing drops the fragment, a default port and the
// lang / _normalbrowse_* query parameters.
func matchLink(href string, base *url.URL, domain string, profile linkProfile) (string, int, bool) {
	u, err := base.Parse(strings.TrimSpace(href))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", 0, false
	}
	if registrableDomain(u.Hostname()) != domain {
		return "", 0, false
	}
	path := strings.ToLower(u.Path)
	tier := slices.IndexFunc(profile.tiers, func(keywords []string) bool {
		return slices.ContainsFunc(keywords, func(kw string) bool { return strings.Contains(path, kw) })
	})
	if tier < 0 {
		return "", 0, false
	}

	u.Fragment = ""
	u.RawFragment = ""
	if port := u.Port(); (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		u.Host = u.Hostname()
	}
	if u.RawQuery != "" {
		q := u.Query()
		for key := range q {
			if key == "lang" || strings.HasPrefix(key, "_normalbrowse_") {
				q.Del(key)
			}
		}
		u.RawQuery = q.Encode()
	}
	return u.String(), tier, true
}

// registrableDomain returns the registrable domain (eTLD+1) of host, or host
// itself when it is an IP address or has no public suffix.
func registrableDomain(host string) string {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if _, err := netip.ParseAddr(host); err == nil {
		return host
	}
	d, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return host
	}
	return d
}
