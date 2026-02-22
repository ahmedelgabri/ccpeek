// Copy-to-clipboard: any element with a data-copy attribute copies its value on click.
document.addEventListener("click", (e) => {
  const btn = e.target.closest("[data-copy]");
  if (!btn) return;
  void navigator.clipboard.writeText(btn.getAttribute("data-copy")).then(() => {
    const prev = btn.textContent;
    btn.textContent = "Copied";
    setTimeout(() => {
      btn.textContent = prev;
    }, 1500);
  });
});

export {};
