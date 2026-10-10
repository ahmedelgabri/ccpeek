# Agent data features

Features built from data that agents already write locally but ccpeek ignored. Each section matches one jj revision.

## Codex model from session_meta

Current Codex rollouts name the model only in `session_meta.payload.base_instructions.provenance.model`. The adapter read the model from `turn_context`, which only 3 of 50 local rollouts carry. As a result, 13,904 of 14,063 Codex assistant messages had no model, and 45 of 56 Codex sessions listed no model.

- The session_meta model seeds the current model. A later `turn_context` still wins, so model switches within a session are kept.
- Codex `ParseVersion` is 5.
- Cost does not change. The model-less rows that carried usage had zero tokens. The fix restores the model filter and the per-session model list.
