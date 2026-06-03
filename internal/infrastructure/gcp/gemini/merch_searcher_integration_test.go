//go:build integration

package gemini_test

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/pannpers/go-logging/logging"
)

// isAcceptedURL reports whether got exactly matches one of the accepted
// ground-truth URLs, tolerating only a single trailing slash difference. This
// is deliberately strict: a fabricated or dead deep link that merely shares a
// host with an accepted page does NOT match.
func isAcceptedURL(got string, accepted []string) bool {
	norm := func(s string) string { return strings.TrimRight(strings.TrimSpace(s), "/") }
	g := norm(got)
	for _, a := range accepted {
		if g == norm(a) {
			return true
		}
	}
	return false
}

// isOfficialURL matches a resolved URL against host-level ground truth after
// stripping a leading "www.".
//
//   - An entry WITHOUT a slash matches the host OR any subdomain of it: the
//     entry is treated as the official apex domain, so "yorushika.com" matches
//     "store.yorushika.com" (the official store lives on a subdomain) but NOT
//     "evil-yorushika.com" (subdomain match requires a "." boundary).
//   - An entry WITH a slash matches host+path prefix, used to pin a specific
//     official social / shared-EC account (e.g. "x.com/super_beaver" or
//     "store.plusmember.jp/vaundy") where the host alone is shared
//     infrastructure and the path proves ownership.
func isOfficialURL(rawURL string, allow []string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	hostPath := host + u.EscapedPath()
	for _, entry := range allow {
		e := strings.ToLower(strings.TrimPrefix(entry, "www."))
		if strings.Contains(e, "/") {
			if strings.HasPrefix(hostPath, e) {
				return true
			}
		} else if host == e || strings.HasSuffix(host, "."+e) {
			return true
		}
	}
	return false
}

// Live smoke for the merch-url searcher — mirrors the concert searcher's
// build-tagged, env-gated harness. It makes REAL (billed) Gemini API calls, so
// it never runs in the normal `make check` path: it requires both the
// `integration` build tag AND MERCH_EVAL=1.
//
// Run:
//
//	GCP_GEMINI_SEARCH_API_KEY=$(esc env get liverty-music/dev pulumiConfig.geminiSearchApiKey --show-secrets --value string) \
//	MERCH_EVAL=1 \
//	go test -tags integration -run TestMerchSearcher_LiveSmoke -v ./internal/infrastructure/gcp/gemini/
//
// Optional overrides:
//   - GCP_GEMINI_MERCH_MODEL  : model name (default: Flash-Lite via MerchConfig)
//   - MERCH_EVAL_ARTIST       : single ad-hoc artist name (pairs with MERCH_EVAL_SERIES)
//   - MERCH_EVAL_SERIES       : single ad-hoc series title
const (
	merchEvalEnvVar     = "MERCH_EVAL"
	merchAPIKeyEnvVar   = "GCP_GEMINI_SEARCH_API_KEY"
	merchModelEnvVar    = "GCP_GEMINI_MERCH_MODEL"
	merchArtistEnvVar   = "MERCH_EVAL_ARTIST"
	merchSeriesEnvVar   = "MERCH_EVAL_SERIES"
	merchArtistsEnvVar  = "MERCH_EVAL_ARTISTS"  // CSV; intersect default cases by artist name
	merchThinkingEnvVar = "MERCH_EVAL_THINKING" // "", minimal, low, medium, high (default low)
)

type merchCase struct {
	artist string
	series string
	// expectFound is advisory only: the assertion never fails on found/not-found
	// (merch availability is time-dependent and not deterministic). It is logged
	// next to the result so a human can eyeball whether resolution matched
	// expectation for this run.
	expectFound bool
	// acceptedURLs, when set, is the EXACT ground-truth allowlist: a non-empty
	// result must equal (modulo a trailing slash) one of these real, live pages.
	// This is stricter than officialHosts and is what rejects a plausible-but-
	// non-existent deep link (e.g. a fabricated category_id) that merely sits on
	// an official host.
	acceptedURLs []string
	// officialHosts is the looser host-level ground truth, used only when
	// acceptedURLs is empty: a non-empty result must sit on one of these official
	// hosts (or a subdomain). Catches non-official/hallucinated hosts.
	officialHosts []string
}

func TestMerchSearcher_LiveSmoke(t *testing.T) {
	if os.Getenv(merchEvalEnvVar) != "1" {
		t.Skipf("live smoke disabled; set %s=1 (and -tags integration) to run real Gemini calls", merchEvalEnvVar)
	}
	apiKey := os.Getenv(merchAPIKeyEnvVar)
	if apiKey == "" {
		t.Fatalf("%s is required for the live smoke", merchAPIKeyEnvVar)
	}

	logger, err := logging.New()
	if err != nil {
		t.Fatalf("logger: %v", err)
	}

	model := os.Getenv(merchModelEnvVar)
	if model == "" {
		model = "gemini-3.1-flash-lite" // mirrors config.defaultMerchModel
	}
	thinking := os.Getenv(merchThinkingEnvVar) // "", minimal, low, medium, high
	if thinking == "" {
		thinking = "low"
	}

	searcher, err := gemini.NewMerchSearcher(context.Background(), gemini.MerchConfig{
		APIKey:        apiKey,
		Model:         model,
		Temperature:   1.0,
		ThinkingLevel: thinking,
	}, nil, logger)
	if err != nil {
		t.Fatalf("NewMerchSearcher: %v", err)
	}

	cases := defaultMerchCases()
	if a, s := os.Getenv(merchArtistEnvVar), os.Getenv(merchSeriesEnvVar); a != "" && s != "" {
		cases = []merchCase{{artist: a, series: s}}
	} else if filter := strings.TrimSpace(os.Getenv(merchArtistsEnvVar)); filter != "" {
		// Intersect the default cases by artist name (CSV), preserving each
		// case's confirmed officialHosts ground truth.
		want := map[string]bool{}
		for _, name := range strings.Split(filter, ",") {
			want[strings.TrimSpace(name)] = true
		}
		var filtered []merchCase
		for _, c := range cases {
			if want[c.artist] {
				filtered = append(filtered, c)
			}
		}
		cases = filtered
	}

	t.Logf("model=%s | cases=%d", model, len(cases))
	for _, c := range cases {
		c := c
		t.Run(c.artist+" / "+c.series, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
			defer cancel()

			got, err := searcher.SearchMerchURL(ctx, c.artist, c.series)
			if err != nil {
				// A transport/API failure is a real smoke failure (auth, model
				// name, quota, grounding tool availability).
				t.Fatalf("SearchMerchURL error: %v", err)
			}

			t.Logf("artist=%q series=%q expectFound=%v -> result=%q", c.artist, c.series, c.expectFound, got)

			if got == "" {
				// Empty is always a contract-valid outcome (no confident official
				// source). For an expect-found case it is a soft signal worth a
				// human look, but not a hard failure (merch pages are
				// time-dependent and grounding is non-deterministic).
				if c.expectFound {
					t.Logf("⚠ expected an official URL for a real current tour but got empty — review searcher recall")
				}
				return
			}
			// Hard contract: a non-empty value MUST be a valid absolute http(s)
			// URL (same rule validMerchURL enforces before persistence). Catches
			// prose, a bare domain, or a NONE-with-trailing-text the parser missed.
			u, perr := url.ParseRequestURI(got)
			if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				t.Errorf("resolved value is not a valid http(s) URL: %q (%v)", got, perr)
				return
			}
			if strings.EqualFold(strings.TrimSpace(got), "NONE") {
				t.Errorf("parser leaked the NONE sentinel as a URL: %q", got)
			}
			// Hard ground-truth assertion. Prefer the EXACT accepted-URL set
			// when defined: only the real, live merch pages count — a
			// plausible-but-non-existent deep link on an official host (e.g. a
			// fabricated category_id) is a failure, not a pass. Fall back to the
			// host-level check when no exact set is pinned.
			switch {
			case len(c.acceptedURLs) > 0:
				if !isAcceptedURL(got, c.acceptedURLs) {
					t.Errorf("resolved URL is not one of the accepted real pages: %q (accepted: %v)", got, c.acceptedURLs)
				}
			case len(c.officialHosts) > 0:
				if !isOfficialURL(got, c.officialHosts) {
					t.Errorf("resolved URL is NOT a confirmed-official destination: %q (allowed: %v)", got, c.officialHosts)
				}
			}
		})
	}
}

// defaultMerchCases pairs real artists with their REAL 2026 tours (verified via
// web research on 2026-06-03) and the confirmed official-host ground truth. The
// final case is a negative control: a deliberately non-existent series for
// which no confident official source should exist (expect empty).
//
// officialHosts ground truth (researched 2026-06-03):
//   - store.plusmember.jp is the shared official EC platform; the per-artist
//     path (/uverworld, /vaundy) proves ownership.
//   - official-goods-store.jp is SUPER BEAVER's official tour-goods store.
func defaultMerchCases() []merchCase {
	return []merchCase{
		{
			// Strongest positive control: the official goods detail page for this
			// tour is already PUBLISHED pre-tour, and the official EC lists the
			// "一人称" collection. The searcher should resolve to exactly one of
			// the two real, live pages below — and, per the priority rule, should
			// prefer the goods-announcement detail page over the EC store root.
			artist:      "ヨルシカ",
			series:      "ヨルシカ LIVE TOUR 2026「一人称」",
			expectFound: true,
			acceptedURLs: []string{
				"https://yorushika.com/news/detail/11751",
				"https://store.plusmember.jp/yorushika/",
			},
		},
		{
			artist:      "UVERworld",
			series:      "UVERworld ZERO LAG TOUR",
			expectFound: true,
			officialHosts: []string{
				"uverworld.com", "uverworld.jp",
				"store.plusmember.jp/uverworld",
				"x.com/UVERworld_dR2", "twitter.com/UVERworld_dR2",
				"x.com/Info_UVERworld", "twitter.com/Info_UVERworld",
				"instagram.com/uverworld_official",
			},
		},
		{
			artist:      "Vaundy",
			series:      `Vaundy DOME TOUR 2026 "SILENCE"`,
			expectFound: true,
			officialHosts: []string{
				"vaundy.jp", "member.vaundy.jp",
				"store.plusmember.jp/vaundy",
			},
		},
		{
			artist:      "SUPER BEAVER",
			series:      "都会のラクダ DOME TOUR 2026",
			expectFound: true,
			officialHosts: []string{
				"super-beaver.com", "sp.super-beaver.com",
				"official-goods-store.jp/super-beaver",
				"superbeaver.thebase.in",
				"x.com/super_beaver", "twitter.com/super_beaver",
			},
		},
		{
			// Negative control: a series that does not exist. The official-only
			// + empty-if-uncertain rules should ideally yield no URL; if the
			// model instead falls back to the artist's official site that is
			// tolerable, but a non-official host is still a hard failure.
			artist:      "SUPER BEAVER",
			series:      "都会のラクダ PHANTOM TOUR 2099",
			expectFound: false,
			officialHosts: []string{
				"super-beaver.com", "sp.super-beaver.com",
				"official-goods-store.jp/super-beaver",
				"superbeaver.thebase.in",
				"x.com/super_beaver", "twitter.com/super_beaver",
			},
		},
	}
}
