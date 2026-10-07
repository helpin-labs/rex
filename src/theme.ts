// Appearance: follow the system, or force light or dark. The choice is kept
// in this app's local storage and applied before the first render.
export type Theme = "system" | "light" | "dark";

const key = "rex.theme";

export function loadTheme(): Theme {
  try {
    const value = localStorage.getItem(key);
    return value === "light" || value === "dark" ? value : "system";
  } catch {
    return "system";
  }
}

export function applyTheme(theme: Theme) {
  const root = document.documentElement;
  if (theme === "system") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", theme);
}

export function saveTheme(theme: Theme) {
  applyTheme(theme);
  try { localStorage.setItem(key, theme); } catch { /* storage unavailable: applies for this session */ }
}
