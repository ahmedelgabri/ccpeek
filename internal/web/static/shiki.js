import { codeToHtml } from "https://esm.sh/shiki@latest";

document
  .querySelectorAll('pre code[class*="language-"]')
  .forEach(async (el) => {
    const lang = el.className.match(/language-(\w+)/)?.[1];
    if (!lang) return;

    try {
      const html = await codeToHtml(el.textContent, {
        lang,
        theme: "vitesse-dark",
      });
      el.parentElement.outerHTML = html;
    } catch {
      // unknown language — leave as plain text
    }
  });
