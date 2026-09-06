import { test, expect } from "@playwright/test";
import { resolve } from "node:path";
import {
  createServer,
  loadConfigFromFile,
} from "../../ui/node_modules/vite/dist/node/index.js";

test("Vite proxy preserves the origin of scan mutations", async ({
  request,
  baseURL,
}) => {
  const loaded = await loadConfigFromFile(
    { command: "serve", mode: "test" },
    resolve("ui/vite.config.ts"),
  );
  if (!loaded || !baseURL)
    throw new Error("missing Vite configuration or API URL");
  const config = loaded.config;
  const proxy = config.server?.proxy?.["/api"];
  if (!proxy || !config.server?.proxy) throw new Error("missing API proxy");
  // Override only the destination. Retaining shorthand here would reproduce
  // Vite's implicit changeOrigin:true, rather than hiding it with test options.
  if (typeof proxy === "string") config.server.proxy["/api"] = baseURL;
  else proxy.target = baseURL;
  const vite = await createServer({
    ...config,
    configFile: false,
    root: resolve("ui"),
    server: { ...config.server, host: "127.0.0.1", port: 0 },
  });
  try {
    await vite.listen();
    const address = vite.httpServer?.address();
    if (!address || typeof address === "string")
      throw new Error("missing Vite listener");
    const origin = `http://127.0.0.1:${address.port}`;
    const path = `${origin}/api/v1/scan/9007199254740991/ignore`;
    // A nonexistent finding reaches the real mutation handler and returns
    // 404 only after passing the origin check. No shared fixture is changed.
    const allowed = await request.post(path, {
      headers: { Origin: origin },
      data: { ignored: true },
    });
    expect(allowed.status()).toBe(404);
    expect((await allowed.json()).error).toContain("scan finding");
    const denied = await request.post(path, {
      headers: { Origin: "http://localhost:5173" },
      data: { ignored: true },
    });
    expect(denied.status()).toBe(403);
  } finally {
    await vite.close();
  }
});
