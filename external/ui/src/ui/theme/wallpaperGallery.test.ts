import { afterEach, describe, expect, test, vi } from "vitest";
import {
  readGallery,
  readGalleryPhoto,
  galleryPhotoUrl,
} from "./wallpaperGallery";
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
  ])("a %s selection is read as image bytes", async (type) => {
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
    "a response larger than 20 MiB is accepted (declared %s)",
    async (declared) => {
      const size = 20 * 1024 * 1024 + 1;
      const cancel = vi.fn(async () => {});
      const reader = {
        read: vi
          .fn()
          .mockResolvedValueOnce({
            value: new Uint8Array(size - 1),
            done: false,
          })
          .mockResolvedValueOnce({ value: new Uint8Array([1]), done: false })
          .mockResolvedValueOnce({ done: true }),
        cancel,
        releaseLock: vi.fn(),
      };
      vi.stubGlobal(
        "fetch",
        vi.fn(async () => ({
          ok: true,
          headers: new Headers({
            "Content-Type": "image/png",
            ...(declared ? { "Content-Length": String(size) } : {}),
          }),
          body: { cancel, getReader: () => reader },
        })),
      );
      const blob = await readGalleryPhoto(
        { name: "a.png" },
        new AbortController().signal,
      );
      expect(blob.size).toBe(size);
      expect(cancel).not.toHaveBeenCalled();
      expect(reader.releaseLock).toHaveBeenCalledOnce();
    },
  );
  test("a gallery listing larger than 20 MiB is accepted", async () => {
    const json = JSON.stringify({
      wallpapers: [{ name: "a.png" }],
      padding: " ".repeat(20 * 1024 * 1024),
    });
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(json, {
            headers: {
              "Content-Type": "application/json",
              "Content-Length": String(json.length),
            },
          }),
      ),
    );
    expect(await readGallery(new AbortController().signal)).toEqual([
      { name: "a.png" },
    ]);
  });
  test("a malformed listing is an error, not an empty gallery", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => Response.json({ wallpapers: [{}] })),
    );
    await expect(readGallery(new AbortController().signal)).rejects.toThrow();
  });
});
