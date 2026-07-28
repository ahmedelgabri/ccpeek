// Build-time stand-in for `@pierre/theming/themes` (see ui/vite.config.ts).
// The real entry's loader maps cover every Pierre theme AND all of shiki's
// bundled themes as dynamic imports, so the bundler emits ~60 theme chunks
// nothing can ever fetch — this app registers and requests exactly one
// theme, "ccpeek" (ui/src/PierreDiff.tsx). Only the three names
// @pierre/diffs actually imports are served.
import { normalizeTheme, type ThemeRegistrationAny } from "@shikijs/core";

type ThemeLoader = () => Promise<
  ThemeRegistrationAny | { default: ThemeRegistrationAny }
>;

// The shared highlighter iterates this to pre-register Pierre's own theme
// loaders; an empty list registers nothing.
export const pierreThemes = {
  getThemes(): { name: string; load: ThemeLoader }[] {
    return [];
  },
};

// Fallback lookup for theme names nobody registered — reachable only when
// a theme other than "ccpeek" is requested, which this app never does.
// Returning undefined makes the library throw its own descriptive
// "No valid theme loader registered" error.
export const shikiThemes = {
  getTheme(_name: string): undefined {
    return undefined;
  },
};

// Faithful port of the real createTheme: wrap the loader so the resolved
// theme comes out shiki-normalized (registerCustomTheme relies on this).
export function createTheme({
  name,
  load,
}: {
  name: string;
  load: ThemeLoader;
}) {
  return {
    name,
    load: async () => {
      const theme = await load();
      return normalizeTheme("default" in theme ? theme.default : theme);
    },
  };
}
