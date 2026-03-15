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

  // Collapse consecutive same-tool calls into segments
  const segments = [];
  for (const d of data) {
    const last = segments[segments.length - 1];
    if (last && last.name === d.name) {
      last.count++;
    } else {
      segments.push({ name: d.name, count: 1 });
    }
  }

  const chartHeight = 28;
  const gap = 1;
  const minSegWidth = 4;
  const totalCalls = data.length;
  // Scale segment widths proportional to call count
  const availableWidth = 800;
  const totalGaps = (segments.length - 1) * gap;
  const usableWidth = availableWidth - totalGaps;

  const segRects = [];
  let x = 0;
  for (const seg of segments) {
    const w = Math.max(minSegWidth, (seg.count / totalCalls) * usableWidth);
    const color = colors[seg.name] || defaultColor;
    const label = `${esc(seg.name)} (${seg.count}x)`;
    segRects.push(
      `<rect x="${x}" y="0" width="${w}" height="${chartHeight}" rx="3" fill="${color}" opacity="0.75">` +
        `<title>${label}</title></rect>`,
    );
    x += w + gap;
  }
  const chartWidth = x - gap;

  // Legend: unique tools ordered by total count
  const toolCounts = {};
  for (const d of data) {
    toolCounts[d.name] = (toolCounts[d.name] || 0) + 1;
  }
  const toolNames = Object.keys(toolCounts).toSorted(
    (a, b) => toolCounts[b] - toolCounts[a],
  );

  const legend = toolNames
    .map((name) => {
      const color = colors[name] || defaultColor;
      return (
        `<span class="inline-flex items-center gap-1 text-xs text-slate-500">` +
        `<span class="inline-block w-2 h-2 rounded-sm" style="background:${color}"></span>` +
        `${esc(name)}</span>`
      );
    })
    .join(" ");

  container.innerHTML =
    `<div class="overflow-x-auto">` +
    `<svg viewBox="0 0 ${chartWidth} ${chartHeight}" class="w-full rounded-lg" preserveAspectRatio="none" style="height: ${chartHeight}px">` +
    segRects.join("") +
    `</svg></div>` +
    `<div class="flex flex-wrap gap-3 mt-2">${legend}</div>`;
}

init();
export { init };
