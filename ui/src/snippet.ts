// FTS5 wraps matched terms in these delimiters (see query.SnippetOpen).
//
// They are control characters, not brackets. With '[' and ']' the
// delimiters were indistinguishable from brackets in the CONTENT, and the
// corpus is source code and agent transcripts: a hit inside a markdown
// link, a slice expression, a JSON array or a log prefix produced stray
// and unbalanced marks. U+0002/U+0003 cannot occur in indexed text.
//
// Both consumers live here so they cannot drift apart — when the
// delimiters changed, the command palette kept stripping brackets and
// started rendering raw control characters in every result label.
export const MATCH_OPEN = "\u0002";
export const MATCH_CLOSE = "\u0003";

/** markSnippet escapes a snippet and turns its match markers into <mark>. */
export function markSnippet(snippet: string): string {
  const escaped = snippet
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
  return escaped
    .replaceAll(MATCH_OPEN, "<mark>")
    .replaceAll(MATCH_CLOSE, "</mark>");
}

/** stripMarkers removes the delimiters for plain-text contexts. */
export function stripMarkers(snippet: string): string {
  return snippet.replaceAll(MATCH_OPEN, "").replaceAll(MATCH_CLOSE, "");
}
