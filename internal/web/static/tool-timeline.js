(() => {
  const container = document.getElementById("tool-timeline");
  if (container) {
    const data = JSON.parse(container.dataset.timeline || "[]");
    if (data.length > 0) {
      const colors = [
        "#8b5cf6",
        "#06b6d4",
        "#f59e0b",
        "#10b981",
        "#ef4444",
        "#ec4899",
        "#6366f1",
        "#14b8a6",
      ];
      const toolNames = [...new Set(data.map((d) => d.name))];
      const colorMap = Object.fromEntries(
        toolNames.map((name, i) => [name, colors[i % colors.length]]),
      );

      const barWidth = Math.max(3, Math.min(8, 600 / data.length));
      const chartWidth = data.length * (barWidth + 1);
      const chartHeight = 40;
      const laneHeight = 6;
      const lanes = {};
      toolNames.forEach((name, i) => {
        lanes[name] = i;
      });
      const totalHeight = toolNames.length * (laneHeight + 2) + 4;

      const rects = data
        .map((d, i) => {
          const x = i * (barWidth + 1);
          const y = lanes[d.name] * (laneHeight + 2) + 2;
          const color = String(colorMap[d.name]);
          return `<rect x="${x}" y="${y}" width="${barWidth}" height="${laneHeight}" rx="1" fill="${color}" opacity="0.7"><title>${d.name} - ${d.timestamp}</title></rect>`;
        })
        .join("");

      const legend = toolNames
        .map((name) => {
          const color = String(colorMap[name]);
          return `<span class="inline-flex items-center gap-1 text-xs text-slate-500"><span class="inline-block w-2 h-2 rounded-sm" style="background:${color}"></span>${String(name)}</span>`;
        })
        .join(" ");

      container.innerHTML = `
        <div class="overflow-x-auto">
          <svg viewBox="0 0 ${chartWidth} ${totalHeight}" class="w-full" style="min-width: ${Math.min(chartWidth, 300)}px; max-height: ${Math.max(totalHeight, chartHeight)}px">
            ${rects}
          </svg>
        </div>
        <div class="flex flex-wrap gap-3 mt-2">${legend}</div>
      `;
    }
  }
})();
