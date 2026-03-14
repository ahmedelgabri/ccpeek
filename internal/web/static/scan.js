// Show-ignored toggle on the scan page.
const cb = document.querySelector("[data-scan-show-ignored]");
if (cb) {
  cb.addEventListener("change", () => {
    const u = new URL(location.href);
    if (cb.checked) {
      u.searchParams.set("show_ignored", "1");
    } else {
      u.searchParams.delete("show_ignored");
    }
    location.href = u.href;
  });
}

export {};
