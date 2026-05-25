# Gemini A/B Evaluation Harness

Ad-hoc harness for comparing Gemini search models on the concert discovery
workload. Not a CI test — runs only when `GEMINI_AB_EVAL=1` is set.

## Files

| Path | Purpose |
|---|---|
| `ab_ground_truth.json` | Frozen fixture of expected concerts per artist (UVERworld / Vaundy / SUPER BEAVER), as of `evaluation_from = 2026-05-20`. |
| `ab_results/` | Per-run outputs (`<RFC3339-utc>.json` + `.csv`). Generated files are gitignored; force-add the most recent run when committing for PR review. |

## How to run

Full matrix (54 cells, ~$0.30, ~15 min):

```bash
GEMINI_AB_EVAL=1 GCP_PROJECT_ID=<dev-project> \
  go test -tags=integration -timeout=3h -v \
  -run TestConcertSearcher_ABEval \
  ./internal/infrastructure/gcp/gemini/...
```

Smoke run (1 cell, ~$0.01, ~2 min — for auth + API sanity check):

```bash
GEMINI_AB_EVAL=1 GEMINI_AB_EVAL_SMOKE=1 GCP_PROJECT_ID=<dev-project> \
  go test -tags=integration -timeout=10m -v \
  -run TestConcertSearcher_ABEval \
  ./internal/infrastructure/gcp/gemini/...
```

Prerequisites:

- ADC configured (`gcloud auth application-default login`) for the project.
- Vertex AI + Google Search grounding enabled on the project.
- Project has budget for the run (grounding is free under 5,000/month).

## Matrix axes (full run)

| Axis | Values |
|---|---|
| Model | `gemini-3.1-flash-lite` |
| Temperature | 0.2, 0.5, 1.0 |
| ThinkingLevel | `medium`, `high` |
| Artist | UVERworld, Vaundy, SUPER BEAVER |
| Repetitions | 3 |

Excluded by design: Gemini 3 Flash Preview (unstable under grounding load —
hit frequent 504s in a partial run on 2026-05-20), Gemini 3.5 Flash (3× cost),
Temperature 0.0 (no variance), ThinkingLevel `low` (near-baseline). See the
[evaluate-gemini-search-model design.md](../../../../../../specification/openspec/changes/evaluate-gemini-search-model/design.md)
for rationale.

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

Each cell record carries `precision`, `recall_public`, `recall_all`,
`f1_*`, `field_accuracy.*`, token counts, latency, retry count, finish
reason, and USD cost.

`<timestamp>.csv` is the same data flattened for spreadsheet analysis.

For high-level model comparison, group rows by `model` + `thinking_level`
and average over `temperature` × `repetition` × `artist`. Per-artist
breakdowns show whether one model handles overseas tours better than another.
