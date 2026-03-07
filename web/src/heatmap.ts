// Heatmap tooltip: show date and count on hover.
const wrapper = document.getElementById("heatmap-wrapper");
const heatmapContainer = document.getElementById("heatmap-container");
const heatmapTooltip = document.getElementById("heatmap-tooltip");
if (wrapper && heatmapContainer && heatmapTooltip) {
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

  const formatDate = (dateStr: string) => {
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

  heatmapContainer.addEventListener(
    "mouseenter",
    (e) => {
      const cell = (e.target as HTMLElement).closest(".heatmap-cell");
      if (!cell) return;
      const date = cell.getAttribute("data-date") || "";
      const count = parseInt(cell.getAttribute("data-count") || "0", 10);
      const countLabel =
        count === 0
          ? "No sessions"
          : count === 1
            ? "1 session"
            : count + " sessions";
      heatmapTooltip.innerHTML =
        "<strong>" + countLabel + "</strong><br>" + formatDate(date);
      heatmapTooltip.classList.remove("hidden");
    },
    true,
  );

  heatmapContainer.addEventListener("mousemove", (e) => {
    if (heatmapTooltip.classList.contains("hidden")) return;
    const wrapperRect = wrapper.getBoundingClientRect();
    const tipRect = heatmapTooltip.getBoundingClientRect();
    let left = e.clientX - wrapperRect.left + 12;
    if (e.clientX + tipRect.width + 16 > window.innerWidth) {
      left = e.clientX - wrapperRect.left - tipRect.width - 12;
    }
    let top = e.clientY - wrapperRect.top - tipRect.height - 8;
    if (top < 0) {
      top = e.clientY - wrapperRect.top + 16;
    }
    heatmapTooltip.style.left = left + "px";
    heatmapTooltip.style.top = top + "px";
  });

  heatmapContainer.addEventListener(
    "mouseleave",
    (e) => {
      const cell = (e.target as HTMLElement).closest(".heatmap-cell");
      if (!cell) return;
      heatmapTooltip.classList.add("hidden");
    },
    true,
  );
}
