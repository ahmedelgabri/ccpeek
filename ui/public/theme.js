// Apply the saved theme before paint without an inline script exception in CSP.
try {
  const theme = localStorage.getItem("ccpeek-theme");
  if (theme === "light" || theme === "dark")
    document.documentElement.dataset.theme = theme;
} catch {
  // Storage may be unavailable; use the system theme.
}
