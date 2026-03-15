// Auto-submit forms when select/input elements with data-autosubmit change
document.querySelectorAll("[data-autosubmit]").forEach((el) => {
  el.addEventListener("change", () => el.form.submit());
});

// Page-jump forms: read total pages and filter query from data attributes
document.querySelectorAll("[data-page-jump]").forEach((form) => {
  const totalPages = Number(form.dataset.totalPages);
  const filterQuery = form.dataset.filterQuery || "";
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    const p = Number(form.querySelector("input").value);
    if (p >= 1 && p <= totalPages) {
      location.search = "?page=" + p + filterQuery;
    }
  });
});
