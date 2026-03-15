// Copy-to-clipboard: elements with data-copy or data-copy-from="next".
document.addEventListener("click", (e) => {
  const btn = e.target.closest("[data-copy], [data-copy-from]");
  if (!btn) return;

  let text;
  if (btn.hasAttribute("data-copy")) {
    text = btn.getAttribute("data-copy");
  } else if (btn.dataset.copyFrom === "next") {
    const source = btn.nextElementSibling;
    text = source ? source.value || source.textContent : "";
  }
  if (text == null) return;

  void navigator.clipboard.writeText(text).then(() => {
    const prev = btn.textContent;
    btn.textContent = "Copied";
    setTimeout(() => {
      btn.textContent = prev;
    }, 1500);
  });
});

export {};
