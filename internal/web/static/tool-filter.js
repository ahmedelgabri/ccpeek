// Tool filter: clicking a tool stat badge filters the call list to that tool.
const toolFilters = document.getElementById("tool-filters");
const toolCalls = document.getElementById("tool-calls");
if (toolFilters && toolCalls) {
  let active = null;
  const rows = toolCalls.querySelectorAll("[data-tool-name]");

  toolFilters.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-tool-filter]");
    if (!btn) return;
    const name = btn.getAttribute("data-tool-filter");

    if (active === name) {
      active = null;
      toolFilters
        .querySelectorAll("[data-tool-filter]")
        .forEach((b) => b.classList.remove("border-violet-400"));
      rows.forEach((r) => (r.hidden = false));
    } else {
      active = name;
      toolFilters.querySelectorAll("[data-tool-filter]").forEach((b) => {
        b.classList.toggle(
          "border-violet-400",
          b.getAttribute("data-tool-filter") === name,
        );
      });
      rows.forEach(
        (r) => (r.hidden = r.getAttribute("data-tool-name") !== name),
      );
    }
  });
}

export {};
