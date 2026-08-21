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

| Field | Meaning |
| --- | --- |
| `InputTokens` | Ordinary, non-cached input tokens |
| `OutputTokens` | Billable output tokens, including reasoning exactly once |
| `CacheReadTokens` | Input tokens served from a prompt cache |
| `CacheWriteTokens` | Input tokens written to a prompt cache |
| `ReasoningTokens` | Informational reasoning detail; never added to cost separately |
| `ReportedCostUSD` | Optional cost supplied by the agent |
| `ServiceTier` | Recorded provider tier when available; currently not used by pricing |

The normalization contract is important because providers disagree about reasoning. Codex reports reasoning as a subset of output, while OpenCode reports reasoning additively beside output. The Codex adapter leaves output unchanged and records reasoning only as detail; the OpenCode adapter adds reasoning to output. Downstream code must therefore never add `ReasoningTokens` to `OutputTokens`.

## Per-record cost selection

CCPeek currently has one effective cost mode: automatic, reported-first pricing. The `calculate` and `display` modes mentioned as plans in `docs/v2-plan.md` are not implemented.

For each `message_usage` row:

1. If `reported_cost_usd` is present, CCPeek uses that value and does not calculate another cost for the row's tokens.
2. If `reported_cost_usd` is absent, CCPeek looks up the message's model in the embedded pricing table and calculates a cost from tokens.
3. If the model cannot be resolved, the tokens are counted as unpriced and contribute no USD amount.

A reported value of `0` is still considered present. It suppresses calculated pricing for that record just like any other reported value.

The estimated formula is:

```text
estimated_cost_usd =
    input_tokens       × input_rate
  + output_tokens      × output_rate
  + cache_write_tokens × cache_write_rate
  + cache_read_tokens  × cache_read_rate
```

Rates are stored as USD per single token. No reasoning term appears in the formula because billable reasoning is already included in `output_tokens`.

At aggregate level:

```text
cost_usd = sum(reported_cost_usd) + sum(estimated_cost_usd)
```

Reported and estimated cost are mutually exclusive for an individual usage row, so this aggregation does not intentionally charge the same row twice.

The core calculated arithmetic lives in `pricing.Rate.Cost` and `db.AutoCost`. Session summaries, rolling blocks, and usage rollups call the shared implementation rather than maintaining separate formulas.

## Agent-specific capture and normalization

### Claude Code

The Claude adapter reads assistant `message.usage` fields as follows:

| Claude field | Canonical field |
| --- | --- |
| `input_tokens` | `InputTokens` |
| `output_tokens` | `OutputTokens` |
| `cache_read_input_tokens` | `CacheReadTokens` |
| `cache_creation_input_tokens` | `CacheWriteTokens` |
| `service_tier` | `ServiceTier` |
| top-level legacy `costUSD` | `ReportedCostUSD` |

Anthropic's `input_tokens` excludes cache reads and cache creation, so the four buckets are additive and match the cost formula directly.

Claude can repeat an API response across resumed or forked transcripts. CCPeek deduplicates usage across the same agent using `(message.id, requestId)` while retaining every transcript message. The current implementation keeps the first matching usage row it encounters. This prevents obvious double-counting, but it can underestimate output when Claude persists multiple streaming snapshots for one response and later snapshots contain a larger, final `output_tokens` value.

The dedupe check is skipped entirely when either key is empty. Legacy transcripts — the same generation that carries `costUSD` — predate `requestId`, so a resumed legacy transcript re-counts both tokens and reported cost. Separately, a legacy line carrying `costUSD` but no `message.usage` produces no usage row at all, so its reported cost is dropped.

Claude also records a nested cache-creation breakdown for five-minute and one-hour cache writes in newer logs. CCPeek currently stores only the combined cache-write count and prices all cache writes at the five-minute rate.

### Pi

For message entries with usage, the Pi adapter maps:

| Pi field | Canonical field |
| --- | --- |
| `usage.input` | `InputTokens` |
| `usage.output` | `OutputTokens` |
| `usage.cacheRead` | `CacheReadTokens` |
| `usage.cacheWrite` | `CacheWriteTokens` |
| `usage.cost.total` | `ReportedCostUSD` |

Pi already normalizes provider token accounting into mutually exclusive Anthropic-style buckets, and its output count includes normal and reasoning output. CCPeek therefore prefers Pi's reported total and does not calculate a second cost for those records.

The current adapter derives the active model from `model_change` entries in file order. Modern Pi assistant messages also carry their own provider and model, but CCPeek does not currently use those per-message fields. File-order model state is an approximation for branched sessions.

Modern Pi `compaction` and `branch_summary` entries can carry usage and reported cost for the LLM call that generated the summary. CCPeek currently does not capture that top-level usage, so those tokens and costs are omitted from session totals.

### Codex CLI

Codex token-count events contain cumulative totals and, in many logs, a `last_token_usage` record for the latest turn. CCPeek uses `last_token_usage` when available. Otherwise it subtracts the previous cumulative total and treats a lower total as a counter reset. Repeated events with an unchanged cumulative total and identical last-turn usage are suppressed.

Codex input includes cached input, so CCPeek currently normalizes:

```text
InputTokens     = input_tokens - cached_input_tokens
CacheReadTokens = cached_input_tokens
OutputTokens    = output_tokens
ReasoningTokens = reasoning_output_tokens
```

Codex reasoning is a subset of output and is not added again.

Current Codex formats also include `cache_write_input_tokens`. CCPeek does not yet parse this field. Logs containing cache writes therefore omit that token bucket and its cost, and their ordinary input should eventually be normalized as input minus both cache reads and cache writes — pending fixture verification that cache writes are in fact a subset of `input_tokens` rather than a disjoint bucket.

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

If an OpenCode usage record has no reported cost, its normalized tokens use calculated pricing. The adapter currently stores `modelID` but discards `providerID`, which can make fallback pricing less precise when the same model identifier is offered by several providers.

### Cursor

The Cursor adapter reads fixture-based `inputTokens`, `outputTokens`, `cacheReadTokens`, and `cacheWriteTokens` fields from message blobs. It does not capture a reported cost, so recognized models use calculated pricing. This mapping has not been validated against a representative real Cursor corpus and should be treated as provisional.

## Pricing data and model lookup

Pricing comes exclusively from `internal/pricing/snapshot.json`, an embedded, pruned copy of LiteLLM's `model_prices_and_context_window.json`. The snapshot records its source URL and fetch timestamp. `scripts/update-pricing.sh` downloads a new source file and keeps these four fields:

- `input_cost_per_token`
- `output_cost_per_token`
- `cache_creation_input_token_cost`
- `cache_read_input_token_cost`

The binary does not fetch prices at runtime. Updating the script output only affects binaries built with the new snapshot.

Model lookup is case-insensitive and tries candidates in order:

1. The complete recorded identifier.
2. Successively stripped slash-delimited provider or router prefixes.
3. A Vertex-style identifier without its `@date` suffix.
4. Bedrock identifiers without region and version decorations.

Exact provider-specific matches therefore win when present, but fallback to a bare model can use a generic rate that does not reflect the original provider, region, or negotiated price.

A model is considered priced when lookup finds a `Rate`. The update script currently replaces missing cache-rate fields with numeric zero. Consequently, a known model with a non-zero cache bucket and an absent cache rate is treated as fully priced with free cache tokens rather than partially unpriced. This is a known source of silent underestimation.

## Aggregation and materialization

Raw usage is stored per message in `message_usage`. Costs are exposed through two paths:

- Session summaries and rolling block queries aggregate raw usage by model and call `db.AutoCost` at query time.
- The Usage page and cost timeline read `rollup_usage_daily`, which materializes reported and estimated costs for dashboard speed.

Daily rollups group by day, agent, workspace, and model. A session that uses several models is priced independently for each model before the results are added. Session counts are recomputed as distinct sessions rather than summed across model rows.

Rollups are fully rebuilt after an ingest pass changes sessions or messages, after source pruning, or when usage exists but the rollup table is empty. A pricing-snapshot change by itself is not tracked as a rollup dependency. After upgrading to a binary with new prices, unchanged historical rollups can therefore retain the previous calculated values while query-time session costs use the new snapshot. A later session change triggers a full rebuild and makes them agree again.

The database stores costs as SQLite `REAL`, and Go calculates with `float64`. Values are not rounded during calculation or aggregation; formatting rounds only for display. This is suitable for analytics but not financial-ledger precision.

## Unpriced data

For an unreported usage row whose model cannot be resolved:

- Token totals are still included.
- Calculated cost contributes zero.
- Session and block responses include an `unpricedTokens` count.
- Daily usage rollups set `priced = 0`, exposed as `hasUnpriced`.
- The displayed USD amount is a lower bound.

Agent-reported costs do not require model lookup. A row with a reported cost and an unknown model is considered costed because CCPeek does not need a rate to use the reported value.

The current unpriced mechanism operates at model lookup level, not individual rate-field level. It does not detect missing cache rates represented as zero.

The mechanism also has a zero-token false positive: a group whose only unreported rows carry zero tokens and an unresolvable model (Claude Code `<synthetic>` error stubs are the common case) still sets `priced = 0`, so the usage surface reports `hasUnpriced` for a group with no actual unpriced tokens — while session and block surfaces, which count unpriced tokens rather than groups, report zero for the same data.

## Pricing dimensions not currently modeled

The LiteLLM source and provider APIs contain more pricing dimensions than CCPeek preserves:

- Historical rate changes and effective dates.
- Long-context thresholds such as rates above 128k, 200k, or 272k tokens.
- Anthropic one-hour cache creation versus five-minute cache creation.
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

**Pi summary usage and per-message model.** Upstream Pi (`badlogic/pi-mono` `packages/agent/src/harness/session/types.ts` @ `7bdb16c28d79`) declares optional `usage` on `CompactionEntry` and `BranchSummaryEntry`, plus lane `UsageRecord`s with `cause: "assistant" | "compaction" | "branch_summary" | "tool" | …`. The local corpus predates this — 0 of 19 session files containing summary entries carry usage — so fixtures need a current Pi build. Real local Pi assistant payloads carry per-message `model` and `provider` (e.g. `"gpt-5.4"` / `"openai-codex"`), which the adapter ignores in favor of file-order `model_change` state; the openai-codex provider is also the source of the gpt-5.4 cache-write tokens in the corpus.

**LiteLLM coverage** (`BerriAI/litellm` `model_prices_and_context_window.json` @ `418c7c6012d7`, fetched 2026-08-21). Units are USD per single token. 126 models carry `cache_creation_input_token_cost_above_1hr` (2× base for Anthropic models — claude-fable-5 at $20/MTok); 60 carry `input_cost_per_token_above_200k_tokens` and 35 the cache-write equivalent; 14 carry `_above_128k_tokens` variants. `claude-sonnet-5` is listed at the $2/$10 introductory rate that ends 2026-08-31 — the embedded snapshot (fetched 2026-07-10) predates that transition, a concrete instance of the temporal-pricing limitation. GPT-5.x entries carry no cache-write cost through the gpt-5.5 family; the gpt-5.6 family carries one at 1.25× input. Embedded snapshot rates were spot-checked against list prices for every model in the local corpus; all match.

**ccusage comparison** (`ryoppippi/ccusage` `rust/crates/ccusage-core/src/pricing.rs` @ `b936c29211b9`). The reference implementation ships three cost modes (`auto`/`calculate`/`display`), carries the four `_above_200k_tokens` tier fields, and — where cache rates are absent — falls back to `input×1.25` / `input×0.1` while tracking whether each rate was explicit. CCPeek deliberately prefers partial pricing with visible unpriced cache buckets over that multiplier default (see improvement 2).

**Claude streaming snapshots.** The first-wins dedupe risk was probed: the 8 largest local transcripts contain only byte-identical duplicate usage rows, no divergent same-key snapshots. The retain-most-complete change therefore remains fixture-gated (see the verification strategy).

## Recommended improvements

### 1. Fix known token omissions before adding pricing features

Add regression fixtures and then correct the three confirmed capture gaps: retain the most complete Claude streaming usage snapshot, capture Pi compaction and branch-summary usage, and normalize Codex cache-write tokens. Existing indexed databases would need a rebuild after adapter fixes because missing source fields cannot be repaired from the current canonical rows.

### 2. Distinguish missing rates from true zero rates

Preserve whether each LiteLLM field was absent instead of converting absence to zero. Pricing should return a per-bucket result so a model can be partly priced: known input and output cost plus explicitly unpriced cache tokens. This avoids both silently free tokens and incorrectly rejecting all tokens for an otherwise known model.

### 3. Invalidate rollups when pricing changes

Store a pricing-snapshot fingerprint or version in database metadata when rollups are generated. On startup, rebuild rollups when that fingerprint differs from the embedded table. This keeps materialized Usage values consistent with query-time session values without requiring unrelated session activity.

### 4. Preserve provider identity

Store provider and model separately, or preserve a normalized `provider/model` key alongside the display model. Exact provider-specific rates should be preferred, and fallback to a generic model should be visible as an approximation rather than silently treated as equally authoritative.

### 5. Support historical and tiered rates

A billing-grade design needs effective-dated price cards and enough per-request context to select long-context and service-tier rates. Applying today's base rate to all historical usage is simple and reproducible, but it cannot answer what an old request would actually have cost at the time.

### 6. Split cache-write tiers

Capture five-minute and one-hour cache-creation tokens separately where Claude exposes them. Until the schema supports that distinction, documentation and UI should explicitly identify the current five-minute-rate assumption.

### 7. Make cost provenance inspectable

Expose the snapshot source, fetch timestamp, resolved model key, selected rates, and reported-versus-estimated decision through a diagnostic command or API response. A user investigating a surprising total should be able to trace one usage row from source fields to final arithmetic.

### 8. Add explicit cost modes only if users need them

If the planned modes are implemented, their semantics should be unambiguous:

- `auto`: use reported cost when present, otherwise calculate.
- `calculate`: ignore reported cost and price every possible token row from the selected price card.
- `display`: show only agent-reported costs and mark all other usage as having no reported cost.

Until those modes exist, user-facing documentation should describe only automatic reported-first behavior.

### 9. Improve user-facing labeling

Every dollar surface should consistently say whether a value is reported, estimated, mixed, or a lower bound. Subscription-backed activity should be labeled API-equivalent rather than spend. The current Usage page shows a reported/estimated split, but that distinction and the subscription caveat are not consistently visible on session and overview surfaces.

## Implementation order (agreed 2026-08-21)

The improvements above are sequenced as staged work; each stage uses fixed synthetic rates and asserts cross-surface agreement before the next begins.

1. **Merge documentation** — this document is canonical; dated evidence and pinned citations merged in (done).
2. **Rollup invalidation infrastructure** (improvement 3) — fingerprint the snapshot content plus a pricing-algorithm version, persist it transactionally in `meta`, rebuild rollups on change; test an unchanged corpus under a changed pricing version.
3. **Reported-zero fallback** — nonzero tokens with reported `$0` use estimation in auto mode; the raw zero is preserved as provenance; unknown models become visibly unpriced. Ships with stage 2, or with a schema bump that guarantees rollup recreation.
4. **Cache-write TTL support** (improvement 6) — keep `cache_write_tokens` as the total, add `cache_write_1h_tokens` as a subset, price `total − 1h` at the five-minute rate and `1h` at its own rate. Version adapter parsing/source cursors to force re-parsing of unchanged files; archived rows whose sources are gone remain permanently unsplit under the five-minute assumption.
5. **Adapter capture hardening** (improvement 1) — Pi per-message provider/model and summary usage; Claude legacy cost-only rows and legacy dedupe; define the duplicate winner and row ownership before implementing compare-and-update, with fixtures covering differing snapshots across resumed sessions; Codex cache writes stay fixture-gated until subset semantics are verified.
6. **Edge cases and population semantics** (improvement 2) — fix the zero-token `hasUnpriced` false positive, add partial bucket pricing, and either normalize timestamp populations across sessions/blocks/rollups or explicitly document and test why they include different rows. Arithmetic parity tests run on well-formed aligned fixtures; intentional population differences are tested separately in `parity_test.go`.
7. **Provider identity and diagnostics** (improvements 4, 7) — preserve provider separately from model; expose the resolved pricing key, rates, snapshot fingerprint, and the reported-versus-estimated decision.
8. **Presentation** (improvement 9) — dynamic timeline agents, preserve unpriced-only days, consistent provenance and API-equivalent labeling.
9. **Deferred** (improvements 5, 8) — historical and tiered pricing, explicit cost modes, and billing-grade decimal arithmetic remain later work.

Once stage 2 lands, refreshing the pricing snapshot — including the Sonnet 5 intro-price transition — is safe, because unchanged rollups will invalidate correctly.

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
- Agreement between session, block, and daily usage totals over the same synthetic corpus.

For manual validation, compare a small session against the source transcript by grouping usage per model, applying the exact embedded rates, and checking reported and estimated rows separately. Do not compare only the final rounded UI value; inspect full-precision API output so display formatting does not hide arithmetic differences.
