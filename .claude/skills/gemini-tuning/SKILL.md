---
name: gemini-tuning
description: Procedure for tuning any Gemini API workload in this repository (the concert searcher, the ticket sale searcher and any later one) - choosing or upgrading the model, the thinking level, the system instruction and prompt, the tools and the generation config, and proving the choice with the A/B harness before it ships. Use it whenever a new Gemini model comes out or a model is about to be deprecated or repriced, whenever someone changes a Gemini model, thinking level, prompt or tool setting, and whenever a Gemini workload's recall, search queries, cost or latency look wrong in prod ("Gemini のチューニング", "モデルを上げる", "hydrogen に移行", "プロンプトを直す", "検索回数が多い"), even if the request names only one of these.
---

# Gemini tuning

Every tuning decision rests on two things: the official Gemini API documentation for the exact model generation in question, and a measured A/B result on this repository's own fixture. The documentation is the canon. This skill holds only the procedure for reading it and for measuring; it deliberately carries no prices, model names, thinking levels or prompting advice, because those change with each model and a copy here would become a stale canon (the Gemini 3 advice in `docs/gemini-concert-searcher-tuning.md` §3 is exactly that now).

The past evaluations in `docs/gemini-concert-searcher-tuning.md` §10 and `internal/infrastructure/gcp/gemini/testdata/README.md` are history: read them for what was tried and why, not as rules for a new model.

## 1. Map the workloads

Find every place the change touches, from the code, not from memory:

- Callers of `google.golang.org/genai` under `internal/infrastructure/gcp/gemini` and the config resolvers in `pkg/config` (`*SearchModel()`, `*Thinking*()`).
- Every process that builds each searcher (`internal/di`: the jobs, the event consumer, the API server) and whether it reports usage. A model change reaches all of them; checking only the job misses the consumer's searches.
- The prod pins in cloud-provisioning's `k8s/namespaces/backend` ConfigMaps (base and prod overlay), resolved the way `pkg/config` resolves them: the model from `GCP_GEMINI_SEARCH_MODEL_EXTRACT`, then the code default; the thinking level from `GCP_GEMINI_SEARCH_THINKING_EXTRACT`, then `GCP_GEMINI_SEARCH_THINKING_LEVEL`, then the code default. Read the rendered ConfigMap of each process (`kustomize build`), since a base value and an overlay value combine; a process with neither variable runs the code default.
- Prod usage per process from PostHog `$ai_generation` (`properties.process`): calls, search queries, errors, over the last month. This is the baseline any result is compared with.

## 2. Read the official documentation for the target model

Read, for the exact model and generation, and record each page's URL and the date you read it:

1. The docs index (`https://aistudio.google.com/llms.txt`) to find the current pages; URLs move between generations.
2. The model's "What's new" page and its **migration checklist**. Go through the checklist item by item against the code and write the result as a table (item, what the code does, change needed). Skipping this once shipped a model with a too-low `maxOutputTokens`.
3. The prompting guidance for that generation: the general prompt design page's model-specific section and any developer guide published for the generation. If only an older generation's section exists, say so and do not present it as guidance for the new model.
4. The pages of every tool and feature the workload uses (Google Search grounding, URL context, structured output, thinking) and the pricing page. Tool behavior matters as much as prompts: URL context opens only URLs given in the request or found by search, never a link on a page it read.

A page that cannot be read is reported as unread; never fill its content from memory. Keep verified facts (quoted from a page) apart from inferences in everything you write.

## 3. Prepare the evaluation

- **Fixture freshness.** A fixture compares the model against what official pages say at capture time. Check its capture date; if events or sales have since passed or left the pages, re-capture from public pages before measuring anything.
- **Fixture coverage.** One case per site or page structure that behaves differently (pages listed in the prompt suffice; a show page the list misses; detail pages behind pagination; tour sites on other domains), rather than many cases of one structure. More cases of the same structure add cost, not information.
- **Recorded per call:** per-entity recall (each artist's or sale's expected items found), search queries from the server-side tool calls, tokens, cost at the current price and at any announced future price, latency, empty or failed responses. What the harnesses record differs; check before relying on a column:
  - the concert harness (`searcher_integration_test.go`) records all of these and writes them to `testdata/ab_results/`; `testdata/README.md` has its commands and variants;
  - the ticket sale harness (`ticket_sale_eval_integration_test.go`, commands in its header comment) logs found, missed and extra sales per case and repetition and the wall-clock time, with search queries only in the searcher's "gemini response metadata" log line; it records no tokens or cost and writes no result file. Extend it before an evaluation that compares cost or queries.
- **Acceptance criteria** written before the runs, with the comparison made **per entity**, not only on totals: an average can hide an artist whose tour is dropped every time while another artist's outlier lifts the mean.

## 4. Run and diagnose

- 3 repetitions per cell, with repetitions as the outer loop so variants of one case run minutes apart. Only the variable under test differs between variants; confirm it from the recorded request metadata.
- When a model behaves differently (fewer searches, missed items), find out why before changing anything: run the case with thought summaries on (`ThinkingConfig.IncludeThoughts`) and record the order of tool calls and each URL fetch's status. Explanations without that evidence are guesses.
- When a test or a hypothesis needs the official pages' structure, check it on the real pages (what the top page links to, whether a page renders without JavaScript) instead of assuming.

## 5. Tune the prompt for the model

Follow the target generation's prompting guidance (step 2). Patterns that held for Gemini 4 at thinking `low`, to re-check against each new guide rather than copy:

- State the purpose, the task, the sources, and a completion criterion ("done when ..."); a model that avoids guessing stops as soon as what it read looks sufficient unless it is told what complete means.
- Remove procedural rules written to correct an older model, but keep the business rules that are the spec's (what counts, which sources). Measure removals and additions as separate variants.
- Give the model the inputs it needs to decide (for example the official top page's links with their labels) rather than encoding the decision in keyword rules; rules miss what a label makes obvious.

Thinking level and other settings that change cost are the owner's decision; as of 2026-11 the owner fixed thinking at `low` for cost. Ask before evaluating a higher level.

## 6. Ship and record

- Change the code default and the prod pin of a model or thinking level in the same release window, and confirm afterwards that every process running the workload runs the new image and config (kubectl), not only the one you were looking at.
- A behavior change that a spec states (what is sent, what is returned) goes through an OpenSpec delta; a model or prompt change that leaves the spec's requirements intact does not.
- Record the runs, the per-entity tables, the decision and the documentation you relied on in `docs/gemini-concert-searcher-tuning.md` §10 and the harness README, and force-add the runs' result files under `testdata/ab_results/` (`git add -f`: that directory's `.gitignore` ignores `*.json` and `*.csv`, so a plain `git add` commits nothing).
