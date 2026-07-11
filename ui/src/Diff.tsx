// Line diff for file changes: an LCS over the old/new excerpts, rendered
// as classic removed/added rows. Payloads are already capped server-side
// (16 KB each), so the quadratic DP stays cheap.
const MAX_LINES = 400;
// Pure additions/deletions (file writes) skip the LCS entirely, so they
// can render much longer before truncating.
const MAX_PURE_LINES = 1200;

type Op = { kind: " " | "-" | "+"; text: string };

// pureOps renders a one-sided change (whole-file write or wipe) without
// the quadratic DP, truncating with a marker instead of giving up.
function pureOps(lines: string[], kind: "-" | "+"): Op[] {
  const shown = lines.slice(0, MAX_PURE_LINES);
  const ops: Op[] = shown.map((text) => ({ kind, text }));
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
    new Array<number>(b.length + 1).fill(0),
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
      ops.push({ kind: " ", text: a[i] });
      i++;
      j++;
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      ops.push({ kind: "-", text: a[i] });
      i++;
    } else {
      ops.push({ kind: "+", text: b[j] });
      j++;
    }
  }
  for (; i < a.length; i++) ops.push({ kind: "-", text: a[i] });
  for (; j < b.length; j++) ops.push({ kind: "+", text: b[j] });
  return ops;
}

export function DiffView({ old, new: neu }: { old: string; new: string }) {
  const ops = diffLines(old, neu);
  if (!ops)
    return (
      <p className="px-3 py-2 font-mono text-[11px] text-ink-faint">
        diff too large to render
      </p>
    );
  return (
    <pre className="max-h-96 overflow-auto rounded-md border border-edge bg-surface font-mono text-[11px] leading-relaxed">
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
          <span className="inline-block w-6 shrink-0 px-1.5 select-none">
            {op.kind}
          </span>
          {op.text || " "}
        </div>
      ))}
    </pre>
  );
}
