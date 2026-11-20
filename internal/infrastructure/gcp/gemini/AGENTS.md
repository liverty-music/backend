### Official page link discovery

`ConcertSearcher` lists official-site pages in its prompt for URL context to read, because URL context never follows links (`official_pages.go`, `concertSearchProfile`). It reads the top page and takes at most 8 same-domain links (concert keywords first, then `news`), then reads the first 3 concert-keyword links (never news pages) in parallel and appends their same-domain links that are neither the top page nor a top-page link, at most 20 pages in total (URL context's per-request limit). Every fetch goes through `NewOfficialPageClient` (5 s, 2 MB, public addresses only, same registrable domain after redirects); a page that cannot be read never fails Search. Details and the reasons: `docs/gemini-concert-searcher-tuning.md` §10.0.

### Gemini A/B Evaluation Harness

The matrix-based A/B harness at `internal/infrastructure/gcp/gemini/searcher_integration_test.go` runs only when `GEMINI_AB_EVAL=1`. It compares concert-search performance across Gemini models, temperatures, and thinking levels against a frozen ground-truth fixture. See [`internal/infrastructure/gcp/gemini/testdata/README.md`](internal/infrastructure/gcp/gemini/testdata/README.md) for the full matrix, run command, fixture format, and how to interpret results.
