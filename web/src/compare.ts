// Session compare: show Compare button when exactly 2 checkboxes are checked.
const compareBtn = document.getElementById("compare-btn") as HTMLAnchorElement | null;
const sessionList = document.querySelector<HTMLElement>("[data-project-dir]");
if (compareBtn && sessionList) {
  const dir = sessionList.getAttribute("data-project-dir");

  const allChecks = sessionList.querySelectorAll<HTMLInputElement>(".compare-check");

  const updateCompare = () => {
    const checked = sessionList.querySelectorAll<HTMLInputElement>(".compare-check:checked");
    if (checked.length === 2) {
      const a = checked[0].getAttribute("data-session-id");
      const b = checked[1].getAttribute("data-session-id");
      compareBtn.href = "/projects/" + dir + "/compare?a=" + a + "&b=" + b;
      compareBtn.classList.remove("hidden");
    } else {
      compareBtn.classList.add("hidden");
    }
    allChecks.forEach((cb) => {
      if (!cb.checked) cb.disabled = checked.length >= 2;
    });
  };

  sessionList.addEventListener("change", (e) => {
    if ((e.target as HTMLElement).classList.contains("compare-check")) updateCompare();
  });
}
