// Effort levels are ordinal: they share one hue stepping from light to
// dark, and "not recorded" sits outside the scale in neutral gray.
// Unknown levels sort after the known ones, before "not recorded".
// Pi's thinking levels start at off and minimal; Codex's at minimal.
const LEVELS = ["off", "minimal", "low", "medium", "high", "xhigh", "max"];

export const NOT_RECORDED = "not recorded";

export function levelRank(level: string): number {
  if (level === "") return 99;
  const i = LEVELS.indexOf(level);
  return i < 0 ? LEVELS.length : i;
}

export function levelLabel(level: string): string {
  return level || NOT_RECORDED;
}

/** levelWeight is how much of the accent a level's shade carries (0.2–1);
 *  null for "not recorded", which uses the neutral gray instead. */
export function levelWeight(level: string): number | null {
  if (level === "") return null;
  const i = LEVELS.indexOf(level);
  return i < 0 ? 0.5 : 0.2 + (i / (LEVELS.length - 1)) * 0.8;
}
