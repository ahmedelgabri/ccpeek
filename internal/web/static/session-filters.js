// Session sort toggle (client-side reordering by date).
function init() {
  const filterList = document.querySelector(".searchable-list");
  if (!filterList) return;

  const sortBtn = document.getElementById("sort-toggle");
  if (!sortBtn) return;

  const rows = Array.from(filterList.querySelectorAll(".list-row"));

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

init();
export { init };
