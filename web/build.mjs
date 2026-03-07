import * as esbuild from "esbuild";
import { execSync } from "node:child_process";
import { cpSync, mkdirSync } from "node:fs";

const outdir = "../internal/web/dist";

mkdirSync(outdir, { recursive: true });

// 1. Build TypeScript
await esbuild.build({
  entryPoints: ["src/app.ts", "src/shiki.ts"],
  bundle: true,
  outdir,
  format: "esm",
  minify: true,
  external: ["https://*"],
  target: "es2022",
});

// 2. Build Tailwind CSS
execSync(
  `pnpm exec tailwindcss --input src/app.css --output ${outdir}/style.css --minify`,
  { stdio: "inherit" },
);

// 3. Copy static assets
cpSync("static/favicon.svg", `${outdir}/favicon.svg`);
