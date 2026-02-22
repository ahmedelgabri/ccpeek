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
