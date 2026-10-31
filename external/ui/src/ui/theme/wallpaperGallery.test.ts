import { afterEach, describe, expect, test, vi } from "vitest";
import {
  readGallery,
  readGalleryPhoto,
  galleryPhotoUrl,
} from "./wallpaperGallery";
import { WALLPAPER_MAX_BYTES } from "./backdropPrefs";
import { setEnv } from "../env/remoteEnv";
afterEach(() => {
  vi.unstubAllGlobals();
  setEnv({ mode: "local" });
});
describe("server wallpaper gallery", () => {
  test("file names are encoded as one API path segment", () => {
    expect(galleryPhotoUrl({ name: "night #1%.png" })).toBe(
      "/coddy/wallpapers/night%20%231%25.png",
    );
  });
  test("the server listing is read through the active environment", async () => {
    setEnv({
      mode: "remote",
      baseUrl: "https://node.test/prefix",
      token: "secret",
    });
    const fetch = vi.fn(async (_url: string, _init?: RequestInit) =>
      Response.json({ wallpapers: [{ name: "a.png" }] }),
    );
    vi.stubGlobal("fetch", fetch);
    const abort = new AbortController();
    expect(await readGallery(abort.signal)).toEqual([{ name: "a.png" }]);
    const [url, init] = fetch.mock.calls[0]!;
    expect(url).toBe("https://node.test/prefix/coddy/wallpapers");
    expect(new Headers(init?.headers).get("Authorization")).toBe(
      "Bearer secret",
    );
    expect(init?.signal).toBe(abort.signal);
  });
  test.each([
    "image/png",
    "image/jpeg",
    "image/gif",
    "image/webp",
    "image/avif",
  ])("a %s selection is read as bounded image bytes", async (type) => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(new Uint8Array([1, 2]), {
            headers: { "Content-Type": type },
          }),
      ),
    );
    const blob = await readGalleryPhoto(
      { name: "a.png" },
      new AbortController().signal,
    );
    expect(blob.type).toBe(type);
    expect(blob.size).toBe(2);
  });
  test.each(["image/svg+xml", "text/html", "application/octet-stream"])(
    "%s is refused before reading its body",
    async (type) => {
      const cancel = vi.fn(async () => {});
      const getReader = vi.fn();
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => ({
          ok: true,
          headers: new Headers({ "Content-Type": type }),
          body: { cancel, getReader },
        })),
      );
      await expect(
        readGalleryPhoto({ name: "a.png" }, new AbortController().signal),
      ).rejects.toThrow();
      expect(cancel).toHaveBeenCalled();
      expect(getReader).not.toHaveBeenCalled();
    },
  );
  test.each([true, false])(
    "a response larger than 20 MiB is cancelled (declared %s)",
    async (declared) => {
      const cancel = vi.fn(async () => {});
      const reader = {
        read: vi
          .fn()
          .mockResolvedValueOnce({
            value: new Uint8Array(WALLPAPER_MAX_BYTES),
            done: false,
          })
          .mockResolvedValueOnce({ value: new Uint8Array([1]), done: false }),
        cancel,
        releaseLock: vi.fn(),
      };
      const headers = new Headers({
        "Content-Type": "image/png",
        ...(declared
          ? { "Content-Length": String(WALLPAPER_MAX_BYTES + 1) }
          : {}),
      });
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => ({
          ok: true,
          headers,
          body: { cancel, getReader: () => reader },
        })),
      );
      await expect(
        readGalleryPhoto({ name: "a.png" }, new AbortController().signal),
      ).rejects.toThrow();
      expect(cancel).toHaveBeenCalled();
      if (declared) expect(reader.read).not.toHaveBeenCalled();
    },
  );
  test("a malformed listing is an error, not an empty gallery", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => Response.json({ wallpapers: [{}] })),
    );
    await expect(readGallery(new AbortController().signal)).rejects.toThrow();
  });
});
