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
