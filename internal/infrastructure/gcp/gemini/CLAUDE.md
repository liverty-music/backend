### Gemini A/B Evaluation Harness

The matrix-based A/B harness at `internal/infrastructure/gcp/gemini/searcher_integration_test.go` runs only when `GEMINI_AB_EVAL=1`. It compares concert-search performance across Gemini models, temperatures, and thinking levels against a frozen ground-truth fixture. See [`internal/infrastructure/gcp/gemini/testdata/README.md`](internal/infrastructure/gcp/gemini/testdata/README.md) for the full matrix, run command, fixture format, and how to interpret results.
