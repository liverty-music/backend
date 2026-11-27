package gemini

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// Page kinds and read outcomes of an OfficialPageCheck.
//
// TODO(measure-official-page-change-detection): remove with the
// official-page-check mode after the measurement.
const (
	PageKindTop    = "top"
	PageKindLinked = "linked"

	PageStatusOK         = "ok"
	PageStatusHTTPError  = "http_error"
	PageStatusFetchError = "fetch_error"
	PageStatusNotHTML    = "not_html"
)

// OfficialPageCheck is one read of an official page for the measurement of
// whether official pages change before new concerts are found (OpenSpec
// change measure-official-page-change-detection).
type OfficialPageCheck struct {
	// URL is the URL the page was read from, after any redirect or www.
	// fallback, or the requested URL when no page was read.
	URL string
	// Kind is PageKindTop or PageKindLinked.
	Kind string
	// Status is one of the PageStatus constants.
	Status string
	// HTTPStatus is the status the page answered; 0 when no response came.
	HTTPStatus int
	// TextSHA256 is the hex SHA-256 of the page's visible text and
	// TextLength its length in characters; both are empty unless Status is
	// PageStatusOK.
	TextSHA256 string
	TextLength int
}

// CheckOfficialPages reads the official site's top page as the concert
// search does, then each link the search would list from it, one page after
// another, and returns one check per page, the top page first. The links are
// read only when the top page was; each is read with the same client and
// guards, and its own registrable domain is its site's. A page that cannot
// be read is a check with its status, never an error. Cancelling ctx stops
// before the next link.
func CheckOfficialPages(ctx context.Context, client *http.Client, siteURL string) []OfficialPageCheck {
	top, err := readOfficialTopPage(ctx, client, siteURL)
	if err != nil {
		return []OfficialPageCheck{failedCheck(siteURL, PageKindTop, err)}
	}
	checks := []OfficialPageCheck{okCheck(top, PageKindTop)}
	for _, link := range top.Links {
		if ctx.Err() != nil {
			break
		}
		checks = append(checks, checkLinkedPage(ctx, client, link.URL))
	}
	return checks
}

// checkLinkedPage reads one listed link.
func checkLinkedPage(ctx context.Context, client *http.Client, link string) OfficialPageCheck {
	u, err := url.Parse(link)
	if err != nil {
		return failedCheck(link, PageKindLinked, err)
	}
	page, err := fetchOfficialPage(ctx, client, u)
	if err != nil {
		return failedCheck(link, PageKindLinked, err)
	}
	return okCheck(page, PageKindLinked)
}

func okCheck(page officialPage, kind string) OfficialPageCheck {
	sum, length := textDigest(page.Text)
	return OfficialPageCheck{
		URL:        page.URL,
		Kind:       kind,
		Status:     PageStatusOK,
		HTTPStatus: page.HTTPStatus,
		TextSHA256: sum,
		TextLength: length,
	}
}

// failedCheck returns the check of the page at requested that could not be
// read because of err.
func failedCheck(requested, kind string, err error) OfficialPageCheck {
	c := OfficialPageCheck{URL: requested, Kind: kind, Status: PageStatusFetchError}
	if se, ok := errors.AsType[*pageStatusError](err); ok {
		c.Status, c.HTTPStatus = PageStatusHTTPError, se.status
	} else if ne, ok := errors.AsType[*notHTMLError](err); ok {
		c.Status, c.HTTPStatus = PageStatusNotHTML, ne.status
	}
	return c
}
