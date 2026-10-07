import { afterEach, expect, test, vi } from "vitest";
import { acquireObjectUrl } from "./objectUrl";
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
  const lease = acquireObjectUrl("/coddy/test/type-race", undefined, true);
  await expect(lease.promise).rejects.toThrow("supported image");
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
  const lease = acquireObjectUrl("/coddy/test/large", 4);
  await expect(lease.promise).rejects.toThrow("preview limit");
  expect(cancel).toHaveBeenCalled();
  lease.release();
});
