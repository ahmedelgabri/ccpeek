# Cost calculation

This document describes how CCPeek currently captures tokens, calculates USD costs, aggregates them, and exposes uncertainty. It documents the implementation as it exists today, including known correctness gaps; it is not a description of an ideal or future billing system.

## What a cost means

CCPeek's dollar values are usage analytics, not invoice reconciliation.

- An **agent-reported cost** is copied from the agent's session data. It is normally the closest value available to what the agent or its provider library calculated for that request, but CCPeek does not verify it against an invoice.
- An **estimated cost** is an API-equivalent value calculated from recorded tokens and the embedded LiteLLM list-price snapshot.
- A session can contain both reported and estimated costs because the choice is made per usage record, not once for the whole session.
- For subscription-backed usage such as Claude Pro/Max or Codex subscriptions, the dollar value is not an amount charged to the user. It is at most an estimate of the equivalent API value.
- When tokens cannot be priced, the displayed cost is a lower bound rather than a complete total.

## Canonical token buckets

Every adapter normalizes usage into these fields in `canon.Usage`:

| Field                | Meaning                                                              |
| -------------------- | -------------------------------------------------------------------- |
| `InputTokens`        | Ordinary, non-cached input tokens                                    |
| `OutputTokens`       | Billable output tokens, including reasoning exactly once             |
| `CacheReadTokens`    | Input tokens served from a prompt cache                              |
| `CacheWriteTokens`   | Total input tokens written to a prompt cache                         |
| `CacheWrite1hTokens` | One-hour-TTL subset of cache writes; zero for legacy unsplit rows    |
| `ReasoningTokens`    | Informational reasoning detail; never added to cost separately       |
| `ReportedCostUSD`    | Optional cost supplied by the agent                                  |
| `ServiceTier`        | Recorded provider tier when available; currently not used by pricing |

Provider identity is stored separately on each canonical message and combined with the model for exact rate lookup before falling back to normalized bare-model candidates.

The normalization contract is important because providers disagree about reasoning. Codex reports reasoning as a subset of output, while OpenCode reports reasoning additively beside output. The Codex adapter leaves output unchanged and records reasoning only as detail; the OpenCode adapter adds reasoning to output. Downstream code must therefore never add `ReasoningTokens` to `OutputTokens`.

## Per-record cost selection

CCPeek exposes three cost modes through the UI, HTTP API, CLI query commands, and MCP tools. Selection is applied independently to each `message_usage` row:

- `auto` uses a non-zero agent-reported cost when present, otherwise calculates from tokens. A reported zero with non-zero usage falls through to calculation; the raw zero remains stored as provenance. A reported zero on a zero-token row remains reported.
- `calculate` ignores every reported cost and calculates each row from tokens.
- `display` uses only agent-reported costs. Tokens on rows without a report are counted as unreported and contribute no USD amount.

For rows that a mode calculates, CCPeek resolves an effective-dated, request-size-aware provider/model rate and prices every bucket whose rate is known. If the model or an individual cache-bucket rate cannot be resolved, those tokens are counted as unpriced and contribute no USD amount; other known buckets on the same row are still priced.

The estimated formula is:

```text
cache_write_5m_tokens = cache_write_tokens - cache_write_1h_tokens

estimated_cost_usd =
    input_tokens          × input_rate
  + output_tokens         × output_rate
  + cache_write_5m_tokens × cache_write_rate
  + cache_write_1h_tokens × cache_write_1h_rate
  + cache_read_tokens     × cache_read_rate
```

Rates are stored as USD per single token. No reasoning term appears in the formula because billable reasoning is already included in `output_tokens`.

At aggregate level in `auto` mode:

```text
cost_usd = sum(reported_cost_usd selected by auto) + sum(estimated_cost_usd selected by auto)
```

Reported and estimated cost are mutually exclusive for an individual row in `auto`, so this aggregation does not intentionally charge the same row twice. `calculate` sums estimates for all usage rows; `display` sums only reported amounts.

The core exact arithmetic lives in `pricing.Rate.PriceAmount`; `db.EvaluateCostAt` applies mode selection, effective dates, provider/model resolution, and request-size tiers to one row. Session summaries, rolling blocks, daily rollups, stats, and query surfaces use that shared evaluator rather than maintaining separate formulas.

## Agent-specific capture and normalization

### Claude Code

The Claude adapter reads assistant `message.usage` fields as follows:

| Claude field                  | Canonical field    |
| ----------------------------- | ------------------ |
| `input_tokens`                | `InputTokens`      |
| `output_tokens`               | `OutputTokens`     |
| `cache_read_input_tokens`     | `CacheReadTokens`  |
| `cache_creation_input_tokens` | `CacheWriteTokens` |
| `service_tier`                | `ServiceTier`      |
| top-level legacy `costUSD`    | `ReportedCostUSD`  |

Anthropic's `input_tokens` excludes cache reads and cache creation, so the four buckets are additive and match the cost formula directly.

Claude can repeat an API response across resumed or forked transcripts. CCPeek deduplicates usage across the same agent using `(message.id, requestId)` while retaining every transcript message; an empty legacy `requestId` remains a valid second key rather than disabling dedupe. The first-seen message owns the usage row, while a duplicate with greater output, then greater total tokens, then a usable reported cost updates that row's values without moving ownership between sessions.

A legacy line carrying `costUSD` but no `message.usage` is retained as a reported, zero-token usage row.

Claude's nested cache-creation breakdown is stored as total cache writes plus a one-hour subset. Five-minute writes use `cache_creation_input_token_cost`; one-hour writes use `cache_creation_input_token_cost_above_1hr`. Archived rows that cannot be reparsed retain a zero one-hour subset and therefore keep the documented five-minute assumption permanently.

### Pi

For message entries with usage, the Pi adapter maps:

| Pi field           | Canonical field    |
| ------------------ | ------------------ |
| `usage.input`      | `InputTokens`      |
| `usage.output`     | `OutputTokens`     |
| `usage.cacheRead`  | `CacheReadTokens`  |
| `usage.cacheWrite` | `CacheWriteTokens` |
| `usage.cost.total` | `ReportedCostUSD`  |

Pi already normalizes provider token accounting into mutually exclusive Anthropic-style buckets, and its output count includes normal and reasoning output. CCPeek therefore prefers Pi's reported total and does not calculate a second cost for those records.

The adapter prefers the provider and model recorded on each modern Pi message, falling back to `model_change` state for older entries. This removes file-order model ambiguity for usage-bearing modern messages.

Usage and reported cost on modern Pi `compaction` and `branch_summary` entries are captured and included in session totals.

### Codex CLI

Codex token-count events contain cumulative totals and, in many logs, a `last_token_usage` record for the latest turn. CCPeek uses `last_token_usage` when available. Otherwise it subtracts the previous cumulative total and treats a lower total as a counter reset. Repeated events with an unchanged cumulative total and identical last-turn usage are suppressed.

Codex input includes cache reads and cache writes, so CCPeek normalizes:

```text
InputTokens      = max(input_tokens - cached_input_tokens - cache_write_input_tokens, 0)
CacheReadTokens  = cached_input_tokens
CacheWriteTokens = cache_write_input_tokens
OutputTokens     = output_tokens
ReasoningTokens  = reasoning_output_tokens
```

Codex reasoning is a subset of output and is not added again. Cache-write counters participate in the same latest-turn, cumulative-delta, reset, and duplicate-event handling as the other buckets. If malformed input reports cache reads plus writes greater than total input, CCPeek clamps ordinary input to zero and emits a warning ingest issue naming the conflicting counts.

Codex does not report a USD cost in the indexed format, so its usage normally follows calculated pricing. Token-count events that arrive before a model is known remain visible as unpriced rather than being assigned an arbitrary model.

### OpenCode

OpenCode supplies ordinary input, output, additive reasoning, cache reads, cache writes, and usually a reported cost. CCPeek normalizes:

```text
InputTokens     = tokens.input
OutputTokens    = tokens.output + tokens.reasoning
ReasoningTokens = tokens.reasoning
CacheReadTokens = tokens.cache.read
CacheWriteTokens = tokens.cache.write
ReportedCostUSD = cost
```

If an OpenCode usage record has no reported cost, or stores zero with non-zero tokens, its normalized tokens use calculated pricing. The adapter preserves both `providerID` and `modelID`, tries the exact provider/model rate first, and can still fall back to the bare model through normal lookup candidates.

### Cursor

The Cursor adapter reads fixture-based `inputTokens`, `outputTokens`, `cacheReadTokens`, and `cacheWriteTokens` fields from message blobs. It does not capture a reported cost, so recognized models use calculated pricing. This mapping has not been validated against a representative real Cursor corpus and should be treated as provisional.

## Pricing data and model lookup

Pricing comes from `internal/pricing/snapshot.json`, an embedded, pruned copy of LiteLLM's `model_prices_and_context_window.json` augmented with authoritative effective-dated cards maintained in `internal/pricing/history.json`. The generated snapshot records its source URL and fetch timestamp. `scripts/update-pricing.sh` downloads a new source file, merges the maintained history, and keeps these fields without converting missing dimensions to zero:

- `input_cost_per_token`
- `output_cost_per_token`
- `cache_creation_input_token_cost`
- `cache_creation_input_token_cost_above_1hr`
- `cache_read_input_token_cost`
- Input, output, cache-write, one-hour-cache-write, and cache-read tier variants above 128k, 200k, 256k, 272k, and 512k input tokens

The binary does not fetch prices at runtime. Updating the script output only affects binaries built with the new snapshot.

Model lookup is case-insensitive and tries candidates in order:

1. The complete recorded identifier.
2. Successively stripped slash-delimited provider or router prefixes.
3. A Vertex-style identifier without its `@date` suffix.
4. Bedrock identifiers without region and version decorations.

Exact provider-specific matches therefore win when present, but fallback to a bare model can use a generic rate that does not reflect the original provider, region, or negotiated price.

Input and output rates are required for a model to enter the snapshot. Cache-rate presence is preserved separately from its numeric value. A known model can therefore be partially priced: input and output contribute cost while cache tokens with an absent rate remain visibly unpriced.

Dated requests resolve against effective-date cards when history exists; a gap in that history is authoritative and remains unpriced rather than silently receiving today's rate. Long-context tiers are selected per request from ordinary input plus cache reads and writes, so aggregation cannot push several small requests across a threshold. A sparse historical card without tiers inherits the current snapshot's absolute tier rates. This deliberately mixes eras when base prices changed; authoritative cards must include their own tiers to avoid that assumption.

## Aggregation and materialization

Raw usage is stored per message in `message_usage`. Costs are exposed through two paths:

- Session summaries and rolling block queries scan raw usage and evaluate each row before aggregation, preserving request dates and size tiers.
- The Usage page, cost timeline, and overview stats read `rollup_usage_daily`, which materializes exact `auto`, `calculate`, and `display` amounts plus reported, estimated, unpriced, and unreported provenance for dashboard speed.

Daily rollups group by day, agent, workspace, and model. A session that uses several models is priced independently for each model before the results are added. Session counts are recomputed as distinct sessions rather than summed across model rows.

Rollups are fully rebuilt after an ingest pass changes sessions or messages, after source pruning, whenever raw usage and rollup presence disagree (including deletion of the final usage row), or when the stored pricing fingerprint differs. The fingerprint hashes the exact snapshot bytes together with a pricing-algorithm version and is written transactionally with the rollups, so query-time and materialized costs cannot remain on different pricing semantics after an upgrade.

Internal amounts are signed 64-bit nanodollars. Per-token rates are quantized to picodollars, token multiplication uses arbitrary-precision intermediates, each row is rounded once to the nearest nanodollar, and aggregation detects overflow. SQLite stores exact integer mirrors such as `reported_cost_nanos`, `cost_nanos`, and mode-specific rollup amounts; legacy `REAL` columns and API floats remain compatibility projections produced only at boundaries. Exact decimal strings are exposed alongside those floats.

## Unpriced data

For a usage row selected for calculation whose model cannot be resolved:

- Token totals are still included.
- Calculated cost contributes zero.
- Session and block responses include an `unpricedTokens` count.
- Daily usage responses derive `hasUnpriced` from persisted per-bucket unpriced token counts; the legacy `priced` rollup column is vestigial and remains write-only pending a schema cleanup.
- The displayed USD amount is a lower bound.

Agent-reported costs do not require model lookup. A row with a reported cost and an unknown model is considered costed because CCPeek does not need a rate to use the reported value.

Unpriced accounting operates per bucket. Session and block responses expose both the total and a token-type breakdown; daily rollups persist the same breakdown and derive `hasUnpriced` from actual non-zero unpriced tokens. Unknown zero-token rows are fully priced by definition and no longer create a cross-surface false positive.

## Pricing dimensions not currently modeled

The LiteLLM source and provider APIs contain more pricing dimensions than CCPeek preserves:

- Historical periods not covered by the small maintained set of authoritative effective-dated cards.
- Historical long-context tier rates when a sparse historical card inherits absolute tiers from the current snapshot.
- Batch, priority, service-tier, and data-residency modifiers.
- Provider-specific and regional rates when only a bare model identifier is stored or matched.
- Images, audio, web-search requests, server tools, and other non-token charges.
- Custom, negotiated, or enterprise price cards.
- Subscription allowances, rate limits, credits, and actual invoice adjustments.

As a result, calculated cost is best described as a base-rate API-equivalent estimate against one embedded snapshot.

## Audit evidence (2026-08-21)

The semantics above were verified against primary sources on 2026-08-21. Upstream citations are pinned to the last commit touching each file as of that date.

**Anthropic field disjointness.** Real local Claude Code JSONL shows `input_tokens: 3` beside `cache_creation_input_tokens: 9293` in one usage object, with the per-TTL breakdown present: `"cache_creation": {"ephemeral_5m_input_tokens": 9293, "ephemeral_1h_input_tokens": 0}`. Cross-checked against a billing-console reproduction (LiteLLM issue #9812): `{input: 3, cache_creation: 12304, output: 550}` on claude-3-7-sonnet priced as `3×$3/M + 12304×$3.75/M + 550×$15/M ≈ $0.0544`, matching the console figure. The four-bucket additive formula is therefore correct for Claude, and the data needed to price one-hour writes separately already exists in the transcripts.

**Codex subset semantics.** A real rollout event shows `total_tokens 22403 = input_tokens 22157 + output_tokens 246` exactly, with `cached_input_tokens 13056` and `reasoning_output_tokens 120` — so cached is a subset of input and reasoning a subset of output. Upstream codex-rs defines the same arithmetic (`non_cached_input() = input_tokens − cached_input()`, `blended_total() = non_cached_input() + output_tokens`) and its `TokenUsage` struct now carries `cache_write_input_tokens` (`openai/codex` `codex-rs/protocol/src/protocol.rs` @ `56012fafb86d`).

**OpenCode normalization and cost.** OpenCode's `getUsage` (`sst/opencode` `packages/opencode/src/session/session.ts` @ `9b0dd36cda0b`) stores `tokens.input = inputTokens − cacheRead − cacheWrite` and `tokens.output = outputTokens − reasoningTokens`, computes cost from models.dev rates with context tiers (`experimentalOver200K`) and reasoning charged at the output rate, derives Copilot cost from `totalNanoAiu`, and defaults cost to `0` when no rate applies. Confirmed empirically in local storage (`input=3` beside `cache.write=15072`). The reported-zero suppression is live in the indexed corpus: 4 OpenCode rows totaling 110,145 tokens carry reported `0`, including 15,087 claude-sonnet-4-5 tokens.

**Pi summary usage and per-message model.** Upstream Pi (`badlogic/pi-mono` `packages/agent/src/harness/session/types.ts` @ `7bdb16c28d79`) declares optional `usage` on `CompactionEntry` and `BranchSummaryEntry`, plus lane `UsageRecord`s with `cause: "assistant" | "compaction" | "branch_summary" | "tool" | …`. The local corpus predates this — 0 of 19 session files containing summary entries carry usage — so a representative fixture still needs a current Pi build. Real local Pi assistant payloads carry per-message `model` and `provider` (e.g. `"gpt-5.4"` / `"openai-codex"`); the adapter now prefers those fields, falls back to file-order `model_change` state, and clears a stale provider when a message changes only the model.

**LiteLLM coverage** (`BerriAI/litellm` `model_prices_and_context_window.json` @ `418c7c6012d7`, fetched 2026-08-21). Units are USD per single token. 126 models carry `cache_creation_input_token_cost_above_1hr` (2× base for Anthropic models — claude-fable-5 at $20/MTok); 60 carry `input_cost_per_token_above_200k_tokens` and 35 the cache-write equivalent; 14 carry `_above_128k_tokens` variants. GPT-5.x entries carry no cache-write cost through the gpt-5.5 family; the gpt-5.6 family carries one at 1.25× input. Embedded snapshot rates were spot-checked against list prices for every model in the local corpus; all match. Anthropic's 2026-08-10 Sonnet 5 announcement made the launch $2/$10 rate permanent and cancelled the planned September increase; the maintained open-ended card and a post-2026-09-01 regression test pin that correction.

**ccusage comparison** (`ryoppippi/ccusage` `rust/crates/ccusage-core/src/pricing.rs` @ `b936c29211b9`). The reference implementation ships three cost modes (`auto`/`calculate`/`display`), carries the four `_above_200k_tokens` tier fields, and — where cache rates are absent — falls back to `input×1.25` / `input×0.1` while tracking whether each rate was explicit. CCPeek deliberately prefers partial pricing with visible unpriced cache buckets over that multiplier default (see improvement 2).

**Claude streaming snapshots.** The first-wins dedupe risk was probed: the 8 largest local transcripts contain only byte-identical duplicate usage rows, no divergent same-key snapshots. Retaining the most complete duplicate is nevertheless implemented and covered synthetically; durability across a later reparse of the first-seen owner file remains separate follow-up work.

## Improvement status

### 1. Harden token capture

Claude duplicate completeness, Pi compaction/branch-summary usage, and Codex cache-write normalization are implemented with synthetic regression fixtures. Schema parse versions force recoverable unchanged sources through a full parse; rows whose source files disappeared remain archival and cannot gain fields that were never stored. Representative real-corpus fixtures for current Codex cache writes, current Pi summary usage, and legacy Claude resumes without `message.id` remain outstanding.

### 2. Distinguish missing rates from true zero rates

Implemented. Snapshot pruning preserves field absence, pricing returns per-bucket unpriced usage, and all cost surfaces expose partial results without treating missing cache prices as free.

### 3. Invalidate rollups when pricing changes

Implemented. The transactional rollup fingerprint covers exact snapshot bytes and the pricing algorithm version; an unchanged ingest pass rebuilds stale materializations.

### 4. Preserve provider identity

Implemented. Provider and model are stored separately, raw cost queries price by provider/model before folding into display-model groups, and the pricing diagnostic reports the exact candidate that resolved.

### 5. Support historical and tiered rates

Implemented for effective-dated cards and request-size tiers. Rows are priced independently so dates and thresholds are not crossed by aggregation, authoritative history gaps remain unpriced, and current LiteLLM long-context dimensions are retained. Historical coverage is intentionally sparse, service-tier modifiers remain unsupported, and sparse cards inherit current absolute tiers as documented above.

### 6. Split cache-write tiers

Implemented as total cache writes plus a one-hour subset. This representation preserves existing token totals and naturally treats irrecoverable legacy rows as five-minute writes.

### 7. Make cost provenance inspectable

Implemented through `ccpeek query pricing [--model provider/model]` and `/api/v1/pricing`: source, fetch time, algorithm, fingerprint, rollup currentness, resolved key, and present rate dimensions are inspectable. Usage rows continue to expose the reported/estimated split.

### 8. Add explicit cost modes

Implemented across sessions, session detail, usage, blocks, stats, pricing diagnostics, HTTP, CLI, MCP, and the UI. `auto`, `calculate`, and `display` use the row-level semantics documented above, URL navigation preserves the selected mode, and incomplete calculated or reported-only totals remain visibly marked.

### 9. Improve user-facing labeling

Implemented for the existing surfaces: money tooltips identify API-equivalent value and its reported/estimated rule, lower bounds name missing model or bucket rates, timeline agents come from indexed data, and unpriced-only days remain visible and exportable.

## Implementation order (agreed 2026-08-21)

The improvements above are sequenced as staged work; each stage uses fixed synthetic rates and asserts cross-surface agreement before the next begins.

1. **Merge documentation** — this document is canonical; dated evidence and pinned citations merged in (done).
2. **Rollup invalidation infrastructure** (improvement 3) — implemented: the snapshot content plus pricing-algorithm version is fingerprinted, persisted transactionally in `meta`, and tested against an unchanged corpus under a changed version.
3. **Reported-zero fallback** — implemented: nonzero tokens with reported `$0` use estimation in auto mode; raw zero is preserved; unknown models become visibly unpriced across all cost surfaces.
4. **Cache-write TTL support** (improvement 6) — implemented: `cache_write_tokens` remains the total, `cache_write_1h_tokens` is the subset, adapter parse versions force recoverable sources through one full reparse, and unavailable archived splits retain the five-minute assumption.
5. **Adapter capture hardening** (improvement 1) — implemented for Pi per-message provider/model and summary usage, Claude legacy cost-only rows and legacy dedupe, compare-and-update with first-seen ownership and an explicit completeness order, and Codex cache-write subset normalization with cumulative-delta/reset handling.
6. **Edge cases and population semantics** (improvement 2) — implemented: zero-token rows no longer create unpriced warnings, partial bucket pricing is persisted and exposed, and `parity_test.go` pins the intentional population rule that blocks require message timestamps while sessions include all usage and daily rollups fall back to session timestamps.
7. **Provider identity and diagnostics** (improvements 4, 7) — preserve provider separately from model; expose the resolved pricing key, rates, snapshot fingerprint, and the reported-versus-estimated decision. Implemented in schema v14 and the `pricing` query/API operation.
8. **Presentation** (improvement 9) — dynamic timeline agents, preserve unpriced-only days, consistent provenance and API-equivalent labeling. Implemented; zero-dollar unpriced days render warning-height markers and remain in CSV exports.
9. **Advanced pricing and modes** (improvements 5, 8) — implemented with effective-dated cards, per-request long-context tiers, exact fixed-point arithmetic, mode-specific materializations, and cross-surface mode exposure.

The pricing snapshot was refreshed after stage 2 landed, including permanent Sonnet 5 pricing, current long-context tiers, one-hour cache-write fields, and maintained history. Future refreshes are safe because unchanged rollups invalidate against the snapshot-and-algorithm fingerprint.

## Verification strategy

Cost changes should be tested with fixed synthetic rates rather than asserting against whatever values happen to be in the current embedded snapshot. The minimum regression matrix should cover:

- One record containing all four token buckets and an exact expected cost.
- A reported cost replacing, rather than supplementing, calculated cost.
- A session mixing reported and calculated rows.
- A session mixing models with different prices.
- A completely unknown model producing unpriced tokens and a lower-bound cost.
- A known model with a missing cache rate producing partial unpriced usage.
- Claude streaming snapshots retaining final output without double-counting.
- Resumed Claude sessions deduplicating the same final request.
- Pi message, compaction, and branch-summary reported costs.
- Codex last-turn usage, cumulative deltas, counter resets, duplicate events, cache reads, cache writes, and reasoning subsets.
- OpenCode additive reasoning being billed exactly once.
- A pricing-fingerprint change invalidating materialized rollups.
- Agreement between session, block, daily usage, stats, and API parity surfaces over the same synthetic corpus, including an effective-date boundary and a long-context tier.

For manual validation, compare a small session against the source transcript by grouping usage per model, applying the exact embedded rates, and checking reported and estimated rows separately. Do not compare only the final rounded UI value; inspect full-precision API output so display formatting does not hide arithmetic differences.
