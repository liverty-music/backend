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
	"sync"
	"syscall"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
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

// errNonPublicAddress returns the FailedPrecondition error of a fetch that
// would dial ip, a loopback, private, link-local, unspecified or multicast
// address.
func errNonPublicAddress(ip netip.Addr) error {
	return apperr.New(codes.FailedPrecondition, "refused to dial a non-public address: "+ip.String())
}

// errOffSiteRedirect returns the FailedPrecondition error of an official page that
// redirected to host, of another registrable domain than the official site's.
func errOffSiteRedirect(host string) error {
	return apperr.New(codes.FailedPrecondition, "official page redirected to another domain: "+host)
}

// errTooManyRedirects returns the FailedPrecondition error of an official page that
// redirected more than officialPageMaxRedirects times.
func errTooManyRedirects() error {
	return apperr.New(codes.FailedPrecondition, "official page redirected too many times")
}

// linkProfile selects the links of an official top page for one searcher.
// Tiers are keyword lists in priority order: a link belongs to the first tier
// with a keyword its path contains, and the cap is filled tier by tier so a
// lower tier never displaces a higher one.
//
// The first follow links of the first tier are then read too, and their
// links that the top page does not carry are appended, whatever their path,
// until total links are listed.
type linkProfile struct {
	tiers  [][]string
	max    int
	follow int
	total  int
}

// concertSearchProfile selects the pages for ConcertSearcher: concert pages
// first, then news indexes and articles. News pages announce tours whose own
// pages sit on another domain (e.g. go!go!vanillas), and in evaluation the
// news tier cut search queries by a further 13% against concert pages alone.
//
// URL context does not follow links, so the tour detail pages behind a live
// list page were found by searching (calls over 20 queries used 48% of the
// queries in prod, 2026-10-21..11-20). The first 3 concert pages are read for
// those links; news pages are not, as an index links dozens of unrelated
// articles. 20 is URL context's limit of URLs per request.
var concertSearchProfile = linkProfile{
	tiers:  [][]string{{"live", "schedule", "tour", "concert", "show"}, {"news"}},
	max:    8,
	follow: 3,
	total:  20,
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
				return errTooManyRedirects()
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
		return errNonPublicAddress(ip)
	}
	return nil
}

// officialPageLinks reads the official site's top page and returns the
// links profile selects, at most profile.max of them, followed by the links
// of the first profile.follow first-tier pages that the top page does not
// carry, at most profile.total links in all. When the site's www. host does
// not resolve, the apex host is read instead, once. An error means the top
// page could not be read; callers search without links. A first-tier page
// that cannot be read is skipped.
func officialPageLinks(ctx context.Context, client *http.Client, siteURL string, profile linkProfile) ([]string, error) {
	page, err := url.Parse(siteURL)
	if err != nil {
		return nil, err
	}
	if page.Scheme != "http" && page.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme %q", page.Scheme)
	}

	domain := registrableDomain(page.Hostname())
	hrefs, final, err := fetchPageHrefs(ctx, client, page, domain)
	if dnsErr, ok := errors.AsType[*net.DNSError](err); ok && dnsErr.IsNotFound && strings.HasPrefix(page.Hostname(), "www.") {
		apex := *page
		apex.Host = strings.TrimPrefix(page.Host, "www.")
		page = &apex
		domain = registrableDomain(page.Hostname())
		hrefs, final, err = fetchPageHrefs(ctx, client, page, domain)
	}
	if err != nil {
		return nil, err
	}

	top := selectLinks(hrefs, final, domain, profile)
	if profile.follow == 0 || len(top.followed) == 0 {
		return top.links, nil
	}

	// The top page itself and every page it links to are already known to
	// the model or are site navigation repeated on every page.
	known := top.all
	for _, u := range []*url.URL{page, final} {
		if link, ok := normalizeLink(u.String(), u, domain); ok {
			known[link] = struct{}{}
		}
	}
	return append(top.links, followedLinks(ctx, client, top.followed, domain, known, profile.total-len(top.links))...), nil
}

// followedLinks reads pages concurrently and returns their links to domain
// that are not in known, de-duplicated, in page order, page by page, at most
// limit of them. A page that cannot be read is skipped.
func followedLinks(ctx context.Context, client *http.Client, pages []string, domain string, known map[string]struct{}, limit int) []string {
	type result struct {
		hrefs []string
		base  *url.URL
	}
	results := make([]result, len(pages))
	var wg sync.WaitGroup
	for i, p := range pages {
		wg.Go(func() {
			u, err := url.Parse(p)
			if err != nil {
				return
			}
			hrefs, final, err := fetchPageHrefs(ctx, client, u, domain)
			if err != nil {
				return
			}
			results[i] = result{hrefs: hrefs, base: final}
		})
	}
	wg.Wait()

	var links []string
	for _, r := range results {
		for _, href := range r.hrefs {
			if len(links) >= limit {
				return links
			}
			link, ok := normalizeLink(href, r.base, domain)
			if !ok {
				continue
			}
			if _, dup := known[link]; dup {
				continue
			}
			known[link] = struct{}{}
			links = append(links, link)
		}
	}
	return links
}

// fetchPageHrefs fetches page and returns the href of every <a> tag in its
// first officialPageMaxBytes, with the URL the page was served from after
// redirects. A page served from another registrable domain than domain is
// refused.
func fetchPageHrefs(ctx context.Context, client *http.Client, page *url.URL, domain string) ([]string, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, page.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", officialPageUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, nil, fmt.Errorf("official page %s answered %d", page, resp.StatusCode)
	}
	if mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err != nil ||
		(mediaType != "text/html" && mediaType != "application/xhtml+xml") {
		return nil, nil, fmt.Errorf("official page %s is not HTML: %q", page, resp.Header.Get("Content-Type"))
	}
	final := resp.Request.URL
	if registrableDomain(final.Hostname()) != domain {
		return nil, nil, errOffSiteRedirect(final.Host)
	}

	return anchorHrefs(io.LimitReader(resp.Body, officialPageMaxBytes)), final, nil
}

// anchorHrefs returns the href attribute of every <a> tag of the HTML, in
// document order.
func anchorHrefs(r io.Reader) []string {
	var hrefs []string
	z := html.NewTokenizer(r)
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return hrefs
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
				hrefs = append(hrefs, string(val))
			}
			if !more {
				break
			}
		}
	}
}

// topPageLinks is what selectLinks takes from a top page.
type topPageLinks struct {
	// links are the selected links, filled tier by tier up to profile.max.
	links []string
	// followed are the first profile.follow selected links of the first
	// tier, whose pages are read for their own links.
	followed []string
	// all holds every normalized link of the page to domain, selected or not.
	all map[string]struct{}
}

// selectLinks takes the hrefs of an HTML page that point to domain, resolved
// against base and normalized: the links matching profile, de-duplicated and
// filled tier by tier in page order up to profile.max, the first-tier links
// among them to follow, and the set of all of them.
func selectLinks(hrefs []string, base *url.URL, domain string, profile linkProfile) topPageLinks {
	all := make(map[string]struct{})
	byTier := make([][]string, len(profile.tiers))

	for _, href := range hrefs {
		link, ok := normalizeLink(href, base, domain)
		if !ok {
			continue
		}
		if _, dup := all[link]; dup {
			continue
		}
		all[link] = struct{}{}
		if tier := linkTier(link, profile); tier >= 0 {
			byTier[tier] = append(byTier[tier], link)
		}
	}

	top := topPageLinks{all: all}
	for i, tier := range byTier {
		for _, link := range tier {
			if len(top.links) == profile.max {
				return top
			}
			top.links = append(top.links, link)
			if i == 0 && len(top.followed) < profile.follow {
				top.followed = append(top.followed, link)
			}
		}
	}
	return top
}

// linkTier returns the index of the first tier of profile with a keyword the
// path of link contains, or -1.
func linkTier(link string, profile linkProfile) int {
	u, err := url.Parse(link)
	if err != nil {
		return -1
	}
	path := strings.ToLower(u.Path)
	return slices.IndexFunc(profile.tiers, func(keywords []string) bool {
		return slices.ContainsFunc(keywords, func(kw string) bool { return strings.Contains(path, kw) })
	})
}

// normalizeLink resolves href against base and returns the normalized link
// when it is an http(s) link to domain. Normalizing drops the fragment, a
// default port and the lang / _normalbrowse_* query parameters.
func normalizeLink(href string, base *url.URL, domain string) (string, bool) {
	u, err := base.Parse(strings.TrimSpace(href))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	if registrableDomain(u.Hostname()) != domain {
		return "", false
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
	return u.String(), true
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
