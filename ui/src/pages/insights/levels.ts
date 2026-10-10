// Effort levels are ordinal: they share one hue stepping from light to
// dark, and "not recorded" sits outside the scale in neutral gray.
// Unknown levels sort after the known ones, before "not recorded".
export const LEVELS = ["low", "medium", "high", "xhigh", "max"];

export function levelRank(level: string): number {
  if (level === "") return 99;
  const i = LEVELS.indexOf(level);
  return i < 0 ? LEVELS.length : i;
}

export function levelLabel(level: string): string {
  return level || "not recorded";
}

/** levelWeight is how much of the accent a level's shade carries (0–1);
 *  null for "not recorded", which uses the neutral gray instead. */
export function levelWeight(level: string): number | null {
  if (level === "") return null;
  const i = LEVELS.indexOf(level);
  return i < 0 ? 0.5 : 0.2 + i * 0.2;
}
