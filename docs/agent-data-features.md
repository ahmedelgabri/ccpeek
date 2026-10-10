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
