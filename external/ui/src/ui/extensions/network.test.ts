import { afterEach, expect, test, vi } from "vitest";
import { extensionNetwork } from "./network";

afterEach(() => vi.unstubAllGlobals());

test("server proxy preserves binary data, explicit credentials and HTTP errors", async () => {
  const fetcher = vi.fn(async () =>
    Response.json({
      status: 418,
      status_text: "I'm a teapot",
      headers: {
        "X-Target": ["local"],
        "Content-Type": ["application/octet-stream"],
      },
      body: btoa("\x00\xff\x80"),
    }),
  );
  vi.stubGlobal("fetch", fetcher);
  const net = extensionNetwork(
    "demo",
    "approved",
    new AbortController().signal,
  );
  const response = await net.fetch("http://127.0.0.1:8080/api", {
    method: "post",
    headers: { Authorization: "Bearer target" },
    body: new Uint8Array([255, 0, 128]),
  });
  const [url, init] = fetcher.mock.calls[0] as unknown as [string, RequestInit];
  expect(url).toBe("/coddy/extensions/demo/fetch");
  expect(init.headers).toEqual({ "Content-Type": "application/json" });
  expect(JSON.parse(init.body as string)).toEqual({
    sha256: "approved",
    url: "http://127.0.0.1:8080/api",
    method: "POST",
    headers: { authorization: "Bearer target" },
    body: btoa("\xff\x00\x80"),
  });
  expect(response.status).toBe(418);
  expect(response.ok).toBe(false);
  expect(response.headers.get("X-Target")).toBe("local");
  expect(new Uint8Array(await response.arrayBuffer())).toEqual(
    new Uint8Array([0, 255, 128]),
  );
});

test.each([204, 205, 304])(
  "bodyless status %i returns a readable Response",
  async (status) => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        Response.json({ status, status_text: "", headers: {}, body: null }),
      ),
    );
    const net = extensionNetwork(
      "demo",
      "approved",
      new AbortController().signal,
    );
    expect(await (await net.fetch("https://example.test")).text()).toBe("");
  },
);

test("proxy errors reject instead of becoming target responses", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response("revoked", { status: 409 })),
  );
  const net = extensionNetwork(
    "demo",
    "approved",
    new AbortController().signal,
  );
  await expect(net.fetch("http://localhost")).rejects.toThrow(
    "HTTP 409: revoked",
  );
});

test("body encoding preserves content type and enforces the upload limit", async () => {
  const fetcher = vi.fn(async () =>
    Response.json({ status: 200, status_text: "OK", headers: {}, body: "" }),
  );
  vi.stubGlobal("fetch", fetcher);
  const net = extensionNetwork(
    "demo",
    "approved",
    new AbortController().signal,
  );
  await net.fetch("http://localhost", {
    method: "POST",
    body: new URLSearchParams({ q: "a b" }),
  });
  const init = (fetcher.mock.calls[0] as unknown as [string, RequestInit])[1];
  expect(JSON.parse(init.body as string).headers["content-type"]).toBe(
    "application/x-www-form-urlencoded;charset=UTF-8",
  );
  await expect(
    net.fetch("http://localhost", {
      method: "POST",
      body: new Uint8Array(4 * 1024 * 1024 + 1),
    }),
  ).rejects.toThrow("exceeds 4 MiB");
  expect(fetcher).toHaveBeenCalledOnce();
});

test.each(["lifetime", "caller"])(
  "%s abort cancels in-flight proxy calls and later calls stay closed",
  async (source) => {
    const lifetime = new AbortController();
    const caller = new AbortController();
    let requestSignal: AbortSignal | undefined;
    const fetcher = vi.fn(
      (_url: string, init: RequestInit) =>
        new Promise<Response>((_resolve, reject) => {
          requestSignal = init.signal!;
          requestSignal.addEventListener(
            "abort",
            () => reject(new DOMException("Aborted", "AbortError")),
            { once: true },
          );
        }),
    );
    vi.stubGlobal("fetch", fetcher);
    const net = extensionNetwork("demo", "approved", lifetime.signal);
    const pending = net.fetch("http://localhost", { signal: caller.signal });
    const rejected = expect(pending).rejects.toMatchObject({
      name: "AbortError",
    });
    (source === "lifetime" ? lifetime : caller).abort();
    await rejected;
    expect(requestSignal?.aborted).toBe(true);
    await expect(
      net.fetch("http://localhost", { signal: caller.signal }),
    ).rejects.toMatchObject({ name: "AbortError" });
    expect(fetcher).toHaveBeenCalledOnce();
  },
);
