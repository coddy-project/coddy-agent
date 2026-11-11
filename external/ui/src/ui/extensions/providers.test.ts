import { describe, expect, test, vi } from "vitest";
import { extensionProviders } from "./providers";

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("the providers API", () => {
  test("list returns only display-safe fields and freezes the result", async () => {
    const controller = new AbortController();
    const request = vi.fn(async (_url: string, _init?: RequestInit) =>
      json({
        providers: [
          {
            name: "main",
            type: "openai",
            api_base: "https://api.example.test/v1",
            api_key: "sk-secret",
            api_key_command: "print-secret",
            proxy: "http://proxy.example.test",
            timeout_ms: 30_000,
            usage_limits_panel: false,
            acp: { env: { TOKEN: "acp-secret" } },
            arbitrary: "not part of the SDK",
          },
          { name: "other", type: "anthropic" },
        ],
      }),
    );
    const api = extensionProviders(controller.signal, { request });

    const rows = await api.list();

    expect(rows).toEqual([
      {
        name: "main",
        type: "openai",
        api_base: "https://api.example.test/v1",
        proxy: "http://proxy.example.test",
        timeout_ms: 30_000,
        usage_limits_panel: false,
      },
      { name: "other", type: "anthropic" },
    ]);
    expect(Object.isFrozen(rows)).toBe(true);
    expect(Object.isFrozen(rows[0])).toBe(true);
    expect(Object.isFrozen(rows[1])).toBe(true);
    expect(request).toHaveBeenCalledWith("/coddy/config", {
      method: "GET",
      signal: controller.signal,
    });
  });

  test("models normalizes token counts, dedupes ids and freezes every row", async () => {
    const request = vi.fn(async () =>
      json({
        ok: true,
        models: [
          {
            id: "model-one",
            name: "Model One",
            context_window: 32_768.9,
            catalog: {
              context_window: 131_072.8,
              max_tokens: 4_096.7,
              multimodal: true,
            },
            ignored: "extra",
          },
          { id: "model-one", name: "Duplicate", context_window: 1 },
          {
            id: "plain",
            name: "",
            context_window: 0,
            catalog: {
              context_window: -1,
              max_tokens: 100,
              multimodal: true,
            },
          },
          { id: "", name: "Missing id" },
        ],
      }),
    );
    const api = extensionProviders(new AbortController().signal, { request });

    const models = await api.models("main");

    expect(models).toEqual([
      {
        id: "model-one",
        name: "Model One",
        context_window: 32_768,
        catalog: {
          context_window: 131_072,
          max_tokens: 4_096,
          multimodal: true,
        },
      },
      { id: "plain" },
    ]);
    expect(Object.isFrozen(models)).toBe(true);
    expect(Object.isFrozen(models[0])).toBe(true);
    expect(Object.isFrozen(models[0]?.catalog)).toBe(true);
    expect(Object.isFrozen(models[1])).toBe(true);
  });

  test("models encodes the provider name and rejects an empty one", async () => {
    const request = vi.fn(async () => json({ ok: true, models: [] }));
    const api = extensionProviders(new AbortController().signal, { request });

    await api.models("team/a ?#%");

    expect(request).toHaveBeenCalledWith(
      "/coddy/providers/team%2Fa%20%3F%23%25/models",
      expect.objectContaining({ method: "GET" }),
    );
    await expect(api.models(" \t ")).rejects.toThrow(
      "Providers API: provider name must be non-empty",
    );
    expect(request).toHaveBeenCalledTimes(1);
  });

  test("HTTP and upstream failures reject with Providers API errors", async () => {
    const http = extensionProviders(new AbortController().signal, {
      request: async () => new Response("bad gateway", { status: 502 }),
    });
    await expect(http.list()).rejects.toThrow("Providers API: HTTP 502");

    const upstream = extensionProviders(new AbortController().signal, {
      request: async () =>
        json({ ok: false, error: "upstream credentials rejected" }),
    });
    await expect(upstream.models("main")).rejects.toThrow(
      "Providers API: upstream credentials rejected",
    );
  });

  test("abort reaches the transport and disposes later calls", async () => {
    const controller = new AbortController();
    const request = vi.fn(
      (_url: string, init?: RequestInit) =>
        new Promise<Response>((_resolve, reject) => {
          const signal = init?.signal;
          if (signal?.aborted) {
            reject(signal.reason);
            return;
          }
          signal?.addEventListener("abort", () => reject(signal.reason), {
            once: true,
          });
        }),
    );
    const api = extensionProviders(controller.signal, { request });

    const pending = api.list();
    const rejection = expect(pending).rejects.toMatchObject({
      name: "AbortError",
    });
    controller.abort(new DOMException("Extension disposed", "AbortError"));
    await rejection;

    await expect(api.list()).rejects.toMatchObject({ name: "AbortError" });
    await expect(api.models("main")).rejects.toMatchObject({
      name: "AbortError",
    });
    expect(request).toHaveBeenCalledTimes(1);
  });
});
