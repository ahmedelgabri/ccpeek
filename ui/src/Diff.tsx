import { useMemo } from "react";
import { parseDiffFromFile, registerCustomTheme } from "@pierre/diffs";
import { File, FileDiff } from "@pierre/diffs/react";
import { theme } from "./highlight";
import { useThemePref } from "./theme";

// File changes render through @pierre/diffs with the same highlights theme
// as every other code block. Payloads are capped server-side (16 KB each),
// so whole excerpts render without truncation.
registerCustomTheme(theme.name, () => Promise.resolve(theme), "zed");

// The diff draws inside its own shadow root, whose color-scheme defaults
// to the system's — themeType pins it to the app's toggle so the palette's
// light-dark() tokens resolve the same inside the diff as around it.
function useOptions() {
  const [themeType] = useThemePref();
  return useMemo(
    () => ({
      theme: theme.name,
      themeType,
      preferredHighlighter: "highlights" as const,
      diffStyle: "unified" as const,
      // The surrounding chrome carries the counts; the tool chip above
      // already names the file.
      disableFileHeader: true,
    }),
    [themeType],
  );
}

// NewFileView renders a whole-file write as a FILE, not as a diff. A
// write has no "before", so every line qualified as an addition and a
// diff painted several hundred lines of solid green — a wash that hid the
// code inside it while implying a comparison that never happened.
function NewFileView({ text, path }: { text: string; path?: string }) {
  const options = useOptions();
  const lines = text.split("\n").length;
  return (
    <div className="overflow-hidden rounded-md border border-edge">
      <div className="flex items-center gap-2 border-b border-edge bg-surface-2 px-3 py-1 font-mono text-micro text-ink-faint">
        <span className="text-ok">new file</span>
        <span className="tabular-nums">{lines.toLocaleString()} lines</span>
      </div>
      <div className="max-h-96 overflow-auto">
        <File file={{ name: path ?? "", contents: text }} options={options} />
      </div>
    </div>
  );
}

// Edit excerpts are snippets, not files: nearly all of them end without a
// newline, and diffs would mark "No newline at end of file" under each.
function asFile(text: string) {
  return text === "" || text.endsWith("\n") ? text : `${text}\n`;
}

export function DiffView({
  old,
  new: neu,
  path,
}: {
  old: string;
  new: string;
  path?: string;
}) {
  // A write with no prior content is a new file, not a diff.
  const isNew = old === "" && neu !== "";
  const options = useOptions();

  // The parent is the transcript, which re-renders on every virtualizer
  // range change — continuously while scrolling — so the parse is
  // memoized rather than redone on every frame an expanded diff is open.
  //
  // It stays ABOVE the new-file return: `old` flips from "" to text when a
  // watch-mode refetch fills in a diff that is already expanded, and an
  // early return over this hook changed the hook count mid-mount — React
  // threw, and the route's ErrorBoundary took the whole session page with
  // it.
  const fileDiff = useMemo(
    () =>
      isNew
        ? null
        : parseDiffFromFile(
            { name: path ?? "", contents: asFile(old) },
            { name: path ?? "", contents: asFile(neu) },
          ),
    [isNew, old, neu, path],
  );
  if (isNew) return <NewFileView text={neu} path={path} />;
  if (!fileDiff) return null;
  let added = 0;
  let removed = 0;
  for (const h of fileDiff.hunks) {
    added += h.additionLines;
    removed += h.deletionLines;
  }
  return (
    <div className="overflow-hidden rounded-md border border-edge">
      <div className="flex items-center gap-3 border-b border-edge bg-surface-2 px-3 py-1 font-mono text-micro tabular-nums">
        <span className="text-ok">+{added}</span>
        <span className="text-warn">−{removed}</span>
      </div>
      <div className="max-h-96 overflow-auto">
        <FileDiff fileDiff={fileDiff} options={options} />
      </div>
    </div>
  );
}
