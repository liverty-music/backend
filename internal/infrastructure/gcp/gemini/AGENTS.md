### Tuning a Gemini workload

Model upgrades, thinking levels, prompts and tool settings follow the `gemini-tuning` skill (`.claude/skills/gemini-tuning/SKILL.md`): the official documentation for the target model is the canon, and every change is measured per entity with the A/B harness before it ships.

### Official page links in the prompt

`ConcertSearcher` reads the official top page and lists its links in the prompt as `label: URL` (link text, or an image's alt text), so URL context can open the show pages among them without searching; the model, not a keyword rule, decides which links announce shows (`official_pages.go`). Links are listed in page order, at most 60, leaving out `blockedLinkDomains` (social networks, streaming, link hubs, shops, ticket vendors, fan-club and login platforms, lyrics) and the official site's own `siteInfoPathSegments` pages (privacy, terms, FAQ, profile, discography, sign-in, …). A domain belongs in `blockedLinkDomains` only if it never carries an artist's official show announcements; record labels, management companies and artist-owned domains stay listed. The prompt's official site is the URL the top page was read from. Every fetch goes through `NewOfficialPageClient` (5 s, 2 MB, public addresses only, same registrable domain after redirects); a page that cannot be read never fails Search. Evaluation and reasons: `docs/gemini-concert-searcher-tuning.md` §10.0.

### Gemini A/B Evaluation Harness

The matrix-based A/B harness at `internal/infrastructure/gcp/gemini/searcher_integration_test.go` runs only when `GEMINI_AB_EVAL=1`. It compares concert-search performance across Gemini models, temperatures, and thinking levels against a frozen ground-truth fixture. See [`internal/infrastructure/gcp/gemini/testdata/README.md`](internal/infrastructure/gcp/gemini/testdata/README.md) for the full matrix, run command, fixture format, and how to interpret results.
