# Agent data features

Features built from data that agents already write locally but ccpeek ignored. Each section matches one jj revision.

## Codex model from session_meta

Current Codex rollouts name the model only in `session_meta.payload.base_instructions.provenance.model`. The adapter read the model from `turn_context`, which only 3 of 50 local rollouts carry. As a result, 13,904 of 14,063 Codex assistant messages had no model, and 45 of 56 Codex sessions listed no model.

- The session_meta model seeds the current model. A later `turn_context` still wins, so model switches within a session are kept.
- Codex `ParseVersion` is 5.
- Cost does not change. The model-less rows that carried usage had zero tokens. The fix restores the model filter and the per-session model list.

## Claude subagent sessions

Claude Code writes each subagent to its own transcript beside the parent session: `projects/<project>/<session>/subagents/agent-<id>.jsonl`, and workflow agents under `subagents/workflows/<wf>/`. Each has an `agent-<id>.meta.json`. Discovery never read these files. Older versions inlined subagent turns into the parent as `progress` lines, which the adapter skips. So in both layouts the subagent cost was not counted.

- Each subagent file becomes a session with external id `agent-<id>` and a `subagent_of` relation. The relation points to the parent subagent when `meta.json` names `parentAgentId`, otherwise to the main session. The evidence keeps `agentType`, `description`, `toolUseId` and `workflow`.
- The meta `description` titles the session. Without one, the first prompt does.
- Lines in a subagent file carry the parent's `sessionId` and `isSidechain: true`. In the subagent's own session these are expected, so they raise no identity warning and messages are not flagged as sidechains.
- `meta.json` is read at parse time, not as a fingerprint companion. The append-cursor path hashes only the primary file, so a companion would make the stored hash flip between the two code paths. Meta files are written once, at spawn.
- Query layer:
  - `sessions` hides subagent sessions unless `subagents=true`.
  - Every summary carries `parentId`, `subagentType` and `subagentCount`.
  - `session` lists direct `subagents` and a `subagentTotal` (sessions, messages, tokens, cost) over all descendants, via a recursive CTE.
  - `attachCosts` became `accumulateCosts`, which lets several sessions share one summary slot.
- UI:
  - The sessions list has a `subagents` toggle (`?subagents=show`). Subagent rows show `↳ <type>`, and parents show `+N subagents`.
  - The session detail has a Subagents tab and a "Subagent cost" tile.

Local result: 1,721 subagent sessions linked, none pending. Claude cost went from $6,776 to $8,087 (+$1,311), and no usage dedupe key is duplicated. Several recent subagent sessions use models newer than the pricing snapshot, so the added cost is a lower bound.

## Thinking and images in the transcript

Raw message payloads already held the model's reasoning (`thinking` blocks in Claude and Pi, `reasoning` parts in OpenCode) and base64 images (pasted screenshots, plus screenshots returned inside tool results). Only text reached the transcript.

- Adapters lift reasoning text into `canon.Message.Thinking` and record each image as a `canon.ImageRef`: its media type plus an RFC 6901 JSON pointer to the base64 data inside the stored payload. The bytes are not copied a second time.
- Schema v19 adds `messages.thinking_text` and `messages.images_json`. The migration uses a conditional column add, which keeps it safe against a database that already has the latest shape, like the `IF NOT EXISTS` migrations before it.
- ParseVersion bumps: Claude 4, Pi 5, OpenCode 4. Archived sessions whose source is gone keep empty columns, but their raw payloads still hold both.
- Codex reasoning is encrypted in every local rollout, so Codex has no thinking text to show.
- The `transcript` op takes `thinking=true` (off by default, for token budget) and always lists `images` with index and media type.
- `GET /api/v1/sessions/{agent}/{id}/transcript/{seq}/images/{index}` serves the decoded bytes. It serves only PNG, JPEG, GIF and WebP, with `nosniff` and a sandbox CSP. An SVG or other document type returns 404, never content in the app's origin.
- UI: thinking renders folded to one dim line above the reply, and images render as fixed-height thumbnails that open full size.
- The deep-link anchor now waits for the range's tool chips. Chips arrive after the messages and grow the rows they land in. Before this change, targets were usually tool-only rows that appeared only once chips loaded, so the race stayed hidden. Rows that carry thinking or images appear at once, and the anchor landed 30+ rows off.

Local result: thinking on 2,028 Claude and 17,814 Pi messages. Most Claude thinking blocks are empty: 7,028 of 7,772 carry only a signature. Images: 1,486 Claude and 153 Pi.

A full reparse exposed an existing slow path: `restoreUsageOwners` scanned every message of the agent per usage key. It is fixed in its own revision ("perf(db): seek by content id when restoring usage owners"). The reparse went from ~45 minutes to under 2 minutes.

## Legacy subagent files

The oldest Claude Code layout kept subagents beside their parent as `projects/<project>/agent-<id>.jsonl`, with no meta.json. These always indexed (their cost was counted), but as standalone sessions with no link to the parent. Every line carries the parent's `sessionId`, which is the only record of the link.

- The adapter treats top-level `agent-*.jsonl` files as subagents. It takes the parent from the first line's `sessionId` and emits `subagent_of` with evidence `source: legacy-agent-file`.
- Claude `ParseVersion` is 5, so already-indexed legacy files gain the link.

Local result: 239 legacy subagent sessions linked, none pending.

## Session graph: resumes and fork points

Resuming a Claude session under a new id copies its history into the new file, and every copied line keeps the session id it was written under. A chain of resumes copies every ancestor, oldest first. ccpeek flagged these lines as an identity mismatch (51 warnings locally) and recorded no lineage. Forked agents (`agentType: fork`) carry a `fork-context-ref` line that names the parent entry their context ends at.

- The adapter tracks copied lines instead of warning. The last foreign session id is the direct parent, and its last copied entry is the branch point. It emits `resumed_from` with evidence `branchUuid` and `copiedEntries`.
- The identity warning stays only for a file made entirely of other sessions' lines: a copy or rename.
- Fork agents add `branchUuid` to their `subagent_of` evidence.
- Claude `ParseVersion` is 6.
- `session` relations gain `atSeq`: the seq, in the parent session, of the branch entry.
- UI: relation chips read "resumed from / continued in / fork of / forked into". An outgoing chip opens the parent at the branch point (`@#N`).

Local result: 10 `resumed_from` edges (one pending: its parent file is gone), 56 edges with a branch point (10 resumes and 46 fork agents), and 0 identity warnings.

A subagent's lines always name its parent, and a legacy subagent can outlive a resume of that parent, so its lines carry two parent ids. Subagent files are therefore excluded from resume detection. Without that exclusion, 40 legacy agent files produced false "continued in" edges.

Usage attribution across copies is fixed later, in "Resumed turns charged where they happened".

## Errors and the Insights page

Transcripts record failed model requests (Claude's synthetic `isApiErrorMessage` lines with `error`, `apiErrorStatus` and `quotaLimits`), interrupted turns (`[Request interrupted by user…]`), refused tool calls (`toolDenialKind` on the tool_result line), and Pi turns ending in `stopReason: error | aborted` with `errorMessage`. None of these were countable.

- New `session_events` table (schema v20): session, transcript anchor (`message_seq`), `kind`, `at`, `label`, `status`, `duration_ms`, `detail` (bounded at 400 bytes), and `payload_json` for kind-specific data. It is cleared with a session's other children on full reparse. Later features add kinds instead of tables.
- `RecordSink` gains `SessionEvent`.
- Event kinds: `api_error` (label = error class), `aborted`, `tool_denied` (label = denial kind).
- Pi's free-text errors map onto Claude's classes: `rate_limit`, `connection`, `invalid_request`, `authentication_failed`, `server_error`, with `error` as the fallback.
- Claude `ParseVersion` is 7 and Pi's is 6.
- New ops (CLI, MCP and HTTP, generated from the registry):
  - `session-events` lists events newest first. Filters: kind list, agent, one session, dates.
  - `event-stats` counts events by kind and one of label, status, agent or day, with distinct sessions and duration totals.
  - `tool-stats` gives calls and failed results per tool.
- `session` reports per-kind event counts.
- UI:
  - A new Insights page (`/insights`, URL-held tab, agent and date filters). Its Errors tab shows totals, error classes, denial reasons, the tools that fail most, and a recent list that links into each transcript at the event.
  - Sessions with events get an Events tab.

Local result: 638 API errors (521 rate limits), 393 aborted turns, 150 refused tool calls, and a 4.3% tool failure rate across 118k calls.

## Pull requests

Claude Code writes a `pr-link` line (`prNumber`, `prUrl`, `prRepository`) each time it opens or checks a PR. Locally that is 457 lines for 31 PRs: one PR was rewritten 162 times in one session. Some lines precede the session's first message.

- New event kind `pr_link`. Label is the repository and detail is the URL; the payload carries number, URL and repository.
- The adapter emits one event per URL per parse. Events seen before the session row exists wait in a buffer until it does.
- Claude `ParseVersion` is 8.
- UI:
  - The Insights "pull requests" tab lists each PR (newest first) with every session that linked it. Each session link opens the transcript at the link point.
  - Sessions with PRs show them as chips under the header.
  - Only `http(s)` URLs render as links, since they come from transcripts.

Local result: 31 PRs across 22 sessions.

## Timing

Claude Code writes a `system/turn_duration` line (`durationMs`, `messageCount`) after each turn. Codex `task_complete` events carry Unix-second `started_at`/`completed_at`, and newer rollouts add `duration_ms` and `time_to_first_token_ms`. Every agent timestamps tool calls and their results, but ccpeek stored only the call's start, as second-precision text.

- New event kind `turn` with the wall time in `duration_ms`. Claude uses `turn_duration`. Codex uses the exact duration when recorded, otherwise the second bounds; the payload keeps first-token latency.
- Schema v21 adds `tool_calls.started_ms` (millisecond start) and `duration_ms`.
- `canon.ToolResult.At` carries the result's arrival time, and the writer sets `duration_ms` to arrival minus start, ignoring negative spans. Pi's nested codemode calls bring their own `durationMs` through `canon.ToolCall.DurationMs`.
- `ParseVersion`: Claude 9, Pi 7, Codex 6.
- `tool-stats` gains `timed`, `totalMs`, `avgMs`, `maxMs` and `sort=errors|total|avg`. `session-events` gains `sort=time|duration`.
- The Insights "timing" tab shows timed turns, average and longest turn, average turn per agent, where tool time goes, and the longest turns (each linking to its transcript). The shared `fmtDuration` moved to `time.ts` and gained ms and tenth-of-a-second precision.

Tool time is wall time from call to result. It includes waits for permission prompts and user answers, which is why `AskUserQuestion` and `ExitPlanMode` rank high. The panel says so.

Local coverage: 68,020 of 76,431 Claude calls and 41,590 of 41,816 Pi calls are timed. Most Codex tool rows come from archived sessions with no source left. Codex `custom_tool_call` items are still not parsed (an existing gap). Turns: 2,354 Claude and 49 Codex. Pi records no turn durations.

## Compactions and context fill

Claude writes `system/compact_boundary` lines (`compactMetadata`: trigger, tokens before and sometimes after, duration), and Pi writes `compaction` entries with `tokensBefore`. Neither was countable. Every request's usage already holds how much context it sent.

- New event kind `compaction`. Label is the trigger (auto or manual, Claude only). The payload holds `preTokens` and `postTokens`, and the detail reads "N → M tokens" or "N tokens before".
- Claude's boundary lines are now transcript entries of kind `compaction`, not `message`.
- `ParseVersion`: Claude 10, Pi 8.
- New `context` op: one session's prompt tokens (input plus cache reads and writes) per assistant request in order, plus its compactions.
- UI:
  - Sessions with usage get a Context tab: a single-series line of prompt size per request with compaction markers, a snapping crosshair tooltip (click opens the transcript there), a compactions list, and a table view.
  - Insights gains a compaction tab: totals, manual count, average context before, compactions per agent, and the recent list.

Local result: 46 Claude (all auto) and 74 Pi compactions. Codex records none.

## Hooks

Claude Code records every hook run as an `attachment` line: `hook_success` (hook event, hook name, command, exit code, duration), `async_hook_response` (exit code, no command or duration), and `hook_blocking_error`. That is about 55k runs locally, none of them visible.

- New event kind `hook`.
  - Label is the hook event (PreToolUse, Stop, …).
  - Detail is the command, or the hook name for async responses, which record no command.
  - Status is `ok`, `error` (non-zero exit) or `blocked`.
  - The payload holds the hook name and exit code.
- Hook stdout and stderr are never decoded or stored. They are arbitrary program output.
- Claude `ParseVersion` is 11.
- `event-stats` gains `group=detail` and a `failures` count (status error or blocked). `session-events` gains a `status` filter.
- The Insights "hooks" tab shows runs, failures, time in hooks, runs per hook event, a per-command table (runs, failures, avg, max, total), and the failed or blocked runs linked to their transcripts.

Local result: 55,226 hook runs taking 39 minutes in total. 846 failed, mostly async hooks exiting 127 (command not found), and 8 blocked the agent.

## Lines changed

Claude's edit and write results carry `toolUseResult.structuredPatch` (and the full content for a new file), and Pi's edit results carry a unified `details.patch`. ccpeek could not say how much code an agent changed, or where.

- `canon.CountDiffLines` counts `+`/`-` lines in any of these diff shapes, skipping file headers. `canon.CountTextLines` sizes a new file.
- Schema v22 adds `tool_calls.lines_added` and `lines_removed`, NULL when unknown. A size comes either with the call (`ToolCall.Lines`) or with its result (`ToolResult.Lines`).
- Claude sizes a `create` by its content and an edit or overwrite by its patch. Pi sizes edits by their patch and writes by their content.
- `ParseVersion`: Claude 12, Pi 9.
- New `changes` op: lines added and removed, change count, files and sessions, grouped by day, agent or file. Relative paths resolve against the session's working directory; otherwise every repository's `README.md` would merge into one row.
- `session` reports `lines` (added, removed, files).
- UI: sessions with sized changes get a "Lines changed" tile. The Insights "lines changed" tab shows totals, net, lines per agent, and the 50 most-changed files.

Coverage and limits:

- Sized locally: 8,360 of 11,238 Claude edits, 2,749 of 3,344 Claude writes, 3,528 of 6,283 Pi edits, and all 1,597 Pi writes. Older Claude versions and some Pi edits record no patch.
- A Pi write that overwrites a file records no removed lines anywhere, so its removals count as zero.
- Codex `apply_patch` was not sized: no local rollout contains one to verify the format against.

Local result: +529,888 / −71,085 lines across 16,234 sized changes.

## Effort and thinking level

Claude assistant lines carry `effort` and, when a turn overrides it, `perTurnEffort`. Pi stamps `thinkingLevel` on newer assistant messages and records changes as `thinking_level_change` entries. Codex `turn_context` carries `effort`, but only in a few rollouts. Nothing connected a level to what it cost.

- `canon.Message.Effort`, stored in `messages.effort` (schema v23), is set on assistant entries:
  - Claude: `perTurnEffort`, else `effort`.
  - Pi: the message's own level, else the last level change.
  - Codex: the last `turn_context` effort.
- `ParseVersion`: Claude 13, Pi 10, Codex 7.
- The session-cost pricing loop became `priceUsageRows`, which prices usage rows into slots under any string key. Sessions and effort share it, so a level's cost is computed exactly like a session's.
- New `effort` op: requests, tokens and cost per level, per agent (`group=level`) or per day. Level is empty where none was recorded.
- The Insights "effort" tab shows requests with a level, the most used level, cost at a recorded level, a per-day stacked chart, and a per-agent level table with cost per request. The chart uses one hue stepped light to dark because levels are ordinal; "not recorded" is neutral gray.

Local result: Claude ran 14.6k requests at high and 3.7k at xhigh. Pi ran 15.3k at high and 14.9k at xhigh. 38.8k Claude requests predate the field.

## Pricing refresh

The embedded LiteLLM snapshot (fetched 2026-09-06) had no `claude-opus-5-5`, which left 3.45B Claude tokens unpriced, so recent cost figures were lower bounds.

- The snapshot is refreshed. `claude-opus-5-5` now prices at $1,208 locally, and Claude has no unpriced tokens left.
- `scripts/update-pricing.sh` now keeps models that upstream dropped. LiteLLM deletes retired models, and a plain refresh would have removed 504 entries, including `claude-opus-4-1`, `claude-opus-4-20250514` and `claude-sonnet-4-20250514`, which archived sessions use. Missing models keep their last snapshot rate; upstream wins for every model it lists. The script copies the old snapshot aside first, because the output redirect truncates it before jq runs.
- Still unpriced: OpenCode's `kimi-k2.5-free` (95k tokens). It is a free tier that LiteLLM does not list, and it really costs nothing.

## Pi multi-edit calls

Current Pi sends every replacement of an edit in one `edits: [{oldText, newText}]` array. The adapter read only the older top-level `oldText`/`newText`, so 4,773 of 6,283 Pi edits, nested codemode edits included, had no diff.

- `piToolArgs.editTexts` joins the replacements into one old text and one new text, separated by a `⋯` line. The separator is identical on both sides, so the line diff keeps it as an unchanged line between hunks.
- Pi `ParseVersion` is 11. Locally, 4 Pi edits still have no diff text; their arguments were too large for Pi to record.

The 395 `claude-code` "Edit" calls with an `edits` array, and 1,510 more with Pi-shaped arguments, come from 68 v1-imported sessions. They are Pi sessions that v1 stored under `claude-code`. They duplicate sessions v2 indexes under `pi` (same file, same id), and they carry no usage, so cost is not double-counted. They are archive rows and cannot be reparsed, so this fix does not reach them.

## Codex custom tools and patches

Researched against the codex-rs source (`protocol/src/models.rs` `ResponseItem`, `apply-patch/src/parser.rs`, `rollout/src/policy.rs`) and real rollouts published by other projects:

- `custom_tool_call` (`name`, freeform string `input`) and `custom_tool_call_output` carry Codex's freeform tools. Current Codex sends `apply_patch` this way, with the patch as the input, and code mode as the `exec` tool with JavaScript as the input. Neither was parsed.
- Tool outputs (`function_call_output.output`, `custom_tool_call_output.output`) are a string or an array of content items. The adapter decoded `output` as a string, which failed the whole item, so array outputs were dropped. Locally that was 11 of 12 function outputs.
- Older Codex sent `apply_patch` as a `function_call` with `{"input": patch}`.

Changes:

- Both item types are parsed. Outputs flatten to text: a string, or the content items' text joined by newlines. Images are dropped, as Codex's own `to_text` does.
- `apply_patch` in either form becomes a `file_edit` on the patch's first file (`*** Add/Update/Delete File:`), sized by its `+`/`-` lines. The input keeps every file the patch touches.
- Code-mode `exec` becomes a `script`, with its source shown and formatted like Pi codemode.
- Codex `ParseVersion` is 8.

Locally: 25 code-mode scripts are now visible, and every `js`/`wait` call is paired with its result and timed. No local rollout contains `apply_patch`, so the test uses the real shapes found in public rollouts.

Not done: code mode's inner commands. Paginated rollouts record each one as an `item_completed` `CommandExecution`, and they record directly called commands that way too. Indexing those items could double-count shell calls, and the local data cannot confirm how their ids relate to `call_id`.

## Meta lines and session titles

Six Claude sessions were titled "Caveat: The messages below were generated by the user while running local commands…". Claude Code 2.0.x wrote that caveat as a plain user message flagged `isMeta: true`. Later versions wrap it in `<local-command-caveat>`, which the title filter already rejects.

- User lines flagged `isMeta` never title a session. Claude Code injects them (caveats, skill bodies); the user did not type them. This covers the old caveat and any other injected line, where one more prefix rule would cover only the caveat.
- Claude `ParseVersion` is 14.

Locally, five sessions now carry their first real prompt as the title. The sixth only ran `/plugin` commands and is untitled.

## Resumed turns charged where they happened

Every file in a Claude resume chain holds the whole history up to that point, so one turn (message id, request id) appears in every copy. Usage dedupe kept one row per turn, on the copy indexed first. Files index in name order, not time order: in one local chain of 10 sessions, the newest (`016a7091`) sorted first and owned all the turns, while its 9 ancestors showed $0.

- `Store.AssignUsageOwners` runs in `reconcile` after each pass that changed data, before rollups. For every deduplicated turn present in a changed session (`dirty_sessions`), it moves the usage row to the copy in the session that ended first (earliest `modified_at`; then lowest session id; then the turn's first line). Each later copy only extends the history, so that is where the turn happened. Both sessions are marked dirty, so their rollups recompute.
- Totals cannot change: the row moves between identical copies, which share model and timestamp.
- A pass with nothing to move commits nothing, so it does not bump the archive generation.

Locally, the 10-session chain now charges each session its own turns. On a full reparse the pass adds about 15–20 seconds.

## Duplicate v1 imports of Pi sessions

v1 indexed some Pi session files as Claude sessions. The v1 importer counts a session as an orphan when v2 holds no session with its id, but it looked only under `claude-code`. So it imported 68 Pi sessions as `claude-code` orphans, beside the Pi sessions v2 indexes from the same files. They carried no usage, so cost was never double-counted, but they duplicated 15,810 messages in session lists and search.

- Migration v24 deletes v1 imports whose id belongs to a non-imported session under another agent. Their search docs are deleted first, because FTS is kept in step by explicit deletes, not by the cascade.
- `v2HasSession` now checks every agent, so re-running the import cannot bring the duplicates back. Session ids are UUIDs, unique across agents.

Locally, imported-v1 sessions went from 156 to 88; the 88 left are genuine orphans. The FTS integrity check passes.

## Effort chart matches the Usage chart

The effort tab's per-day chart was a hand-drawn SVG. It now uses the same component as the Usage page's cost chart, so it looks and behaves the same: ECharts on a real time axis, a legend, an axis tooltip, wheel and slider zoom, drag to select a range, a reset control that appears only when zoomed, and CSV export (`ccpeek-daily-effort.csv`).

- `StackedTimeline` (new) holds the chart, zoom, selection, reset and CSV behaviour. `CostTimeline` is now a thin wrapper that fetches cost per agent; its behaviour, accessible name and CSV columns are unchanged. `EffortTimeline` stacks requests per day by level and is lazy-loaded like the cost chart.
- Canvas cannot evaluate `color-mix()`, so level shades are mixed in sRGB from the resolved accent and surface colors. "Not recorded" stays neutral gray. Level ordering and labels moved to `pages/insights/levels.ts`, shared by the table and the chart.
- The e2e drag-select/reset test now runs on both charts.

## Simplify pass

A review of the whole stack for reuse, simplification, efficiency and altitude, then a cleanup revision. Query outputs on a copy of the real archive match the previous build, except for the additive fields noted below.

Fixed:

- **One pricing path.** `CostTotals` (tokens, automatic/reported/estimated cost, unpriced tokens) is embedded in `SessionSummary`, `SubagentTotal` and `EffortRow`, and filled by one `costAcc` accumulator. `priceUsageRows` creates accumulators lazily, so `Effort` is one query instead of a key pass plus a pricing pass, and the subagent total uses a single accumulator. `SubagentTotal` and `EffortRow` gain the reported/estimated split and `unpricedTokenTypes` (additive JSON).
- **Query helpers.** `windowClauses` (agent plus day window) replaces four copies; `placeholders` and `limitClause` replace hand-built IN lists and LIMITs. `canon.EventKinds` is the one list of event kinds, read by the kind filter and the ops description. The fork branch-point lookup runs only for relations that carry a `branchUuid`.
- **Ownership order shared.** `restoreOwnerSQL` ranks copies with the same `usageOwnerOrder` as `AssignUsageOwners`, so a restored owner is already the one reconcile would pick. `v2HasSession` now seeks the `(agent_id, external_id)` unique index.
- **Image serving** walks the JSON pointer one raw level at a time instead of decoding the whole message.
- **Adapters.** Claude's `toolUseResult`, Pi's `details` and Codex's `output` are reduced to what is read while the line decodes, so large tool results are never copied. Claude metadata events go through one `queueEvent`. Codex builds function and custom tool calls in one place, and `exec` maps to the script kind in `normalizeTool`. `canon.JoinThinking` and `canon.CompactionDetail` replace per-adapter copies.
- **UI.** `isCodeKind` replaces four shell-or-script checks; `get()` takes numeric params; the Insights panels use `EmptyNote error=` and a shared `TableHead`; the context chart memoizes its geometry so hover does not rebuild the path; effort levels include Pi's `off` and `minimal`, with a `NOT_RECORDED` constant instead of comparing labels; file-local exports and the `fmtAxisCount` wrapper are gone; session tabs gate on `counts`.
- **Tests.** `ingestRoots` and `claudeSessionsService` (query) and `handlerFor` (api) replace repeated store/ingest setup.

Skipped:

- A generic HTTP handler for ops: the explicit per-route handlers are the documented convention.
- Typed relation fields instead of evidence JSON, and resolving relative file paths at ingest: both are schema or reparse changes.
- Canonical effort normalization in `canon`, covering indexes for the new aggregates, a thinking preview in list responses, and server-side aggregation for the PR and compaction tabs: behaviour or storage trade-offs, not cleanups.
- `useTooltip` in the context chart, reusing `SessionRow` in the subagents tab, and a shared `ToggleButton`: UI changes beyond the reviewed diff.
- A set-based `AssignUsageOwners`, adapter-test helpers, and trimming the `ParseVersion` changelog comments.
