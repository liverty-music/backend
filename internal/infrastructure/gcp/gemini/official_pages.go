package gemini

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
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

// errOffSiteRedirect returns the FailedPrecondition error of a top page that
// redirected to host, of another registrable domain than the official site's.
func errOffSiteRedirect(host string) error {
	return apperr.New(codes.FailedPrecondition, "official top page redirected to another domain: "+host)
}

// errTooManyRedirects returns the FailedPrecondition error of a top page that
// redirected more than officialPageMaxRedirects times.
func errTooManyRedirects() error {
	return apperr.New(codes.FailedPrecondition, "official top page redirected too many times")
}

// pageLink is one link of the official top page listed in the prompt: its
// URL and its label, the link text or else the alternative text of an image
// inside the link.
type pageLink struct {
	URL   string
	Label string
}

// maxPageLinks caps the links listed from one top page. After the two
// exclusions below, 80 production top pages carried at most 58 links
// (median 22, 2026-11-21); the cap drops links from the end of the page,
// where the footer is, if a page carries more.
const maxPageLinks = 60

// maxLabelRunes caps a label: a card-style link makes every text inside it
// the label, and page text is untrusted, so only its start goes into the
// prompt. maxLinkURLLen leaves out links whose URL is longer, such as
// tracking redirects.
const (
	maxLabelRunes = 100
	maxLinkURLLen = 500
)

// blockedLinkDomains are registrable domains that never carry an artist's
// official show announcements: social networks, music streaming, link hubs,
// shops, ticket vendors, fan-club and login platforms, and lyrics sites,
// taken from the links of 80 production official top pages (2026-11-21).
// Record labels, management companies and artist-owned domains are not
// blocked: some artists' official pages live there. A domain missing here
// only adds a line to the prompt; the model decides which links announce
// shows.
var blockedLinkDomains = setOf(
	// Social networks and video.
	"instagram.com", "youtube.com", "youtu.be", "youtube-nocookie.com", "twitter.com", "x.com", "tiktok.com",
	"facebook.com", "line.me", "lin.ee", "weibo.com", "threads.net",
	// Music streaming and stores.
	"apple.com", "itunes.com", "spotify.com", "soundcloud.com", "tidal.com", "amazon.com", "amazon.co.jp",
	"amzn.to", "bandcamp.com", "deezer.com",
	// Link hubs.
	"lnk.to", "ffm.to", "linkco.re", "bio.to", "linktr.ee", "lit.link", "lsnto.me",
	// Merchandise shops.
	"official-goods-store.jp", "stores.jp", "shop-pro.jp", "official.ec", "official-store.jp", "theshop.jp",
	"thebase.in", "gum.co", "printful.com",
	// Ticket vendors.
	"eplus.jp", "pia.jp", "l-tike.com", "ticketmaster.com", "livenation.com", "tixplus.jp", "dice.fm", "laylo.com",
	// Fan-club, login and site platforms.
	"plusmember.jp", "fanplus.co.jp", "skiyaki.com", "bitfan.id", "weverse.io", "google.com",
	// Lyrics.
	"uta-net.com", "lyrical-nonsense.com",
)

// siteInfoPathSegments are path segments that name an official site's own
// site-information, legal, artist-profile, discography or membership pages,
// which every site links from its navigation and none uses for show
// announcements. On 80 production top pages they matched 318 links and no
// show page (2026-11-21). "entry" is left out: some blogs use it for
// articles.
var siteInfoPathSegments = setOf(
	"privacy", "privacy-policy", "terms", "deals", "law", "customer_harassment",
	"about", "device", "environment", "faq", "qa", "help", "contact", "sitemap",
	"profile", "profiles", "biography", "bio", "discography", "disc", "company",
	"signup", "signin", "login", "membership", "account", "payment", "cart", "wallpaper",
)

func setOf(items ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(items))
	for _, it := range items {
		m[it] = struct{}{}
	}
	return m
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

// officialPage is what one read of an official page gives: the links to
// list in the prompt (see listedLinks), the URL the page was read from, the
// status it answered and its visible text (see scanPage).
type officialPage struct {
	Links      []pageLink
	URL        string
	HTTPStatus int
	Text       string
}

// pageStatusError is the error of a page that answered a status outside
// 2xx.
type pageStatusError struct{ status int }

func (e *pageStatusError) Error() string {
	return fmt.Sprintf("official top page answered %d", e.status)
}

// notHTMLError is the error of a page that answered 2xx with a body that is
// not HTML.
type notHTMLError struct {
	status      int
	contentType string
}

func (e *notHTMLError) Error() string {
	return fmt.Sprintf("official top page is not HTML: %q", e.contentType)
}

// officialPageLinks reads the official site's top page and returns the links
// to list in the prompt (see listedLinks) and the URL the page was read
// from. When the site's www. host does not resolve, the apex host is read
// instead, once. An error means the page could not be read; callers search
// without links.
func officialPageLinks(ctx context.Context, client *http.Client, siteURL string) ([]pageLink, string, error) {
	page, err := readOfficialTopPage(ctx, client, siteURL)
	return page.Links, page.URL, err
}

// readOfficialTopPage reads the official site's top page as
// officialPageLinks describes and returns everything the read gives.
func readOfficialTopPage(ctx context.Context, client *http.Client, siteURL string) (officialPage, error) {
	page, err := url.Parse(siteURL)
	if err != nil {
		return officialPage{}, err
	}
	if page.Scheme != "http" && page.Scheme != "https" {
		return officialPage{}, fmt.Errorf("unsupported scheme %q", page.Scheme)
	}

	read, err := fetchOfficialPage(ctx, client, page)
	if dnsErr, ok := errors.AsType[*net.DNSError](err); ok && dnsErr.IsNotFound && strings.HasPrefix(page.Hostname(), "www.") {
		apex := *page
		apex.Host = strings.TrimPrefix(page.Host, "www.")
		return fetchOfficialPage(ctx, client, &apex)
	}
	return read, err
}

// fetchOfficialPage fetches page and returns its listed links, the URL it
// was served from, its status and its visible text, from one parse of the
// body. The registrable domain of page, not of a redirect target, is the
// site's domain. A status outside 2xx is a *pageStatusError and a body that
// is not HTML a *notHTMLError.
func fetchOfficialPage(ctx context.Context, client *http.Client, page *url.URL) (officialPage, error) {
	domain := registrableDomain(page.Hostname())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, page.String(), nil)
	if err != nil {
		return officialPage{}, err
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", officialPageUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return officialPage{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return officialPage{}, &pageStatusError{status: resp.StatusCode}
	}
	if mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err != nil ||
		(mediaType != "text/html" && mediaType != "application/xhtml+xml") {
		return officialPage{}, &notHTMLError{status: resp.StatusCode, contentType: resp.Header.Get("Content-Type")}
	}
	final := resp.Request.URL
	if registrableDomain(final.Hostname()) != domain {
		return officialPage{}, errOffSiteRedirect(final.Host)
	}

	// Labels are text the model reads, so the page is decoded to UTF-8 from
	// its declared charset (Content-Type, then <meta charset>): older
	// Japanese artist sites serve Shift_JIS or EUC-JP.
	body, err := charset.NewReader(io.LimitReader(resp.Body, officialPageMaxBytes), resp.Header.Get("Content-Type"))
	if err != nil {
		return officialPage{}, fmt.Errorf("official top page charset: %w", err)
	}
	scan := scanPage(body)
	return officialPage{
		Links:      listedLinks(scan.anchors, final, domain),
		URL:        final.String(),
		HTTPStatus: resp.StatusCode,
		Text:       scan.text,
	}, nil
}

// anchor is one <a> tag: its href and its label.
type anchor struct {
	href  string
	label string
}

// pageScan is what one pass over an HTML page collects: its <a> tags and
// its visible text.
type pageScan struct {
	anchors []anchor
	text    string
}

// scanPage reads the HTML once and returns, in document order, its <a> tags,
// each with its href and its label (the text inside the tag, or else the
// alternative text of the first image inside it, with whitespace collapsed),
// and its visible text (see textScanner).
func scanPage(r io.Reader) pageScan {
	var sc anchorScanner
	var tx textScanner
	z := html.NewTokenizer(r)
	for {
		switch tt := z.Next(); tt {
		case html.ErrorToken:
			return pageScan{anchors: sc.out, text: tx.String()}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			sc.startTag(z, string(name), hasAttr)
			if tt == html.StartTagToken {
				tx.startTag(string(name))
			}
		case html.TextToken:
			// Text, like TagName, returns the token's bytes only once.
			text := z.Text()
			sc.text(text)
			tx.text(text)
		case html.EndTagToken:
			name, _ := z.TagName()
			if string(name) == "a" {
				sc.endAnchor()
			}
			tx.endTag(string(name))
		}
	}
}

// hiddenTextTags are the elements whose content is not visible page text:
// scripts, styles, the fallback of disabled scripts, templates and inline
// SVG drawings.
var hiddenTextTags = setOf("script", "style", "noscript", "template", "svg")

// textScanner collects the visible text of a page: the text nodes outside
// hiddenTextTags, with entities decoded by the tokenizer, joined by a space,
// with every run of whitespace collapsed to one space.
type textScanner struct {
	hiddenDepth int
	buf         strings.Builder
}

func (tx *textScanner) startTag(name string) {
	if _, hidden := hiddenTextTags[name]; hidden {
		tx.hiddenDepth++
	}
}

func (tx *textScanner) endTag(name string) {
	if _, hidden := hiddenTextTags[name]; hidden && tx.hiddenDepth > 0 {
		tx.hiddenDepth--
	}
}

func (tx *textScanner) text(b []byte) {
	if tx.hiddenDepth == 0 {
		tx.buf.Write(b)
		tx.buf.WriteByte(' ')
	}
}

// String returns the collected text with whitespace collapsed.
func (tx *textScanner) String() string {
	return strings.Join(strings.Fields(tx.buf.String()), " ")
}

// textDigest returns the hex SHA-256 of a page's visible text and its length
// in characters.
func textDigest(text string) (string, int) {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:]), utf8.RuneCountInString(text)
}

// anchorScanner collects anchors from a tokenizer's events.
type anchorScanner struct {
	out       []anchor
	cur       *anchor
	textBuf   strings.Builder
	altBuf    strings.Builder
	altFilled bool
}

// startTag opens an anchor at <a> and records the first image's alt text
// inside an open anchor. name and hasAttr are what z.TagName returned for the
// current tag.
func (sc *anchorScanner) startTag(z *html.Tokenizer, name string, hasAttr bool) {
	switch {
	case name == "a":
		sc.cur = &anchor{href: tagAttr(z, hasAttr, "href")}
		sc.textBuf.Reset()
		sc.altBuf.Reset()
		sc.altFilled = false
	case name == "img" && sc.cur != nil && !sc.altFilled:
		if alt := tagAttr(z, hasAttr, "alt"); alt != "" {
			sc.altBuf.WriteString(alt)
			sc.altFilled = true
		}
	}
}

// text adds text inside an open anchor to its label.
func (sc *anchorScanner) text(b []byte) {
	if sc.cur != nil {
		sc.textBuf.Write(b)
		sc.textBuf.WriteByte(' ')
	}
}

// endAnchor closes the open anchor, labeling it with its text or else its
// image's alt text.
func (sc *anchorScanner) endAnchor() {
	if sc.cur == nil {
		return
	}
	sc.cur.label = strings.Join(strings.Fields(sc.textBuf.String()), " ")
	if sc.cur.label == "" {
		sc.cur.label = strings.Join(strings.Fields(sc.altBuf.String()), " ")
	}
	sc.out = append(sc.out, *sc.cur)
	sc.cur = nil
}

// tagAttr returns the value of the current tag's attribute key, or "".
func tagAttr(z *html.Tokenizer, hasAttr bool, key string) string {
	for hasAttr {
		var k, v []byte
		k, v, hasAttr = z.TagAttr()
		if string(k) == key {
			return string(v)
		}
	}
	return ""
}

// listedLinks returns the links of an HTML page to list in the prompt:
// http(s) links resolved against base and normalized, each once, in page
// order, at most maxPageLinks, each label cut to maxLabelRunes. It leaves
// out links to the page itself (an <a> without href or with href="#"),
// URLs longer than maxLinkURLLen, links to blockedLinkDomains other than
// the official site's own (an official site on a blocked platform's domain,
// such as sites.google.com, keeps its links), and links to pages of domain
// whose path has a siteInfoPathSegments segment. A link that appears again
// keeps its first label unless that was empty.
func listedLinks(as []anchor, base *url.URL, domain string) []pageLink {
	var links []pageLink
	index := make(map[string]int)
	self := ""
	if u, ok := normalizeLink("", base); ok {
		self = u.String()
	}
	for _, a := range as {
		u, ok := normalizeLink(a.href, base)
		if !ok || u.String() == self || len(u.String()) > maxLinkURLLen {
			continue
		}
		linkDomain := registrableDomain(u.Hostname())
		if _, blocked := blockedLinkDomains[linkDomain]; blocked && linkDomain != domain {
			continue
		}
		if linkDomain == domain && hasSiteInfoSegment(u.Path) {
			continue
		}
		link, label := u.String(), truncateRunes(a.label, maxLabelRunes)
		if i, dup := index[link]; dup {
			if links[i].Label == "" {
				links[i].Label = label
			}
			continue
		}
		if len(links) == maxPageLinks {
			break
		}
		index[link] = len(links)
		links = append(links, pageLink{URL: link, Label: label})
	}
	return links
}

// truncateRunes returns s cut to at most n runes.
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// hasSiteInfoSegment reports whether a path segment, without a .html, .htm
// or .php extension, is one of siteInfoPathSegments.
func hasSiteInfoSegment(path string) bool {
	for seg := range strings.SplitSeq(strings.ToLower(path), "/") {
		seg = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(seg, ".html"), ".htm"), ".php")
		if _, ok := siteInfoPathSegments[seg]; ok {
			return true
		}
	}
	return false
}

// normalizeLink resolves href against base and returns it normalized when it
// is an http(s) link. Normalizing drops the fragment, a default port and the
// lang / _normalbrowse_* query parameters.
func normalizeLink(href string, base *url.URL) (*url.URL, bool) {
	u, err := base.Parse(strings.TrimSpace(href))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, false
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
	return u, true
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
