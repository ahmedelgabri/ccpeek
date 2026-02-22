// Heatmap tooltip: show date and count on hover.
const wrapper = document.getElementById("heatmap-wrapper");
const container = document.getElementById("heatmap-container");
const tooltip = document.getElementById("heatmap-tooltip");
if (wrapper && container && tooltip) {
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
}
