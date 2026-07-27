// Timestamps cross the wire as UTC RFC3339 (Go's time.RFC3339). The UI
// used to render them by slicing the string — `ts.slice(11, 16)` — which
// prints UTC digits in a place the reader takes for local time: a 6pm PDT
// session showed "01:15" and grouped under the NEXT day's heading. These
// helpers parse the instant and render it in the viewer's zone.
//
// The two deliberately-UTC surfaces keep their own formatting and say so:
// the 5h blocks table ("5h window (UTC)") and the activity heatmap, whose
// day math is UTC because the API's activity days are UTC date strings.

function parse(ts: string): Date | null {
  if (!ts) return null;
  const d = new Date(ts);
  // A malformed timestamp renders as nothing — never as "Invalid Date".
  return Number.isNaN(d.getTime()) ? null : d;
}

const pad = (n: number) => String(n).padStart(2, "0");

const day = (d: Date) =>
  `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
const hm = (d: Date) => `${pad(d.getHours())}:${pad(d.getMinutes())}`;
const hms = (d: Date) => `${hm(d)}:${pad(d.getSeconds())}`;

/** localDay is the YYYY-MM-DD the instant falls on in the viewer's zone —
 *  the key the sessions stream groups its day headings by, so a late
 *  evening session sits under the day the user lived it. */
export function localDay(ts: string): string {
  const d = parse(ts);
  return d ? day(d) : "";
}

/** localTime is local HH:MM, for list cells where the day is already
 *  established by a heading. */
export function localTime(ts: string): string {
  const d = parse(ts);
  return d ? hm(d) : "";
}

/** localClock is local HH:MM:SS — the transcript and the tool grids,
 *  where ordering inside a minute is the point. */
export function localClock(ts: string): string {
  const d = parse(ts);
  return d ? hms(d) : "";
}

/** fmtWhen is the compact local stamp: YYYY-MM-DD HH:MM. */
export function fmtWhen(ts: string): string {
  const d = parse(ts);
  return d ? `${day(d)} ${hm(d)}` : "";
}

/** fullWhen is the untruncated local timestamp with its zone named — the
 *  `title` behind every clipped time, so the exact instant stays
 *  recoverable. Undefined rather than "" so an unusable timestamp leaves
 *  no empty tooltip attribute behind. */
export function fullWhen(ts: string): string | undefined {
  const d = parse(ts);
  if (!d) return undefined;
  return d.toLocaleString(undefined, {
    dateStyle: "medium",
    timeStyle: "long",
  });
}
