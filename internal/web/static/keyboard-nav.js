// Keyboard navigation: j/k to move between list rows, Enter to open, / to search, Escape to blur.
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

  const rows = Array.from(document.querySelectorAll(".list-row:not([hidden])"));
  if (!rows.length) return;

  if (e.key === "j") {
    e.preventDefault();
    setFocus(rows, Math.min(focusIndex + 1, rows.length - 1));
  } else if (e.key === "k") {
    e.preventDefault();
    setFocus(rows, Math.max(focusIndex - 1, 0));
  } else if (e.key === "Enter" && focusIndex >= 0 && focusIndex < rows.length) {
    e.preventDefault();
    rows[focusIndex].click();
  }
});
