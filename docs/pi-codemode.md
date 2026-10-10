# Pi codemode scripts and nested calls

## Problem

Pi's `codemode` tool runs model-written JavaScript that calls other tools (`tools.read`, `tools.edit`, `tools.bash`, …). ccpeek stored each codemode call as `kind=other` with an empty `command`, so the transcript chip showed only the word "codemode" and could not expand. Pi records no transcript entries for the tools a script runs. Their only trace is the `nestedCalls` record on the codemode result message, and ccpeek ignored it. Local history had 469 codemode calls hiding 1,886 nested calls: 912 reads, 523 bash runs, 206 tmux calls, 193 edits and 45 writes. None of them reached the Files tab, the Commands browser or the tool counts.

## Change

- `canon.ToolScript` (`"script"`) is a new tool kind for tools that run code which calls other tools. The script source is stored in `ToolCall.Command` (the `command` column), the same place a shell command goes.
- The Pi adapter maps `codemode` to `script` and lifts `arguments.code` into `Command`.
- The Pi adapter reads `message.nestedCalls.calls[]` on tool results and emits each entry as its own tool call. Each nested call:
  - anchors to the **result** message's seq, so its chip renders under the script's output;
  - keeps Pi's id (`<parent call id>/<n>`) as `external_id`, which preserves the link to the parent call;
  - takes `status` from Pi and stores the `error` text as the result excerpt (Pi never records nested results);
  - goes through the same argument normalization as model-issued calls (path, command, old/new text).
- Pi `ParseVersion` is now 4, so every Pi source on disk reparses once. Archived sessions whose source file is gone keep their old rows.
- UI: `script` chips expand to the full source, formatted with Prettier and highlighted as JavaScript. Models write these scripts as dense one-liners, so the stored text is hard to read. Prettier's standalone build loads as a lazy chunk the first time a script opens. A script that does not parse is shown as stored. In the Tools tab, script rows open the source. `script` has its own color token (`--color-tool-script`).

`nestedCalls` is documented by Pi as a generic core record for any tool that calls `ctx.executeTool()` (see Pi's `docs/extensions.md`), so nested calls from other tools are indexed too. Pi drops arguments over 8 KiB per call, so a few nested edits and writes have no path.

## Verification

- `TestParseCodemodeScriptAndNestedCalls` covers the script kind/source and nested edit/bash calls with status, error and anchoring.
- A reindex of a copy of the real store produced exactly the nested-call counts in the source files (1,886 calls, 63 errors) and 469 scripts with source.
- Checked in the browser: script chips expand formatted and highlighted, and nested chips render on the result row.

## Known gaps

- Modern Pi `edit` calls send an `edits: [{oldText, newText}]` array, which the adapter used to skip. This is fixed by "fix(pi): read multi-edit calls" (see `docs/agent-data-features.md`).
