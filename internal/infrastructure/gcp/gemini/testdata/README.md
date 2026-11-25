# Gemini A/B Evaluation Harness

Ad-hoc harness for comparing Gemini search models on the concert discovery
workload. Not a CI test — runs only when `GEMINI_AB_EVAL=1` is set.

## Files

| Path | Purpose |
|---|---|
| `ab_ground_truth.json` | Frozen fixture of expected concerts per artist, as of `evaluation_from = 2026-11-20`, captured on 2026-11-20 from public official pages only (see "Fixture artists"). |
| `ab_results/` | Per-run outputs (`<RFC3339-utc>.json` + `.csv`). Generated files are gitignored; force-add the most recent run when committing for PR review. |

## Fixture artists

One artist per official-site structure, captured 2026-11-20. In-scope events
are the recall denominator; out-of-scope entries (festivals, shows organized
by other artists, members' solo shows) are negative samples
(`excluded_per_spec`). Events dated before the day of a run are dropped from
scoring, since the prompt's start date is today.

| artist | events (in scope / out of scope) | site structure | sources |
|---|---|---|---|
| Vaundy | 32 / 0 | the listed pages carry every show; multilingual tour page | `member.vaundy.jp/feature/ARENATOUR_2728` |
| SUPER BEAVER | 42 / 1 | a show page whose path has no concert word (`feature/budokan2027`, a banner on the top page) | `sp.super-beaver.com/feature/tour2627`, `tour2728`, `budokan2027` |
| [Alexandros] | 20 / 11 | detail pages (`contents/<id>`) behind page 2 of the schedule list | `contents/schedule`, `contents/<id>` |
| go!go!vanillas | 18 / 0 | tour sites on other domains, linked from the top page | `ggv-livehousetour2627.jp`, `ggv-backhometour2027.jp` |

UVERworld, BRADIO, BIGMAMA and 10-FEET were evaluated on 2026-11-20..21 and
dropped to keep runs cheap: their structures repeat the four above.
[Alexandros]'s 2027-05-12 venue is "福岡 UNITEDLAB" in the fixture; the model
writes "UNITEDLAB", which the scorer counts as a miss.

## How to run

Both harnesses call the production `ConcertSearcher`: one grounded call
(Google Search + URL context) returning JSON, `gemini-4-hydrogen`, thinking
`low`, temperature not sent, Google Search limited to the last 2 months.

`TestConcertSearcher_ABEval` lists the official site's pages in every cell and
records the search queries from the server-side tool calls. Its variants
(`GEMINI_AB_EVAL_VARIANTS`, CSV; options joined with `+`):

| option | searcher setup |
|---|---|
| `base` | production: the official top page's links with their labels |
| `cap` | adds "Run at most 10 Google Search queries; read the listed pages with url_context first." to the system instruction |
| `nosearch` | no Google Search tool; URL context only |

`GEMINI_AB_EVAL_MODELS` (CSV, default `gemini-4-hydrogen`) and
`GEMINI_AB_EVAL_THINKING` (CSV, default `low`) set the model and thinking
axes, `GEMINI_AB_EVAL_REPS` the repetitions (default 3). Repetitions are the
outer loop, so the variants of one artist run minutes apart.

Full matrix (every fixture artist × 3 reps; here base vs cap):

```bash
GEMINI_AB_EVAL=1 GEMINI_AB_EVAL_VARIANTS=base,cap \
  GCP_GEMINI_SEARCH_API_KEY=<gemini-api-key> \
  go test -tags=integration -timeout=3h -v \
  -run TestConcertSearcher_ABEval \
  ./internal/infrastructure/gcp/gemini/...
```

Narrow with `GEMINI_AB_EVAL_ARTISTS=Vaundy,SUPER BEAVER`. Override an
artist's official site for a run with `GEMINI_AB_EVAL_SITES=<name>=<url>`
(CSV); a variable with spaces or `!` needs `env` in zsh
(`env 'GEMINI_AB_EVAL_ARTISTS=go!go!vanillas' go test ...`).

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
(production: the official top page's links are listed in the prompt with
their labels), `E2UJ` (explicit url_context-first tool rules), `E2UJA`
(E2UJ with a mandatory first url_context step). An artist outside the fixture
runs unscored (search queries, URL context use and cost only) when
`GEMINI_GROUNDING_EVAL_SITE` gives its official site. Set
`GEMINI_GROUNDING_EVAL_TOOL_CALLS=1` to record each call's search queries from
the server-side tool calls; otherwise compare against the billing export's
hourly "search query" SKU count (one variant per clock hour).

```bash
GEMINI_GROUNDING_EVAL=1 GEMINI_GROUNDING_EVAL_VARIANT=FINAL \
  GEMINI_GROUNDING_EVAL_ARTIST=Vaundy GEMINI_GROUNDING_EVAL_TOOL_CALLS=1 \
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

## Latest result (2026-11-25, globalize-venues)

The response schema gains a required `time_zone` per event, and
`admin_area` is resolved to a first-level Area code
(`ab_results/20261125T024319Z.*`, production prompt with links,
`gemini-4-hydrogen`, thinking `low`, the 4 fixture artists × 3 reps).

| | Vaundy | SUPER BEAVER Budokan | [Alexandros] FC TOUR | go!go!vanillas BACK HOME TOUR | queries / call | cost / call |
|---|---|---|---|---|---|---|
| 2026-11-21 production | 32/32 ×3 | 2/2 ×3 | 8/8 ×3 | 8/8 ×3 | 0 | $0.075 |
| with `time_zone` | 32/32 ×3 | 2/2 ×3 | 8/8 ×3 | 8/8 ×3 | 0 | $0.077 |

Every returned event's area and time zone were right (all Japanese venues,
`Asia/Tokyo`). [Alexandros]'s recall was 0.85, 0.85 and 0.55 against 0.85 ×3:
the 0.55 run returned the same 20 dates but copied the prefecture in front of
8 venue names as the show page prints them (「北海道 サッポロファクトリーホール」),
the noise already seen with BRADIO below, so the change was accepted.

## Earlier result (2026-11-21, pass-tour-detail-pages-to-concert-search)

`gemini-4-hydrogen`, thinking `low`, 3 reps per artist and variant, cost at
the 2027-01-01 price. Raw outputs: `ab_results/20261121T*`.

Prompt (runs 4–5, 8 artists): the goal-and-done prompt (now production)
against the procedural one. Dates of the tours the listed pages did not
reach ([Alexandros] FC TOUR, go!go!vanillas BACK HOME TOUR): 47/48 against
33/48 (the procedural prompt dropped the whole tour once in 3 runs on each);
the other artists unchanged; cost $0.087 against $0.070 per call (run 5).

Links (runs 6–10, the 4 fixture artists, goal prompt):

| variant | Vaundy | SUPER BEAVER Budokan | [Alexandros] FC TOUR | go!go!vanillas BACK HOME TOUR | queries / call | cost / call |
|---|---|---|---|---|---|---|
| keyword-selected concert pages | 32/32 | 0/2 ×3 | 8, 8, 7 /8 | 8/8 ×3 (4–8 queries) | 2.3 | $0.113 |
| + same-site links with labels | 32/32 | 2/2 ×3 | 8/8 ×3 | 8/8 ×3 (4–8 queries) | 1.3 | $0.094 |
| + "follow the top page's links" instruction | — | 2/2 in 2 of 3 runs, via search | — | — | — | — |
| links of any domain with labels, blocked domains out, no keyword list (production) | 32/32 ×3 | 2/2 ×3 | 8/8 ×3 | 8/8 ×3 (0 queries) | 0 | $0.075 |

## Earlier result (2026-11-20, pass-tour-detail-pages-to-concert-search)

`gemini-3.8-flash`, thinking `low`, temperature unset, 3 reps per artist and
variant, all 7 fixture artists, cost at the 2027-01-01 price
(`ab_results/20261120T101506Z.*`). Run 1 compared production (`base`) with
`detail`: the links of the first 3 concert pages the top page links to,
other than the top page's own links, appended to the listed pages (at most 20).

| artist | base queries (recall) | detail queries (recall) | linked pages base → detail |
|---|---|---|---|
| UVERworld | 0, 0, 0 (1.00) | 0, 2, 0 (1.00) | 8 → 20 |
| Vaundy | 0, 0, 0 (1.00) | 0, 0, 0 (1.00) | 8 → 20 |
| SUPER BEAVER | 0, 0, 2 (0.95) | 6, 4, 8 (1.00) | 8 → 20 |
| BRADIO | 17, 4, 17 (0.78) | 23, 33, 16 (0.78) | 8 → 11 |
| BIGMAMA | 23, 23, 10 (0.68) | 30, 21, 9 (1.00) | 8 → 20 |
| go!go!vanillas | 12, 36, 16 (1.00) | 35, 24, 22 (1.00) | 8 → 8 |
| [Alexandros] | 16, 18, 15 (0.77) | 55, 63, 70 (0.93) | 2 → 20 |
| **all** | median 10, cost $0.246 | median 16, cost $0.477 | |

`detail` raised recall of public events from 0.882 to 0.959, but search
queries per call rose (median 10 → 16; 16 → 30 on the three many-query
artists) and cost per call nearly doubled, so it failed the acceptance
criteria (design D4) and was removed. The listed pages were mostly not tour
detail pages: [Alexandros]'s 20 slots went to 11 region filters of its
schedule page and the side projects on its first list page, so the model
still searched for each tour, now with 20 pages of URL context input.
go!go!vanillas's tour pages sit on other domains and gained nothing.
BIGMAMA's one base miss (recall 0.04) was a single rep. No empty responses,
errors or timeouts in either variant; p90 latency 72 s (base), 86 s (detail).

Run 2 (`ab_results/20261120T102609Z.*`) ran production (`base`) on
`gemini-4-hydrogen`, thinking `low`:

| artist | gemini-3.8-flash queries (recall) | gemini-4-hydrogen queries (recall) |
|---|---|---|
| UVERworld | 0, 0, 0 (1.00) | 0, 0, 0 (1.00) |
| Vaundy | 0, 0, 0 (1.00) | 0, 0, 0 (1.00) |
| SUPER BEAVER | 0, 0, 2 (0.95) | 0, 0, 0 (0.95) |
| BRADIO | 17, 4, 17 (0.78) | 0, 0, 0 (0.67) |
| BIGMAMA | 23, 23, 10 (0.68) | 1, 0, 0 (1.00) |
| go!go!vanillas | 12, 36, 16 (1.00) | 3, 7, 4 (0.87) |
| [Alexandros] | 16, 18, 15 (0.77) | 3, 3, 3 (0.65) |
| **all** | median 10, cost $0.246, p90 72 s | median 0, cost $0.080, p90 36 s |

Recall 0.877 against 0.882 (0.922 against 0.906 weighted by events), within
the 0.02 margin; queries and cost fell by two thirds or more, so the model
switch meets D4. Neither model returned `groundingChunks` (backend #772).

Run 3 (`ab_results/20261120T104756Z.*`, `gemini-4-hydrogen`, thinking `low`)
against run 2:

| artist | base queries (recall) | cap queries (recall) | nosearch recall |
|---|---|---|---|
| UVERworld | 0, 0, 0 (1.00) | 0, 0, 0 (1.00) | 1.00 |
| Vaundy | 0, 0, 0 (1.00) | 0, 0, 0 (1.00) | 1.00 |
| SUPER BEAVER | 0, 0, 0 (0.95) | 0, 0, 0 (0.95) | 0.63 (one 120 s timeout) |
| BRADIO | 0, 0, 0 (0.67) | 0, 0, 2 (0.44; one connection reset) | 0.67 |
| BIGMAMA | 1, 0, 0 (1.00) | 2, 2, 3 (1.00) | 1.00 |
| go!go!vanillas | 3, 7, 4 (0.87) | 4, 6, 8 (1.00) | 0.61 |
| [Alexandros] | 3, 3, 3 (0.65) | 3, 3, 3 (0.78) | 0.00 |
| **all** | recall 0.877, cost $0.080 | recall 0.883, cost $0.091 | recall 0.702, cost $0.058 |

The search-cap line did not lower queries and raised cost, so it was not
adopted; Google Search off loses the tours the listed pages do not reach.
Thinking `minimal` is rejected by `gemini-4-hydrogen` with 400.

## Earlier result (2026-10-07, steer-concert-search-to-official-pages)

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
| Model | `gemini-4-hydrogen` (`GEMINI_AB_EVAL_MODELS`) |
| Temperature | not sent |
| ThinkingLevel | `low` (`GEMINI_AB_EVAL_THINKING`) |
| Variant | `base` (`GEMINI_AB_EVAL_VARIANTS`) |
| Artist | the 4 fixture artists (`GEMINI_AB_EVAL_ARTISTS`) |
| Repetitions | 3 (`GEMINI_AB_EVAL_REPS`) |

## How to refresh the fixture

The fixture's `evaluation_from` is frozen at capture time. To re-curate:

1. Pick a new `evaluation_from` date.
2. For each artist, visit the `official_site_url` and walk the schedule pages.
3. For each upcoming event on/after the new date, capture: `event_name`,
   `venue`, `admin_area` (the code of the first-level Area, e.g. `JP-27`; empty when the page names none), `local_date`,
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
