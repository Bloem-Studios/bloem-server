// @vitest-environment node

import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { runInNewContext } from "node:vm";
import { brotliDecompressSync, gunzipSync } from "node:zlib";
import { build, createServer, type ViteDevServer } from "vite";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { bloemBrand } from "./bloem-brand-plugin";
import { precompressStaticAssets } from "./vite.config";

const WEB_ROOT = path.dirname(new URL(import.meta.url).pathname);
const PUBLIC_FILES = ["site.webmanifest", "sw.js"] as const;
const COMPONENT = `export const wordmark = "/silo-wordmark-sidebar.png";
export const mark = '/silo-icon-1024.png';
export const custom = "https://cdn.example/silo-icon-1024.png";
export const customQuery = "/silo-wordmark-sidebar.png?custom=true";
export const header = "X-Silo-Client-Version";
export const copy = "Welcome to Silo";
console.log(wordmark, mark, custom, customQuery, header, copy);`;

/** Execute the served worker at its browser boundary rather than checking script text. */
function loadWorker(source: string) {
  const handlers = new Map<string, (event: Record<string, unknown>) => void>();
  const notifications: { title: string; options: Record<string, unknown> }[] = [];
  const navigated: string[] = [];
  const opened: string[] = [];
  let focused = 0;
  let closed = 0;
  let installed = 0;
  let claimed = 0;
  let hasWindow = true;
  const origin = "https://media.example";
  runInNewContext(source, {
    URL,
    self: {
      location: { origin },
      addEventListener(name: string, handler: (event: Record<string, unknown>) => void) {
        handlers.set(name, handler);
      },
      skipWaiting() {
        installed++;
      },
      registration: {
        showNotification(title: string, options: Record<string, unknown>) {
          notifications.push({ title, options });
          return Promise.resolve();
        },
      },
      clients: {
        claim() {
          claimed++;
          return Promise.resolve();
        },
        matchAll() {
          return Promise.resolve(
            hasWindow
              ? [
                  {
                    navigate(url: string) {
                      navigated.push(url);
                    },
                    focus() {
                      focused++;
                    },
                  },
                ]
              : [],
          );
        },
        openWindow(url: string) {
          opened.push(url);
          return Promise.resolve();
        },
      },
    },
  });
  async function dispatch(name: string, fields: Record<string, unknown> = {}) {
    let pending: Promise<unknown> | undefined;
    const handler = handlers.get(name);
    if (!handler) throw new Error("Missing worker event: " + name);
    handler({
      ...fields,
      waitUntil(promise: Promise<unknown>) {
        pending = promise;
      },
    });
    await pending;
  }
  return {
    notifications,
    navigated,
    opened,
    dispatch,
    click(url: string) {
      return dispatch("notificationclick", {
        notification: {
          data: { url },
          close() {
            closed++;
          },
        },
      });
    },
    noWindow() {
      hasWindow = false;
    },
    state() {
      return { focused, closed, installed, claimed };
    },
  };
}

async function expectPublicBehavior(manifest: string, source: string) {
  expect(JSON.parse(manifest)).toMatchObject({ name: "Bloem", short_name: "Bloem" });
  const worker = loadWorker(source);
  await worker.dispatch("install");
  await worker.dispatch("activate");
  expect(worker.state()).toMatchObject({ installed: 1, claimed: 1 });
  await worker.dispatch("push");
  expect(worker.notifications[0]).toMatchObject({
    title: "Bloem",
    options: {
      body: "",
      icon: "/web-app-icon-192.png",
      badge: "/web-app-icon-192.png",
      data: { url: "/notifications" },
    },
  });
  await worker.dispatch("push", {
    data: {
      json() {
        throw new Error("Malformed payload");
      },
    },
  });
  expect(worker.notifications[1]?.title).toBe("Bloem");
  await worker.dispatch("push", {
    data: {
      json: () => ({
        title: "Custom " + "Si" + "lo",
        body: "Ready",
        icon: "/custom.png",
        tag: "release",
        url: "/notifications?filter=unread#latest",
      }),
    },
  });
  expect(worker.notifications[2]).toMatchObject({
    title: "Custom " + "Si" + "lo",
    options: {
      body: "Ready",
      icon: "/custom.png",
      badge: "/web-app-icon-192.png",
      tag: "release",
      data: { url: "/notifications?filter=unread#latest" },
    },
  });
  await worker.click("/notifications?filter=unread#latest");
  await worker.click("https://elsewhere.example/external");
  expect(worker.navigated).toEqual(["/notifications?filter=unread#latest", "/notifications"]);
  expect(worker.state()).toMatchObject({ focused: 2, closed: 2 });
  worker.noWindow();
  await worker.click("/library");
  expect(worker.opened).toEqual(["/library"]);
}

describe("Bloem brand adapter through Vite", () => {
  let root: string;
  let server: ViteDevServer | undefined;
  let sources: Record<string, Buffer>;

  beforeEach(async () => {
    root = await mkdtemp(path.join(tmpdir(), "bloem-brand-"));
    await mkdir(path.join(root, "public"), { recursive: true });
    await mkdir(path.join(root, "src/components"), { recursive: true });
    sources = Object.fromEntries(
      await Promise.all(
        PUBLIC_FILES.map(async (file) => [
          file,
          await readFile(path.join(WEB_ROOT, "public", file)),
        ]),
      ),
    );
    await Promise.all([
      ...PUBLIC_FILES.map((file) => writeFile(path.join(root, "public", file), sources[file]!)),
      writeFile(path.join(root, "public", "custom.js"), 'self.customName = "Silo";'),
      writeFile(path.join(root, "src/components/SiloBrand.tsx"), COMPONENT),
      writeFile(
        path.join(root, "src/custom-brand.ts"),
        'export const custom = "/silo-icon-1024.png";',
      ),
      writeFile(
        path.join(root, "index.html"),
        '<script type="module" src="/src/components/SiloBrand.tsx"></script>',
      ),
    ]);
  });

  afterEach(async () => {
    await server?.close();
    server = undefined;
    await rm(root, { recursive: true, force: true });
  });

  async function startDev(base = "/") {
    server = await createServer({
      root,
      base,
      configFile: false,
      plugins: [bloemBrand()],
      logLevel: "silent",
      server: { host: "127.0.0.1", port: 0, watch: null },
    });
    await server.listen();
    const address = server.httpServer!.address();
    if (!address || typeof address === "string") throw new Error("Missing dev server address");
    return "http://127.0.0.1:" + address.port + base;
  }

  async function expectSourceUnchanged() {
    for (const file of PUBLIC_FILES) {
      expect(await readFile(path.join(root, "public", file)), file).toEqual(sources[file]);
      expect(await readFile(path.join(WEB_ROOT, "public", file)), file).toEqual(sources[file]);
    }
  }

  it("maps built-in component literals in dev and preserves custom URLs and compatibility names", async () => {
    const url = await startDev();
    const code = await (await fetch(url + "src/components/SiloBrand.tsx")).text();
    expect(code).toContain("/bloem-wordmark-sidebar.png");
    expect(code).toContain("/bloem-icon-1024.png");
    expect(code).toContain("https://cdn.example/silo-icon-1024.png");
    expect(code).toContain("/silo-wordmark-sidebar.png?custom=true");
    expect(code).toContain("X-Silo-Client-Version");
    expect(code).toContain("Welcome to Bloem");
    const custom = await (await fetch(url + "src/custom-brand.ts")).text();
    expect(custom).toContain("/silo-icon-1024.png");
  });

  it.each(["/", "/media/"])(
    "serves branded public files under %s without changing source",
    async (base) => {
      const url = await startDev(base);
      const manifestResponse = await fetch(url + "site.webmanifest?v=1");
      const workerResponse = await fetch(url + "sw.js?v=1");
      expect(manifestResponse.headers.get("content-type")).toContain("application/manifest+json");
      expect(workerResponse.headers.get("content-type")).toContain("javascript");
      await expectPublicBehavior(await manifestResponse.text(), await workerResponse.text());
      expect(await (await fetch(url + "custom.js")).text()).toBe('self.customName = "Silo";');
      await expectSourceUnchanged();
    },
  );

  it("preserves custom manifest names", async () => {
    const manifest = JSON.parse(sources["site.webmanifest"]!.toString()) as Record<string, unknown>;
    manifest.name = "Custom " + "Si" + "lo";
    manifest.short_name = "My Media";
    await writeFile(path.join(root, "public/site.webmanifest"), JSON.stringify(manifest));
    const url = await startDev();
    expect(await (await fetch(url + "site.webmanifest")).json()).toMatchObject({
      name: "Custom " + "Si" + "lo",
      short_name: "My Media",
    });
  });

  it("builds branded public files before compression and leaves source public bytes intact", async () => {
    await build({
      root,
      configFile: false,
      plugins: [bloemBrand(), precompressStaticAssets()],
      logLevel: "silent",
      build: { minify: false, modulePreload: false },
    });
    const assets = path.join(root, "dist/assets");
    const entry = (await readdir(assets)).find((file) => file.endsWith(".js"));
    if (!entry) throw new Error("Missing built component entry");
    const logged: unknown[][] = [];
    runInNewContext(await readFile(path.join(assets, entry), "utf8"), {
      console: { log: (...values: unknown[]) => logged.push(values) },
    });
    expect(logged).toEqual([
      [
        "/bloem-wordmark-sidebar.png",
        "/bloem-icon-1024.png",
        "https://cdn.example/silo-icon-1024.png",
        "/silo-wordmark-sidebar.png?custom=true",
        "X-Silo-Client-Version",
        "Welcome to Bloem",
      ],
    ]);
    const manifest = await readFile(path.join(root, "dist/site.webmanifest"), "utf8");
    const worker = await readFile(path.join(root, "dist/sw.js"), "utf8");
    await expectPublicBehavior(manifest, worker);
    for (const [suffix, decompress] of [
      ["br", brotliDecompressSync],
      ["gz", gunzipSync],
    ] as const) {
      expect(decompress(await readFile(path.join(root, "dist/sw.js." + suffix))).toString()).toBe(
        worker,
      );
    }
    expect(await readFile(path.join(root, "dist/custom.js"), "utf8")).toBe(
      'self.customName = "Silo";',
    );
    await expectSourceUnchanged();
  });
});
