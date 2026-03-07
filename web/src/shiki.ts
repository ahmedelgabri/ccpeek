// @ts-expect-error - CDN import, no type declarations available
import { codeToHtml } from "https://esm.sh/shiki@latest";

document
  .querySelectorAll<HTMLElement>('pre code[class*="language-"]')
  .forEach(async (el) => {
    const lang = el.className.match(/language-(\w+)/)?.[1];
    if (!lang) return;

    try {
      const html: string = await codeToHtml(el.textContent || "", {
        lang,
        theme: "vitesse-dark",
      });
      el.parentElement!.outerHTML = html;
    } catch {
      // unknown language — leave as plain text
    }
  });
