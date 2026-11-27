package gemini_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTMLTextDigest(t *testing.T) {
	t.Parallel()

	base := `<html><head><title>Band</title></head><body><h1>TOUR 2027</h1><p>Tokyo &amp; Osaka</p></body></html>`
	baseSum, _ := gemini.HTMLTextDigest(base)

	same := []struct {
		name string
		page string
	}{
		{"different script", `<html><head><title>Band</title><script>var token="b81f";</script></head><body><h1>TOUR 2027</h1><p>Tokyo &amp; Osaka</p></body></html>`},
		{"different style", `<html><head><title>Band</title><style>h1{color:red}</style></head><body><h1>TOUR 2027</h1><p>Tokyo &amp; Osaka</p></body></html>`},
		{"different noscript", `<html><head><title>Band</title></head><body><noscript>Enable JavaScript</noscript><h1>TOUR 2027</h1><p>Tokyo &amp; Osaka</p></body></html>`},
		{"different template", `<html><head><title>Band</title></head><body><template><p>card</p></template><h1>TOUR 2027</h1><p>Tokyo &amp; Osaka</p></body></html>`},
		{"different svg", `<html><head><title>Band</title></head><body><svg viewBox="0 0 1 1"><title>logo</title><text>x</text></svg><h1>TOUR 2027</h1><p>Tokyo &amp; Osaka</p></body></html>`},
		{"different whitespace", "<html>\n<head><title> Band </title></head>\n<body>\n  <h1>TOUR\n\t2027</h1>\n  <p>Tokyo   &amp;   Osaka</p>\n</body></html>"},
		{"different attributes and nonce", `<html><head><title>Band</title></head><body class="x" data-nonce="9a"><h1 id="t">TOUR 2027</h1><p>Tokyo &amp; Osaka</p></body></html>`},
	}
	for _, tt := range same {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sum, _ := gemini.HTMLTextDigest(tt.page)
			assert.Equal(t, baseSum, sum)
		})
	}

	t.Run("visible text change", func(t *testing.T) {
		t.Parallel()
		sum, _ := gemini.HTMLTextDigest(`<html><head><title>Band</title></head><body><h1>TOUR 2027</h1><p>Tokyo &amp; Nagoya</p></body></html>`)
		assert.NotEqual(t, baseSum, sum)
	})

	t.Run("hash of the normalized text", func(t *testing.T) {
		t.Parallel()
		sum, length := gemini.HTMLTextDigest(base)
		want := sha256.Sum256([]byte("Band TOUR 2027 Tokyo & Osaka"))
		assert.Equal(t, hex.EncodeToString(want[:]), sum)
		assert.Equal(t, 28, length)
	})

	t.Run("length counts characters, not bytes", func(t *testing.T) {
		t.Parallel()
		_, length := gemini.HTMLTextDigest(`<p>東京公演</p>`)
		assert.Equal(t, 4, length)
	})
}

func TestCheckOfficialPages(t *testing.T) {
	t.Parallel()

	t.Run("top page with two listed links", func(t *testing.T) {
		t.Parallel()
		client := newSiteClient(t, map[string]http.HandlerFunc{
			"example.jp": func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/":
					htmlPage(link("/live", "LIVE")+link("http://tour.example.com/", "TOUR"))(w, r)
				case "/live":
					htmlPage("<p>2027.01.10 Zepp</p>")(w, r)
				default:
					http.NotFound(w, r)
				}
			},
			"tour.example.com": htmlPage("<p>Arena tour</p>"),
		})

		checks := gemini.CheckOfficialPages(context.Background(), client, "http://example.jp/")

		require.Len(t, checks, 3)
		assert.Equal(t, gemini.OfficialPageCheck{URL: "http://example.jp/", Kind: gemini.PageKindTop, Status: gemini.PageStatusOK, HTTPStatus: 200,
			TextSHA256: digest(t, "<!doctype html><html><body>"+link("/live", "LIVE")+link("http://tour.example.com/", "TOUR")+"</body></html>"), TextLength: len("LIVE TOUR")}, checks[0])
		assert.Equal(t, gemini.OfficialPageCheck{URL: "http://example.jp/live", Kind: gemini.PageKindLinked, Status: gemini.PageStatusOK, HTTPStatus: 200,
			TextSHA256: digest(t, "<p>2027.01.10 Zepp</p>"), TextLength: len("2027.01.10 Zepp")}, checks[1])
		assert.Equal(t, gemini.OfficialPageCheck{URL: "http://tour.example.com/", Kind: gemini.PageKindLinked, Status: gemini.PageStatusOK, HTTPStatus: 200,
			TextSHA256: digest(t, "<p>Arena tour</p>"), TextLength: len("Arena tour")}, checks[2])
	})

	t.Run("a 404 link and an unreachable link do not stop the check", func(t *testing.T) {
		t.Parallel()
		client := newSiteClient(t, map[string]http.HandlerFunc{
			"example.jp": func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/":
					htmlPage(link("/gone", "GONE")+link("http://down.example.com/", "DOWN")+link("/news", "NEWS")+link("/flyer", "FLYER"))(w, r)
				case "/news":
					htmlPage("<p>news</p>")(w, r)
				case "/flyer":
					w.Header().Set("Content-Type", "application/pdf")
					_, _ = w.Write([]byte("%PDF-1.7"))
				default:
					http.NotFound(w, r)
				}
			},
		})

		checks := gemini.CheckOfficialPages(context.Background(), client, "http://example.jp/")

		require.Len(t, checks, 5)
		assert.Equal(t, gemini.PageStatusOK, checks[0].Status)
		assert.Equal(t, gemini.OfficialPageCheck{URL: "http://example.jp/gone", Kind: gemini.PageKindLinked, Status: gemini.PageStatusHTTPError, HTTPStatus: 404}, checks[1])
		assert.Equal(t, gemini.OfficialPageCheck{URL: "http://down.example.com/", Kind: gemini.PageKindLinked, Status: gemini.PageStatusFetchError}, checks[2])
		assert.Equal(t, gemini.PageStatusOK, checks[3].Status)
		assert.Equal(t, "http://example.jp/news", checks[3].URL)
		assert.Equal(t, gemini.OfficialPageCheck{URL: "http://example.jp/flyer", Kind: gemini.PageKindLinked, Status: gemini.PageStatusNotHTML, HTTPStatus: 200}, checks[4])
	})

	t.Run("an unreadable top page is one check and no links", func(t *testing.T) {
		t.Parallel()
		client := newSiteClient(t, map[string]http.HandlerFunc{
			"example.jp": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) },
		})

		checks := gemini.CheckOfficialPages(context.Background(), client, "http://example.jp/")

		assert.Equal(t, []gemini.OfficialPageCheck{{URL: "http://example.jp/", Kind: gemini.PageKindTop, Status: gemini.PageStatusHTTPError, HTTPStatus: 403}}, checks)
	})

	t.Run("the top page reports the URL it was read from", func(t *testing.T) {
		t.Parallel()
		client := newSiteClient(t, map[string]http.HandlerFunc{
			"example.jp": htmlPage("<p>home</p>"),
		})

		checks := gemini.CheckOfficialPages(context.Background(), client, "http://www.example.jp/")

		require.Len(t, checks, 1)
		assert.Equal(t, "http://example.jp/", checks[0].URL)
		assert.Equal(t, gemini.PageStatusOK, checks[0].Status)
	})
}

// digest returns the SHA-256 of the visible text of page.
func digest(t *testing.T, page string) string {
	t.Helper()
	sum, _ := gemini.HTMLTextDigest(page)
	return sum
}
