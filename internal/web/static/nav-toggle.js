const toggle = document.getElementById("nav-toggle");
const sidebar = document.getElementById("nav-sidebar");

if (toggle && sidebar) {
  toggle.addEventListener("click", () => {
    sidebar.classList.toggle("-translate-x-full");
  });

  // Close sidebar when clicking a nav link on mobile
  sidebar.querySelectorAll("a").forEach((link) => {
    link.addEventListener("click", () => {
      if (window.innerWidth < 1024) {
        sidebar.classList.add("-translate-x-full");
      }
    });
  });
}
