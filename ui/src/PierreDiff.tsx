import { useMemo, type CSSProperties } from "react";
import { File, FileDiff } from "@pierre/diffs/react";
import {
  parseDiffFromFile,
  registerCustomTheme,
  type FileDiffOptions,
  type FileOptions,
  type ThemeRegistration,
} from "@pierre/diffs";
import { ccpeekTheme, langForPath } from "./highlight";

// @pierre/diffs keeps its own shiki highlighter (full-bundle shiki, same
// @shikijs/core runtime the app's highlighter dedupes onto), so the app
// theme is registered with the library once, at module load. The colors
// map feeds the library's git-status variables; everything else — token
// foregrounds included — is the same light-dark() CSS-variable pass-through
// trick highlight.ts documents: custom properties inherit into the
// component's shadow root, so one theme serves both modes.
registerCustomTheme("ccpeek", () =>
  Promise.resolve({
    ...ccpeekTheme,
    colors: {
      "gitDecoration.addedResourceForeground": "var(--color-ok)",
      "gitDecoration.deletedResourceForeground": "var(--color-agent-cursor)",
      "gitDecoration.modifiedResourceForeground": "var(--color-accent)",
    },
  } as ThemeRegistration),
);

// The dual-theme OBJECT form is load-bearing: with a single theme name the
// library reads the theme's light/dark type and pins `color-scheme` on the
// shadow host to it, which would freeze every light-dark() variable to one
// mode. With {light, dark} it leaves the scheme at `system`, and the app's
// color-scheme drives both the chrome and the tokens.
const themeConfig = { light: "ccpeek", dark: "ccpeek" } as const;

const diffOptions: FileDiffOptions<undefined> = {
  theme: themeConfig,
  themeType: "system",
  diffStyle: "unified",
  // Word-level inline highlighting of the changed spans within a line.
  lineDiffType: "word",
  diffIndicators: "classic",
  // The surrounding chip/row already names the file; the library's own
  // header would repeat it at full size.
  disableFileHeader: true,
};

const fileOptions: FileOptions<undefined> = {
  theme: themeConfig,
  themeType: "system",
  disableFileHeader: true,
};

// Matches the app's compact code register (font-mono text-meta) instead of
// the library's 13px default. Custom properties set on the host element
// inherit into the shadow root.
const hostStyle = {
  "--diffs-font-family": "var(--font-mono)",
  "--diffs-font-size": "var(--text-meta)",
  "--diffs-line-height": "1.2",
} as CSSProperties;

// NewFileView renders a whole-file write as a FILE, not as a diff. A
// write has no "before", so every line would qualify as an addition and a
// diff view would paint several hundred lines of solid green — a wash
// that hides the code inside it while implying a comparison that never
// happened. Here the content is syntax-highlighted, numbered, and
// labelled for what it is.
function NewFileView({ text, path }: { text: string; path?: string }) {
  const lineCount = text.split("\n").length;
  const lang = fileLang(path);
  return (
    <div className="overflow-hidden rounded-md border border-edge">
      <div className="flex items-center gap-2 border-b border-edge bg-surface-2 px-3 py-1 font-mono text-micro text-ink-faint">
        <span className="text-ok">new file</span>
        <span className="tabular-nums">{lineCount.toLocaleString()} lines</span>
      </div>
      <div className="max-h-96 overflow-auto bg-surface">
        <File
          file={{ name: path ?? "", contents: text, lang }}
          options={fileOptions}
          style={hostStyle}
        />
      </div>
    </div>
  );
}

// The language is ALWAYS pinned explicitly — a curated grammar id or
// "text" — so the library's filename inference never runs. Inference
// resolves against shiki's full bundled-language map, which the build
// stubs down to the curated set (ui/vite.config.ts); an id outside that
// set would make the render error instead of degrading to plain text.
function fileLang(path: string | undefined) {
  return (path && langForPath(path)) || "text";
}

// The old/new payloads are excerpts capped server-side, so a missing
// trailing newline says nothing about the underlying file — without this,
// the parser would stamp "No newline at end of file" marker rows onto
// nearly every diff.
function withFinalNewline(s: string) {
  return s === "" || s.endsWith("\n") ? s : s + "\n";
}

export default function PierreDiff({
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

  // Parsing stays ABOVE the new-file return: `old` flips from "" to text
  // when a watch-mode refetch fills in a diff that is already expanded,
  // and an early return over a hook would change the hook count mid-mount.
  const fileDiff = useMemo(() => {
    if (isNew) return null;
    const lang = fileLang(path);
    return parseDiffFromFile(
      { name: path ?? "", contents: withFinalNewline(old), lang },
      // The new side's lang is the one parseDiffFromFile carries onto the
      // resulting FileDiffMetadata.
      { name: path ?? "", contents: withFinalNewline(neu), lang },
    );
  }, [isNew, old, neu, path]);
  if (isNew || !fileDiff) return <NewFileView text={neu} path={path} />;
  let added = 0;
  let removed = 0;
  for (const h of fileDiff.hunks) {
    added += h.additionLines;
    removed += h.deletionLines;
  }
  return (
    <div className="overflow-hidden rounded-md border border-edge">
      {/* Line counts survive from the old renderer: a glanceable size
          before reading the body. */}
      <div className="flex items-center gap-3 border-b border-edge bg-surface-2 px-3 py-1 font-mono text-micro tabular-nums">
        <span className="text-ok">+{added}</span>
        <span className="text-warn">−{removed}</span>
      </div>
      <div className="max-h-96 overflow-auto bg-surface">
        <FileDiff fileDiff={fileDiff} options={diffOptions} style={hostStyle} />
      </div>
    </div>
  );
}
