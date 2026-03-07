interface TokenData {
  date: string;
  tokens: number;
}

function formatTokens(n: number): string {
  if (n >= 1000000) return (n / 1000000).toFixed(1) + "M";
  if (n >= 1000) return (n / 1000).toFixed(1) + "K";
  return String(n);
}

const tokenContainer = document.getElementById("token-chart");
if (tokenContainer) {
  const data: TokenData[] = JSON.parse(tokenContainer.dataset.timeline || "[]");
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

    tokenContainer.innerHTML = `
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

    const chartWrapper = tokenContainer.querySelector<HTMLElement>(".overflow-x-auto")!;
    const chartTooltip = document.getElementById("token-tooltip")!;
    const svg = tokenContainer.querySelector("svg")!;

    svg.addEventListener("mousemove", (e) => {
      const rect = svg.getBoundingClientRect();
      const xRatio = e.clientX - rect.left;
      const idx = Math.floor((xRatio / rect.width) * data.length);
      if (idx >= 0 && idx < data.length) {
        const d = data[idx];
        chartTooltip.textContent = `${d.date}: ~${formatTokens(d.tokens)} tokens`;
        chartTooltip.classList.remove("hidden");
        const wrapperRect = chartWrapper.getBoundingClientRect();
        let tx = e.clientX - wrapperRect.left + 8;
        const ty = e.clientY - wrapperRect.top - 30;
        const tipWidth = chartTooltip.offsetWidth;
        if (tx + tipWidth > wrapperRect.width) {
          tx = e.clientX - wrapperRect.left - tipWidth - 8;
        }
        chartTooltip.style.left = tx + "px";
        chartTooltip.style.top = ty + "px";
      }
    });

    svg.addEventListener("mouseleave", () => {
      chartTooltip.classList.add("hidden");
    });
  }
}
