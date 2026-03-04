(() => {
  const container = document.getElementById("token-chart");
  if (container) {
    const data = JSON.parse(container.dataset.timeline || "[]");
    if (data.length > 0) {
      const maxTokens = Math.max(...data.map((d) => d.tokens));
      const barWidth = Math.max(4, Math.floor(600 / data.length) - 2);
      const chartWidth = data.length * (barWidth + 2);
      const chartHeight = 200;
      const padding = 20;

      const bars = data
        .map((d, i) => {
          const h = Math.max(
            1,
            (d.tokens / maxTokens) * (chartHeight - padding),
          );
          const x = i * (barWidth + 2);
          const y = chartHeight - h;
          return `<rect x="${x}" y="${y}" width="${barWidth}" height="${h}" rx="1" fill="rgba(139,92,246,0.5)" data-idx="${i}" class="hover:fill-[rgba(139,92,246,0.8)]" style="cursor:pointer"></rect>`;
        })
        .join("");

      container.innerHTML = `
        <div class="overflow-x-auto relative" style="min-height:${chartHeight + 40}px">
          <svg viewBox="0 0 ${chartWidth} ${chartHeight}" preserveAspectRatio="none" class="w-full" style="min-width: ${Math.min(chartWidth, 400)}px; height: ${chartHeight}px">
            ${bars}
          </svg>
          <div id="token-tooltip" class="absolute hidden pointer-events-none bg-slate-800 border border-slate-600 rounded px-2 py-1 text-xs text-slate-200 whitespace-nowrap z-10"></div>
        </div>
        <div class="flex justify-between text-xs text-slate-600 mt-2">
          <span>${data[0].date}</span>
          <span>${data[data.length - 1].date}</span>
        </div>
      `;

      const wrapper = container.querySelector(".overflow-x-auto");
      const tooltip = document.getElementById("token-tooltip");
      const svg = container.querySelector("svg");

      svg.addEventListener("mousemove", (e) => {
        const rect = svg.getBoundingClientRect();
        const xRatio = e.clientX - rect.left;
        const idx = Math.floor((xRatio / rect.width) * data.length);
        if (idx >= 0 && idx < data.length) {
          const d = data[idx];
          tooltip.textContent = `${d.date}: ~${formatTokens(d.tokens)} tokens`;
          tooltip.classList.remove("hidden");
          const wrapperRect = wrapper.getBoundingClientRect();
          let tx = e.clientX - wrapperRect.left + 8;
          const ty = e.clientY - wrapperRect.top - 30;
          const tipWidth = tooltip.offsetWidth;
          if (tx + tipWidth > wrapperRect.width) {
            tx = e.clientX - wrapperRect.left - tipWidth - 8;
          }
          tooltip.style.left = tx + "px";
          tooltip.style.top = ty + "px";
        }
      });

      svg.addEventListener("mouseleave", () => {
        tooltip.classList.add("hidden");
      });
    }
  }

  function formatTokens(n) {
    if (n >= 1000000) return (n / 1000000).toFixed(1) + "M";
    if (n >= 1000) return (n / 1000).toFixed(1) + "K";
    return String(n);
  }
})();
