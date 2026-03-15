// Session badge filters: toggle visibility of rows by data attributes.
function init() {
  const filtersEl = document.getElementById("session-filters");
  const filterList = document.querySelector(".searchable-list");
  if (!filtersEl || !filterList) return;

  const searchInput = document.querySelector(".search-input");
  const countEl = document.querySelector(".search-count");
  const rows = Array.from(filterList.querySelectorAll(".list-row"));
  const activeFilters = new Set();

  const passesSearch = (row) => {
    if (!searchInput) return true;
    const query = searchInput.value.toLowerCase().trim();
    if (!query) return true;
    const terms = query.split(/\s+/);
    const keys = (row.getAttribute("data-search-keys") || "").toLowerCase();
    return terms.every((t) => keys.includes(t));
  };

  const passesFilter = (row) => {
    for (const f of activeFilters) {
      if (f === "todo" && !row.getAttribute("data-has-todo")) return false;
      if (f === "commands" && !row.getAttribute("data-has-commands"))
        return false;
    }
    return true;
  };

  const applyFilters = () => {
    let shown = 0;
    rows.forEach((row) => {
      const visible = passesSearch(row) && passesFilter(row);
      row.hidden = !visible;
      if (visible) shown++;
    });

    if (countEl) {
      const query = searchInput ? searchInput.value.trim() : "";
      const hasActive = query || activeFilters.size > 0;
      countEl.textContent = hasActive
        ? shown + " of " + rows.length + " items"
        : rows.length + " items";
    }

    // Uncheck hidden compare checkboxes so they don't count toward the limit
    rows.forEach((row) => {
      if (row.hidden) {
        const cb = row.querySelector(".compare-check");
        if (cb && cb.checked) {
          cb.checked = false;
          cb.dispatchEvent(new Event("change", { bubbles: true }));
        }
      }
    });
  };

  filtersEl.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-filter]");
    if (!btn) return;
    const name = btn.getAttribute("data-filter");

    if (activeFilters.has(name)) {
      activeFilters.delete(name);
      btn.classList.remove("border-violet-400");
    } else {
      activeFilters.add(name);
      btn.classList.add("border-violet-400");
    }
    applyFilters();
  });

  if (searchInput) {
    searchInput.addEventListener("input", applyFilters);
  }
}

init();
export { init };
