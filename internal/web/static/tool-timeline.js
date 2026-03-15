const esc = (s) =>
  `${s}`
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");

function init() {
  const container = document.getElementById("tool-timeline");
  if (!container) return;

  const data = JSON.parse(container.dataset.timeline || "[]");
  if (data.length === 0) return;

  const colors = {
    Read: "#06b6d4",
    Edit: "#10b981",
    Write: "#14b8a6",
    Bash: "#f59e0b",
    Agent: "#8b5cf6",
    Glob: "#6366f1",
    Grep: "#818cf8",
    WebFetch: "#ec4899",
    WebSearch: "#f472b6",
    TaskCreate: "#22c55e",
    TaskUpdate: "#ef4444",
    ToolSearch: "#a855f7",
    AskUserQuestion: "#6366f1",
  };
  const defaultColor = "#64748b";

  // Count per tool, sorted by frequency for lane ordering
  const toolCounts = {};
  for (const d of data) {
    toolCounts[d.name] = (toolCounts[d.name] || 0) + 1;
  }
  const toolNames = Object.keys(toolCounts).toSorted(
    (a, b) => toolCounts[b] - toolCounts[a],
  );
  const laneOf = {};
  toolNames.forEach((name, i) => {
    laneOf[name] = i;
  });

  // Layout
  const labelWidth = 130;
  const laneHeight = 16;
  const laneGap = 2;
  const chartHeight = toolNames.length * (laneHeight + laneGap);
  const chartWidth = 1200;
  const plotWidth = chartWidth - labelWidth;

  // Temporal mapping: each call gets an x position based on its index
  // (timestamps may not be evenly spaced, but sequential index gives
  // a uniform spread that shows ordering clearly)
  const barWidth = Math.max(2, Math.min(6, plotWidth / data.length));

  const rects = data
    .map((d, i) => {
      const x = labelWidth + (i / data.length) * (plotWidth - barWidth);
      const y = laneOf[d.name] * (laneHeight + laneGap);
      const color = colors[d.name] || defaultColor;
      const ts = d.timestamp
        ? d.timestamp.replace(/T/, " ").replace(/Z$/, "")
        : "";
      return (
        `<rect x="${x}" y="${y}" width="${barWidth}" height="${laneHeight}" rx="2" ` +
        `fill="${color}" opacity="0.7">` +
        `<title>${esc(d.name)}${ts ? " - " + esc(ts) : ""}</title></rect>`
      );
    })
    .join("");

  // Lane labels on the left
  const labels = toolNames
    .map((name, i) => {
      const y = i * (laneHeight + laneGap) + laneHeight / 2;
      const color = colors[name] || defaultColor;
      return (
        `<text x="${labelWidth - 6}" y="${y}" dy="0.35em" ` +
        `text-anchor="end" fill="${color}" font-size="10" font-family="ui-monospace, monospace">` +
        `${esc(name)} (${toolCounts[name]})</text>`
      );
    })
    .join("");

  // Lane background stripes for readability
  const stripes = toolNames
    .map((_, i) => {
      const y = i * (laneHeight + laneGap);
      return i % 2 === 0
        ? `<rect x="${labelWidth}" y="${y}" width="${plotWidth}" height="${laneHeight}" fill="white" opacity="0.02" rx="2"/>`
        : "";
    })
    .join("");

  container.innerHTML =
    `<div class="overflow-x-auto">` +
    `<svg viewBox="0 0 ${chartWidth} ${chartHeight}" class="w-full">` +
    stripes +
    labels +
    rects +
    `</svg></div>`;
}

init();
export { init };
