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

// Bookmark toggle: POST to /bookmarks/toggle and swap the star icon.
document.addEventListener("click", (e) => {
  const btn = e.target.closest(".bookmark-btn");
  if (!btn) return;
  e.preventDefault();
  e.stopPropagation();
  const key = btn.getAttribute("data-bookmark-key");
  void fetch("/bookmarks/toggle", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ key }),
  })
    .then((r) => r.json())
    .then((data) => {
      btn.textContent = data.bookmarked ? "\u2605" : "\u2606";
      btn.classList.toggle("text-amber-400", data.bookmarked);
      btn.classList.toggle("text-slate-700", !data.bookmarked);
    });
});

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
      const link = rows[focusIndex].querySelector("a") || rows[focusIndex];
      link.click();
    }
  });
})();
