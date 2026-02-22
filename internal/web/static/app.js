// Client-side search: filters list items by data-search-keys attribute.
document.querySelectorAll(".search-input").forEach((input) => {
  const targetClass = input.getAttribute("data-search-target");
  if (!targetClass) return;

  const container = input.parentElement.querySelector("." + targetClass);
  if (!container) return;

  const countEl = input.parentElement.querySelector(".search-count");
  const items = container.querySelectorAll("[data-search-keys]");

  const update = () => {
    const query = input.value.toLowerCase().trim();
    const terms = query ? query.split(/\s+/) : [];
    let shown = 0;

    items.forEach((item) => {
      const keys = (item.getAttribute("data-search-keys") || "").toLowerCase();
      const match = terms.every((t) => keys.includes(t));
      item.hidden = !match;
      if (match) shown++;
    });

    if (countEl) {
      countEl.textContent =
        terms.length > 0
          ? shown + " of " + items.length + " items"
          : items.length + " items";
    }
  };

  input.addEventListener("input", update);
  update();
});

// Copy-to-clipboard: any element with a data-copy attribute copies its value on click.
document.addEventListener("click", (e) => {
  const btn = e.target.closest("[data-copy]");
  if (!btn) return;
  void navigator.clipboard.writeText(btn.getAttribute("data-copy")).then(() => {
    const prev = btn.textContent;
    btn.textContent = "Copied";
    setTimeout(() => {
      btn.textContent = prev;
    }, 1500);
  });
});

// Session compare: show Compare button when exactly 2 checkboxes are checked.
(() => {
  const btn = document.getElementById("compare-btn");
  const list = document.querySelector("[data-project-dir]");
  if (!btn || !list) return;
  const dir = list.getAttribute("data-project-dir");

  const allChecks = list.querySelectorAll(".compare-check");

  const updateCompare = () => {
    const checked = list.querySelectorAll(".compare-check:checked");
    if (checked.length === 2) {
      const a = checked[0].getAttribute("data-session-id");
      const b = checked[1].getAttribute("data-session-id");
      btn.href = "/projects/" + dir + "/compare?a=" + a + "&b=" + b;
      btn.classList.remove("hidden");
    } else {
      btn.classList.add("hidden");
    }
    allChecks.forEach((cb) => {
      if (!cb.checked) cb.disabled = checked.length >= 2;
    });
  };

  list.addEventListener("change", (e) => {
    if (e.target.classList.contains("compare-check")) updateCompare();
  });
})();

// Tool filter: clicking a tool stat badge filters the call list to that tool.
(() => {
  const filters = document.getElementById("tool-filters");
  const list = document.getElementById("tool-calls");
  if (!filters || !list) return;

  let active = null;
  const rows = list.querySelectorAll("[data-tool-name]");

  filters.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-tool-filter]");
    if (!btn) return;
    const name = btn.getAttribute("data-tool-filter");

    if (active === name) {
      active = null;
      filters
        .querySelectorAll("[data-tool-filter]")
        .forEach((b) => b.classList.remove("border-violet-400"));
      rows.forEach((r) => (r.hidden = false));
    } else {
      active = name;
      filters.querySelectorAll("[data-tool-filter]").forEach((b) => {
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
})();

// Session filters + sort: badge filters (AND logic) combined with text search, sort by date.
(() => {
  const filtersEl = document.getElementById("session-filters");
  const list = document.querySelector(".searchable-list");
  if (!filtersEl || !list) return;

  const searchInput = document.querySelector(".search-input");
  const countEl = document.querySelector(".search-count");
  const sortBtn = document.getElementById("sort-toggle");
  const rows = Array.from(list.querySelectorAll(".list-row"));
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

      const sorted = [...rows].sort((a, b) => {
        const ma = a.getAttribute("data-modified") || "";
        const mb = b.getAttribute("data-modified") || "";
        return next === "desc" ? mb.localeCompare(ma) : ma.localeCompare(mb);
      });
      sorted.forEach((row) => list.appendChild(row));
    });
  }
})();

// Heatmap tooltip: show date and count on hover.
(() => {
  const wrapper = document.getElementById("heatmap-wrapper");
  const container = document.getElementById("heatmap-container");
  const tooltip = document.getElementById("heatmap-tooltip");
  if (!wrapper || !container || !tooltip) return;

  const days = [
    "Sunday",
    "Monday",
    "Tuesday",
    "Wednesday",
    "Thursday",
    "Friday",
    "Saturday",
  ];
  const months = [
    "Jan",
    "Feb",
    "Mar",
    "Apr",
    "May",
    "Jun",
    "Jul",
    "Aug",
    "Sep",
    "Oct",
    "Nov",
    "Dec",
  ];

  const formatDate = (dateStr) => {
    const parts = dateStr.split("-");
    const d = new Date(+parts[0], +parts[1] - 1, +parts[2]);
    return (
      days[d.getDay()] +
      ", " +
      months[d.getMonth()] +
      " " +
      d.getDate() +
      ", " +
      d.getFullYear()
    );
  };

  container.addEventListener(
    "mouseenter",
    (e) => {
      const cell = e.target.closest(".heatmap-cell");
      if (!cell) return;
      const date = cell.getAttribute("data-date");
      const count = parseInt(cell.getAttribute("data-count"), 10);
      const countLabel =
        count === 0
          ? "No sessions"
          : count === 1
            ? "1 session"
            : count + " sessions";
      tooltip.innerHTML =
        "<strong>" + countLabel + "</strong><br>" + formatDate(date);
      tooltip.classList.remove("hidden");
    },
    true,
  );

  container.addEventListener("mousemove", (e) => {
    if (tooltip.classList.contains("hidden")) return;
    const wrapperRect = wrapper.getBoundingClientRect();
    const tipRect = tooltip.getBoundingClientRect();
    let left = e.clientX - wrapperRect.left + 12;
    if (e.clientX + tipRect.width + 16 > window.innerWidth) {
      left = e.clientX - wrapperRect.left - tipRect.width - 12;
    }
    let top = e.clientY - wrapperRect.top - tipRect.height - 8;
    if (top < 0) {
      top = e.clientY - wrapperRect.top + 16;
    }
    tooltip.style.left = left + "px";
    tooltip.style.top = top + "px";
  });

  container.addEventListener(
    "mouseleave",
    (e) => {
      const cell = e.target.closest(".heatmap-cell");
      if (!cell) return;
      tooltip.classList.add("hidden");
    },
    true,
  );
})();

// Keyboard navigation: j/k to move between list rows, Enter to open, / to search, Escape to blur.
(() => {
  let focusIndex = -1;

  const setFocus = (rows, index) => {
    rows.forEach((r) => r.classList.remove("ring-1", "ring-slate-500"));
    if (index >= 0 && index < rows.length) {
      focusIndex = index;
      rows[index].classList.add("ring-1", "ring-slate-500");
      rows[index].scrollIntoView({ block: "nearest" });
    }
  };

  document.addEventListener("keydown", (e) => {
    const tag = document.activeElement?.tagName;
    const inInput = tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT";

    if (e.key === "/" && !inInput) {
      const search = document.querySelector(".search-input");
      if (search) {
        e.preventDefault();
        search.focus();
      }
      return;
    }

    if (e.key === "Escape" && inInput) {
      document.activeElement.blur();
      return;
    }

    if (inInput) return;

    const rows = Array.from(
      document.querySelectorAll(".list-row:not([hidden])"),
    );
    if (!rows.length) return;

    if (e.key === "j") {
      e.preventDefault();
      setFocus(rows, Math.min(focusIndex + 1, rows.length - 1));
    } else if (e.key === "k") {
      e.preventDefault();
      setFocus(rows, Math.max(focusIndex - 1, 0));
    } else if (
      e.key === "Enter" &&
      focusIndex >= 0 &&
      focusIndex < rows.length
    ) {
      e.preventDefault();
      rows[focusIndex].click();
    }
  });
})();
