# Agent fixture corpus

Per-agent fixtures for the v2 adapter framework (`internal/agent`,
`internal/adapters/*`). Each directory mirrors one agent's data root exactly
as the adapter expects to find it on disk:

| Directory      | Mirrors       | Format notes                                                                                                                                                                                                |
| -------------- | ------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `claude-code/` | `~/.claude`   | JSONL sessions under `projects/<encoded-cwd>/`, with real `message.usage`, `message.model`, `parentUuid`, `isSidechain`, `requestId` fields (v1's `testdata/` fixtures predate usage capture and lack them) |
| `pi/`          | `~/.pi/agent` | JSONL sessions under `sessions/<encoded-cwd>/`, per the documented spec (`pi-mono` `docs/session-format.md`, header `version` 1–3)                                                                          |

Conventions:

- Fixtures are **real-shaped**: field names and envelope structure match what
  the agent actually writes, so adapter parsers are tested against reality,
  not against our own simplifications. When an agent version changes its
  format, add new fixtures alongside the old ones rather than editing them —
  parsers must stay version-tolerant.
- Interesting cases belong here: resumed sessions repeating assistant entries
  (usage dedupe), sidechains, tree branching, compaction, model changes,
  unpriced models.
- Keep fixtures small. They exist to pin parser behavior, not to benchmark.

The legacy `testdata/` root (plans, todos, projects, …) is the v1 corpus and
is still used by v1 tests and the e2e suite; leave it untouched until v1 is
deleted at parity.
