import { useMemo, useRef } from "react";
import { useHighlight } from "./highlight";

// Line diff for file changes: an LCS over the old/new excerpts, rendered
// as classic removed/added rows with line numbers. Payloads are already
// capped server-side (16 KB each), so the quadratic DP stays cheap.
const MAX_LINES = 400;
// Pure additions/deletions (file writes) skip the LCS entirely, so they
// can render much longer before truncating.
const MAX_PURE_LINES = 1200;

type Op = {
  kind: " " | "-" | "+";
  text: string;
  oldNo?: number;
  newNo?: number;
};

// pureOps renders a one-sided change (whole-file write or wipe) without
// the quadratic DP, truncating with a marker instead of giving up.
function pureOps(lines: string[], kind: "-" | "+"): Op[] {
  const shown = lines.slice(0, MAX_PURE_LINES);
  const ops: Op[] = shown.map((text, i) => ({
    kind,
    text,
    ...(kind === "+" ? { newNo: i + 1 } : { oldNo: i + 1 }),
  }));
  if (lines.length > MAX_PURE_LINES) {
    ops.push({
      kind: " ",
      text: `… ${lines.length - MAX_PURE_LINES} more lines`,
    });
  }
  return ops;
}

function diffLines(oldText: string, newText: string): Op[] | null {
  if (oldText === "") return pureOps(newText.split("\n"), "+");
  if (newText === "") return pureOps(oldText.split("\n"), "-");
  const a = oldText.split("\n");
  const b = newText.split("\n");
  if (a.length > MAX_LINES || b.length > MAX_LINES) return null;

  // LCS table.
  const dp: number[][] = Array.from({ length: a.length + 1 }, () =>
    Array.from({ length: b.length + 1 }, () => 0),
  );
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      dp[i][j] =
        a[i] === b[j]
          ? dp[i + 1][j + 1] + 1
          : Math.max(dp[i + 1][j], dp[i][j + 1]);
    }
  }
  const ops: Op[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      ops.push({ kind: " ", text: a[i], oldNo: i + 1, newNo: j + 1 });
      i++;
      j++;
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      ops.push({ kind: "-", text: a[i], oldNo: i + 1 });
      i++;
    } else {
      ops.push({ kind: "+", text: b[j], newNo: j + 1 });
      j++;
    }
  }
  for (; i < a.length; i++) ops.push({ kind: "-", text: a[i], oldNo: i + 1 });
  for (; j < b.length; j++) ops.push({ kind: "+", text: b[j], newNo: j + 1 });
  return ops;
}

function Gutter({ n }: { n?: number }) {
  return (
    <span className="inline-block w-9 shrink-0 pr-2 text-right text-ink-faint select-none">
      {n ?? ""}
    </span>
  );
}

// NewFileView renders a whole-file write as a FILE, not as a diff. A
// write has no "before", so every line qualified as an addition and the
// old renderer painted several hundred lines of solid green — a wash
// that hid the code inside it while implying a comparison that never
// happened. Here the content is syntax-highlighted (like every other code
// block in the app), numbered, and labelled for what it is.
function NewFileView({ text }: { text: string }) {
  const box = useRef<HTMLDivElement>(null);
  useHighlight(box, [text]);
  const lines = text.split("\n");
  const shown = lines.slice(0, MAX_PURE_LINES);
  return (
    <div ref={box} className="overflow-hidden rounded-md border border-edge">
      <div className="flex items-center gap-2 border-b border-edge bg-surface-2 px-3 py-1 font-mono text-micro text-ink-faint">
        <span className="text-ok">new file</span>
        <span className="tabular-nums">
          {lines.length.toLocaleString()} lines
        </span>
      </div>
      <div className="flex max-h-96 overflow-auto bg-surface">
        <pre
          aria-hidden
          className="shrink-0 px-2 py-2 text-right font-mono text-meta leading-relaxed text-ink-faint tabular-nums select-none"
        >
          {shown.map((_, i) => `${i + 1}\n`).join("")}
        </pre>
        <pre className="min-w-0 flex-1 py-2 pr-3 font-mono text-meta leading-relaxed">
          <code className="block whitespace-pre">{shown.join("\n")}</code>
        </pre>
      </div>
      {lines.length > MAX_PURE_LINES && (
        <div className="border-t border-edge px-3 py-1 font-mono text-micro text-ink-faint">
          … {(lines.length - MAX_PURE_LINES).toLocaleString()} more lines
        </div>
      )}
    </div>
  );
}

export function DiffView({ old, new: neu }: { old: string; new: string }) {
  // A write with no prior content is a new file, not a diff.
  if (old === "" && neu !== "") return <NewFileView text={neu} />;

  // diffLines is an O(n²) LCS over up to MAX_LINES × MAX_LINES cells. The
  // parent is the transcript, which re-renders on every virtualizer range
  // change — i.e. continuously while scrolling — so an unmemoized call
  // rebuilt the whole DP table on every frame an expanded diff was open.
  const ops = useMemo(() => diffLines(old, neu), [old, neu]);
  if (!ops)
    return (
      <p className="px-3 py-2 font-mono text-meta text-ink-faint">
        diff too large to render
      </p>
    );
  const added = ops.filter((o) => o.kind === "+").length;
  const removed = ops.filter((o) => o.kind === "-").length;
  return (
    <div className="overflow-hidden rounded-md border border-edge">
      <div className="flex items-center gap-3 border-b border-edge bg-surface-2 px-3 py-1 font-mono text-micro tabular-nums">
        <span className="text-ok">+{added}</span>
        <span className="text-warn">−{removed}</span>
      </div>
      <pre className="max-h-96 overflow-auto bg-surface font-mono text-meta leading-relaxed">
        {ops.map((op, idx) => (
          <div
            key={idx}
            className={
              op.kind === "-"
                ? "bg-[color-mix(in_oklab,var(--color-agent-cursor)_18%,transparent)] text-[color-mix(in_oklab,var(--color-agent-cursor)_70%,var(--color-ink))]"
                : op.kind === "+"
                  ? "bg-[color-mix(in_oklab,var(--color-ok)_14%,transparent)] text-[color-mix(in_oklab,var(--color-ok)_70%,var(--color-ink))]"
                  : "text-ink-dim"
            }
          >
            {/* Line numbers for both sides: a diff without them cannot be
                carried back to the file it came from. */}
            <Gutter n={op.oldNo} />
            <Gutter n={op.newNo} />
            <span className="inline-block w-4 shrink-0 select-none">
              {op.kind}
            </span>
            {op.text || " "}
          </div>
        ))}
      </pre>
    </div>
  );
}
