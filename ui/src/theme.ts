import { useEffect, useState } from "react";

// Theme preference: the system scheme by default, pinnable to light or
// dark. The choice lives in localStorage and lands on the root element
// as data-theme, which flips color-scheme (styles.css) and with it every
// light-dark() token — index.html applies it inline before hydration so
// a pinned theme never flashes.
export type ThemePref = "system" | "light" | "dark";

const KEY = "ccpeek-theme";

export function getThemePref(): ThemePref {
  const v = localStorage.getItem(KEY);
  return v === "light" || v === "dark" ? v : "system";
}

export function setThemePref(pref: ThemePref) {
  if (pref === "system") {
    localStorage.removeItem(KEY);
    delete document.documentElement.dataset.theme;
  } else {
    localStorage.setItem(KEY, pref);
    document.documentElement.dataset.theme = pref;
  }
  window.dispatchEvent(new Event("ccpeek-themechange"));
}

function resolve(pref: ThemePref): "light" | "dark" {
  if (pref !== "system") return pref;
  return window.matchMedia("(prefers-color-scheme: dark)").matches
    ? "dark"
    : "light";
}

// useResolvedTheme reports the scheme in effect and re-renders on both
// toggle changes and system-preference changes — canvas renderers
// (ECharts) resolve their colors at draw time and need the signal.
export function useResolvedTheme(): "light" | "dark" {
  const [theme, setTheme] = useState(() => resolve(getThemePref()));
  useEffect(() => {
    const update = () => setTheme(resolve(getThemePref()));
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    media.addEventListener("change", update);
    window.addEventListener("ccpeek-themechange", update);
    return () => {
      media.removeEventListener("change", update);
      window.removeEventListener("ccpeek-themechange", update);
    };
  }, []);
  return theme;
}

// cssColor resolves a design token (e.g. "--color-accent") to a concrete
// color for canvas consumers. light-dark() values don't resolve through
// getPropertyValue on the root, so a probe element computes the real
// color under the current scheme.
export function cssColor(token: string): string {
  const probe = document.createElement("span");
  probe.style.display = "none";
  probe.style.color = `var(${token})`;
  document.body.appendChild(probe);
  const color = getComputedStyle(probe).color;
  probe.remove();
  return color;
}
