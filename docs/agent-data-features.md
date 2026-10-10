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

Known existing behavior, not changed here: usage dedupe gives a copied turn's cost to whichever copy indexed first, so a continued session can show less cost than its own turns made.

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
