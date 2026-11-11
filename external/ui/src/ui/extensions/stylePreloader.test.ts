import { afterEach, describe, expect, test, vi } from "vitest";
import {
  ExtensionStylePreloader,
  STYLE_PRELOAD_TIMEOUT_MS,
} from "./stylePreloader";
import type { ExtensionEntry } from "./runtime";

const entry: ExtensionEntry = {
  api: 1,
  id: "demo",
  name: "Demo",
  version: "1",
  sha256: "package",
  entry_sha256: "entry",
  enabled: true,
  ui: {
    entry: "main.js",
    styles: ["style.css"],
    compat_styles: ["compat.css"],
  },
};

afterEach(() => {
  vi.unstubAllGlobals();
  document
    .querySelectorAll("style[data-ext],style[data-ext-compat]")
    .forEach((el) => el.remove());
});

function stubAssets(fail = false) {
  const fetcher = vi.fn(async (url: string) =>
    fail
      ? new Response("missing", { status: 404 })
      : new Response(url.includes("compat.css") ? ".compat{}" : ".ordinary{}"),
  );
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}

describe("extension style preloading", () => {
  test("prepare installs ordinary styles before adoption and compat only on legacy", async () => {
    stubAssets();
    const modern = new ExtensionStylePreloader(() => false);
    await modern.prepare([entry]);
    expect(document.querySelectorAll("style[data-ext='demo']")).toHaveLength(1);
    expect(document.querySelectorAll("style[data-ext-compat]")).toHaveLength(0);
    const adopted = await modern.adopt(entry);
    expect(adopted?.ordinary).toHaveLength(1);
    expect(adopted?.compat).toHaveLength(0);
    expect(await modern.adopt(entry)).toBeNull();
    adopted?.ordinary.forEach((style) => style.remove());

    const legacy = new ExtensionStylePreloader(() => true);
    await legacy.prepare([entry]);
    expect(
      document.querySelectorAll("style[data-ext-compat='demo']"),
    ).toHaveLength(1);
    const legacyAdopted = await legacy.adopt(entry);
    expect(legacyAdopted?.compat).toHaveLength(1);
    legacyAdopted?.ordinary.forEach((style) => style.remove());
    legacyAdopted?.compat.forEach((style) => style.remove());
  });

  test("clear and a changed package list remove unadopted layers", async () => {
    stubAssets();
    const preloader = new ExtensionStylePreloader(() => false);
    await preloader.prepare([entry]);
    expect(document.querySelectorAll("style[data-ext='demo']")).toHaveLength(1);
    await preloader.prepare([]);
    expect(document.querySelectorAll("style[data-ext='demo']")).toHaveLength(0);

    await preloader.prepare([entry]);
    preloader.clear();
    expect(document.querySelectorAll("style[data-ext='demo']")).toHaveLength(0);
  });

  test("an asset failure reaches adoption without blocking prepare", async () => {
    stubAssets(true);
    const preloader = new ExtensionStylePreloader(() => false);
    await expect(preloader.prepare([entry])).resolves.toBeUndefined();
    const adopted = await preloader.adopt(entry);
    expect(String(adopted?.error)).toContain("HTTP 404");
    expect(document.querySelectorAll("style[data-ext]")).toHaveLength(0);
  });

  test("a stalled asset times out instead of holding startup", async () => {
    vi.useFakeTimers();
    try {
      vi.stubGlobal(
        "fetch",
        vi.fn(
          (_url: string, init?: RequestInit) =>
            new Promise<Response>((_resolve, reject) => {
              init?.signal?.addEventListener(
                "abort",
                () => reject(new DOMException("Aborted", "AbortError")),
                { once: true },
              );
            }),
        ),
      );
      const preloader = new ExtensionStylePreloader(() => false);
      const pending = preloader.prepare([entry]);
      await vi.advanceTimersByTimeAsync(STYLE_PRELOAD_TIMEOUT_MS);
      await expect(pending).resolves.toBeUndefined();
      const adopted = await preloader.adopt(entry);
      expect(adopted?.error).toMatchObject({ name: "AbortError" });
    } finally {
      vi.useRealTimers();
    }
  });
});
