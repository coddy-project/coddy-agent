import { afterEach, expect, test, vi } from "vitest";
import { PDF_TYPE, RASTER_IMAGE_TYPE, acquireObjectUrl } from "./objectUrl";
afterEach(() => vi.unstubAllGlobals());
test("mounted image consumers retain a shared typed object URL", async () => {
  const create = vi.fn((_blob: Blob) => "blob:shared");
  const revoke = vi.fn();
  vi.stubGlobal(
    "URL",
    Object.assign(URL, { createObjectURL: create, revokeObjectURL: revoke }),
  );
  const fetcher = vi.fn(
    async () =>
      new Response("png bytes", { headers: { "Content-Type": "image/png" } }),
  );
  vi.stubGlobal("fetch", fetcher);
  const a = acquireObjectUrl("/coddy/test/image");
  const b = acquireObjectUrl("/coddy/test/image");
  expect(await a.promise).toBe("blob:shared");
  expect(await b.promise).toBe("blob:shared");
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(create.mock.calls[0]?.[0]).toHaveProperty("type", "image/png");
  a.release();
  expect(revoke).not.toHaveBeenCalled();
  b.release();
  expect(revoke).toHaveBeenCalledWith("blob:shared");
});

test("a preview rejects active content after a file's type changes", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response("<svg/>", {
          headers: { "Content-Type": "application/octet-stream" },
        }),
    ),
  );
  const lease = acquireObjectUrl("/coddy/test/type-race", {
    accept: RASTER_IMAGE_TYPE,
  });
  await expect(lease.promise).rejects.toThrow("no longer");
  lease.release();
});
test("unknown-length responses are stopped at the byte cap", async () => {
  const cancel = vi.fn();
  const body = new ReadableStream({
    start(controller) {
      controller.enqueue(new Uint8Array(3));
      controller.enqueue(new Uint8Array(3));
    },
    cancel,
  });
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(body, { headers: { "Content-Type": "image/png" } }),
    ),
  );
  const lease = acquireObjectUrl("/coddy/test/large", { cap: 4 });
  await expect(lease.promise).rejects.toThrow("preview limit");
  expect(cancel).toHaveBeenCalled();
  lease.release();
});

// A PDF goes to the browser's own viewer only as a PDF: a file rewritten as
// something else under the same path is refused rather than shown.
test("a PDF preview takes only bytes the node serves as a PDF", async () => {
  const create = vi.fn((_blob: Blob) => "blob:pdf");
  vi.stubGlobal(
    "URL",
    Object.assign(URL, { createObjectURL: create, revokeObjectURL: vi.fn() }),
  );
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: unknown) =>
      String(input).includes("good")
        ? new Response("%PDF-1.7", {
            headers: { "Content-Type": "application/pdf" },
          })
        : new Response("<html></html>", {
            headers: { "Content-Type": "application/octet-stream" },
          }),
    ),
  );
  const good = acquireObjectUrl("/coddy/test/good.pdf", { accept: PDF_TYPE });
  expect(await good.promise).toBe("blob:pdf");
  expect(create.mock.calls[0]?.[0]).toHaveProperty("type", "application/pdf");
  good.release();
  const bad = acquireObjectUrl("/coddy/test/bad.pdf", { accept: PDF_TYPE });
  await expect(bad.promise).rejects.toThrow("no longer");
  bad.release();
});

// The node serves an SVG as a download; the preview gives its bytes the type
// it shows them as, which only an <img> ever receives.
test("bytes served as a download are typed as the preview asks", async () => {
  const create = vi.fn((_blob: Blob) => "blob:svg");
  vi.stubGlobal(
    "URL",
    Object.assign(URL, { createObjectURL: create, revokeObjectURL: vi.fn() }),
  );
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response("<svg/>", {
          headers: { "Content-Type": "application/octet-stream" },
        }),
    ),
  );
  const lease = acquireObjectUrl("/coddy/test/logo.svg", {
    as: "image/svg+xml",
  });
  expect(await lease.promise).toBe("blob:svg");
  expect(create.mock.calls[0]?.[0]).toHaveProperty("type", "image/svg+xml");
  lease.release();
});
