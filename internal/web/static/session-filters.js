// Session filters + sort: badge filters (AND logic) combined with text search, sort by date.
const filtersEl = document.getElementById("session-filters");
const filterList = document.querySelector(".searchable-list");
if (filtersEl && filterList) {
  const searchInput = document.querySelector(".search-input");
  const countEl = document.querySelector(".search-count");
  const sortBtn = document.getElementById("sort-toggle");
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
      if (f === "files" && !row.getAttribute("data-has-files")) return false;
      if (f === "commands" && !row.getAttribute("data-has-commands"))
        return false;
      if (
        f === "tokens" &&
        parseInt(row.getAttribute("data-tokens") || "0", 10) < 10000
      )
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

  // Re-apply filters when search text changes
  if (searchInput) {
    searchInput.addEventListener("input", applyFilters);
  }

  if (sortBtn) {
    sortBtn.addEventListener("click", () => {
      const current = sortBtn.getAttribute("data-sort");
      const next = current === "desc" ? "asc" : "desc";
      sortBtn.setAttribute("data-sort", next);
      sortBtn.querySelector("span").textContent =
        next === "desc" ? "Newest" : "Oldest";
      sortBtn.querySelector("svg").style.transform =
        next === "asc" ? "rotate(180deg)" : "";

      const sorted = [...rows].toSorted((a, b) => {
        const ma = a.getAttribute("data-modified") || "";
        const mb = b.getAttribute("data-modified") || "";
        return next === "desc" ? mb.localeCompare(ma) : ma.localeCompare(mb);
      });
      sorted.forEach((row) => filterList.appendChild(row));
    });
  }
}

export {};
