# Gemini A/B Evaluation Harness

Ad-hoc harness for comparing Gemini search models on the concert discovery
workload. Not a CI test — runs only when `GEMINI_AB_EVAL=1` is set.

## Files

| Path | Purpose |
|---|---|
| `ab_ground_truth.json` | Frozen fixture of expected concerts per artist, as of `evaluation_from = 2026-11-20`, captured on 2026-11-20 from public official pages only (see "Fixture artists"). |
| `ab_results/` | Per-run outputs (`<RFC3339-utc>.json` + `.csv`). Generated files are gitignored; force-add the most recent run when committing for PR review. |

## Fixture artists

Captured 2026-11-20. In-scope events are the recall denominator; out-of-scope
entries (festivals, shows organized by other artists, members' solo shows)
are negative samples (`excluded_per_spec`). Events dated before the day of a
run are dropped from scoring, since the prompt's start date is today.

| artist | events (in scope / out of scope) | sources | why in the fixture |
|---|---|---|---|
| UVERworld | 20 / 1 | `feature/2026_live`, `feature/2027_live`, news articles | since 2026-10 |
| Vaundy | 32 / 0 | `member.vaundy.jp/feature/ARENATOUR_2728` (multilingual) | since 2026-10 |
| SUPER BEAVER | 42 / 1 | `sp.super-beaver.com/feature/tour2627`, `tour2728`, `budokan2027` | since 2026-10 |
| BRADIO | 3 / 3 | `show/` list | since 2026-10 |
| BIGMAMA | 24 / 3 | `shows/<id>/` detail pages | prod 2026-10-21..11-20: 36.7 queries per call (max 46) |
| go!go!vanillas | 18 / 0 | tour sites on other domains (`ggv-livehousetour2627.jp`, `ggv-backhometour2027.jp`) | prod: max 35 queries; detail pages are off the official domain |
| [Alexandros] | 20 / 11 | `contents/schedule` list, `contents/<id>` detail pages | prod: 34.0 queries per call (max 58); opaque detail paths |

BUMP OF CHICKEN (prod max 46 queries) was left out: its live pages render in
JavaScript and carry no dates in their HTML, so no ground truth could be
captured from them (nor read with URL context).

## How to run

Both harnesses call the production `ConcertSearcher`: one grounded call
(Google Search + URL context) returning JSON, `gemini-3.8-flash`, thinking
`low`, temperature not sent, Google Search limited to the last 2 months.

`TestConcertSearcher_ABEval` lists the official site's pages in every cell and
records the search queries from the server-side tool calls. Its variants
(`GEMINI_AB_EVAL_VARIANTS`, CSV; options joined with `+`):

| option | searcher setup |
|---|---|
| `base` | the top page's links only (production before pass-tour-detail-pages-to-concert-search) |
| `detail` | the top page's links and the links of its first 3 concert pages (production) |
| `cap` | adds "Run at most 10 Google Search queries; read the listed pages with url_context first." to the system instruction |
| `nosearch` | no Google Search tool; URL context only |

`GEMINI_AB_EVAL_MODELS` (CSV, default `gemini-3.8-flash`) and
`GEMINI_AB_EVAL_THINKING` (CSV, default `low`) set the model and thinking
axes, `GEMINI_AB_EVAL_REPS` the repetitions (default 3). Repetitions are the
outer loop, so the variants of one artist run minutes apart.

Full matrix (every fixture artist × 3 reps; here base vs detail):

```bash
GEMINI_AB_EVAL=1 GEMINI_AB_EVAL_VARIANTS=base,detail \
  GCP_GEMINI_SEARCH_API_KEY=<gemini-api-key> \
  go test -tags=integration -timeout=3h -v \
  -run TestConcertSearcher_ABEval \
  ./internal/infrastructure/gcp/gemini/...
```

Narrow with `GEMINI_AB_EVAL_ARTISTS=Vaundy,BRADIO`.

Smoke run (1 cell: the first model, variant and artist — for auth + API
sanity check):

```bash
GEMINI_AB_EVAL=1 GEMINI_AB_EVAL_SMOKE=1 GCP_GEMINI_SEARCH_API_KEY=<gemini-api-key> \
  go test -tags=integration -timeout=10m -v \
  -run TestConcertSearcher_ABEval \
  ./internal/infrastructure/gcp/gemini/...
```

Prompt variants (`TestConcertSearcher_GroundingVariants`, one artist per run):
`FINAL` (production prompt without linked pages, default), `LINKS`
(production: the concert and news pages the official top page links to,
then the links of its first 3 concert pages, are listed in the prompt), `E2UJ` (explicit url_context-first tool rules), `E2UJA`
(E2UJ with a mandatory first url_context step). An artist outside the fixture
runs unscored (search queries, URL context use and cost only) when
`GEMINI_GROUNDING_EVAL_SITE` gives its official site. Set
`GEMINI_GROUNDING_EVAL_TOOL_CALLS=1` to record each call's search queries from
the server-side tool calls; otherwise compare against the billing export's
hourly "search query" SKU count (one variant per clock hour).

```bash
GEMINI_GROUNDING_EVAL=1 GEMINI_GROUNDING_EVAL_VARIANT=FINAL \
  GEMINI_GROUNDING_EVAL_ARTIST=UVERworld GEMINI_GROUNDING_EVAL_TOOL_CALLS=1 \
  GCP_GEMINI_SEARCH_API_KEY=<gemini-api-key> \
  go test -tags=integration -timeout=1h -v \
  -run TestConcertSearcher_GroundingVariants \
  ./internal/infrastructure/gcp/gemini/...
```

Prerequisites:

- A Gemini API key (`GCP_GEMINI_SEARCH_API_KEY`); the searcher uses the Gemini
  API direct backend only.
- Budget for the run: each call bills its search queries (roughly 6-70 per
  call) and URL context input tokens.

## Latest result (2026-10-07, steer-concert-search-to-official-pages)

`gemini-3.8-flash`, thinking `low`, temperature unset, tool calls on, 3 reps
per artist and variant. `LINKS` lists the top page's concert pages only;
`LINKS_NEWS` adds news indexes and articles as a lower tier and became the
production `LINKS`. Unscored artists are compared with the 2026-10-06 prod
run (n=1 each).

| artist | FINAL queries (recall) | LINKS queries (recall) | LINKS_NEWS queries (recall) |
|---|---|---|---|
| UVERworld | 40.0 (1.00) | 2.7 (1.00) | 1.3 (1.00) |
| Vaundy | 25.0 (1.00) | 1.7 (1.00) | 0.0 (1.00) |
| SUPER BEAVER | 71.7 (0.67, 1 of 3 TOO_MANY_TOOL_CALLS) | 0.0 (1.00) | same links as LINKS, not run |
| BRADIO | 14.3 (1.00) | 16.7 (0.97) | 12.7 (0.97) |
| Novelbright (unscored) | prod 18 after a 46-query attempt | 10.3 | 22.3 |
| go!go!vanillas (unscored) | prod 400 twice, then 85 and stopped | 51.0 (0 links) | 35.0 (8 links) |
| YOASOBI (unscored, no links) | prod 19 | 41.7 (prompt identical to FINAL) | same as LINKS, not run |
| Ed Sheeran (unscored) | 5.7 | 3.7 | same links as LINKS, not run |

Cost per call on the fixture artists fell from $0.59 (FINAL) to $0.13
(LINKS). Against LINKS, LINKS_NEWS cut queries by 13% on the five artists
where the links differ (16.5 to 14.3) and cost from $0.29 to $0.25; it was
adopted.

Two D8 criteria were not met to the letter and were accepted as noise:

- BRADIO's queries did not fall (LINKS reps 3, 14 and 33). Its recall of 0.97
  is one rep copying venue names with a prefecture prefix as the show page
  prints them (「大阪県・BananaHall」), not a missed show.
- YOASOBI's 41.7 queries are outside ±30% of the prod run's 19, but its prompt
  is byte-identical to FINAL (no extractable links); the reps ranged 25-56.

The #542 400 did not reproduce in these 54 calls; the reduced retry is covered
by unit tests and checked in prod.

## Earlier result (2026-10-01..04, tune-concert-search-grounding)

`gemini-3.8-flash`, thinking `low`, temperature unset.

| configuration | search queries / call | recall |
|---|---|---|
| previous prompt, host only | 55 (Vaundy) | 0.94-1.00 |
| short prompt, full URL, 2-month window (XML + parse step) | Vaundy 39, UVERworld 14, SUPER BEAVER 16, BRADIO 10 | 0.87-1.00 |
| same, single grounded call returning JSON (production) | 20-32 (run-to-run 6-70) | 0.86-1.00 |

Per-call search counts read from server-side tool invocations matched the
billing export exactly. Thinking `medium` produced timeouts and empty
responses; temperature 0.3 increased searches on 3 of 4 artists.

## Matrix axes (full run)

| Axis | Values |
|---|---|
| Model | `gemini-3.8-flash` (`GEMINI_AB_EVAL_MODELS`) |
| Temperature | not sent |
| ThinkingLevel | `low` (`GEMINI_AB_EVAL_THINKING`) |
| Variant | `base` (`GEMINI_AB_EVAL_VARIANTS`) |
| Artist | the 7 fixture artists (`GEMINI_AB_EVAL_ARTISTS`) |
| Repetitions | 3 (`GEMINI_AB_EVAL_REPS`) |

## How to refresh the fixture

The fixture's `evaluation_from` is frozen at capture time. To re-curate:

1. Pick a new `evaluation_from` date.
2. For each artist, visit the `official_site_url` and walk the schedule pages.
3. For each upcoming event on/after the new date, capture: `event_name`,
   `venue`, `admin_area` (都道府県; empty for overseas), `local_date`,
   `open_time` / `start_time` (ISO 8601 with timezone, or empty string),
   `source_url`, `confidence` (`confirmed` / `tentative`), `visibility`
   (`public` / `members-only`).
4. Update both `evaluation_from` and `captured_at`.
5. Run `go test ./internal/infrastructure/gcp/gemini/... -run LoadGroundTruth`
   to verify the JSON parses and required fields are present.

## How to read the results

`<timestamp>.json` is the canonical machine output. Top-level keys:

- `run_metadata` — SDK version, timestamps, total cost.
- `cells` — array of per-cell records.

Each cell record carries `variant`, `precision`, `recall_public`,
`recall_all`, `f1_*`, `field_accuracy.*`, token counts, search-query count,
`linked_pages`, latency, finish reason, `retry_count`, `empty_response` (no
candidate, or no text), `grounding_chunks` (backend #772), and USD cost:
`cost_usd` at the current price and `cost_usd_2027` at the price from
2027-01-01 (`Pricing2027`), which model decisions compare.

`<timestamp>.csv` is the same data flattened for spreadsheet analysis.

For high-level model comparison, group rows by `model` + `thinking_level`
and average over `repetition` × `artist`. Per-artist
breakdowns show whether one model handles overseas tours better than another.
