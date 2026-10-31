import { describe, expect, test, vi } from "vitest";
import { configAtPath, extensionConfig, validateConfigPath } from "./configApi";

describe("the configuration API", () => {
  test("a path is a dot walk the extension owns", () => {
    expect(validateConfigPath("providers.+", "demo")).toBe(true);
    expect(validateConfigPath("providers.0.name", "demo")).toBe(true);
    expect(validateConfigPath("models.+", "demo")).toBe(true);
    expect(validateConfigPath("", "demo")).toBe(false);
    expect(validateConfigPath("a..b", "demo")).toBe(false);
    expect(validateConfigPath("a.b c", "demo")).toBe(false);
    expect(validateConfigPath("a.".repeat(40) + "b", "demo")).toBe(false);
    // Another extension's values are the one thing refused as a courtesy.
    expect(validateConfigPath("extension_values.demo.k", "demo")).toBe(true);
    expect(validateConfigPath("extension_values.other.k", "demo")).toBe(false);
  });

  test("reads walk the document and miss as undefined", () => {
    const doc = {
      providers: [{ name: "openai" }],
      agent: { model: "openai/gpt-5" },
    };
    expect(configAtPath(doc, "agent.model")).toBe("openai/gpt-5");
    expect(configAtPath(doc, "providers.0.name")).toBe("openai");
    expect(configAtPath(doc, "providers.9")).toBeUndefined();
    expect(configAtPath(doc, "agent.model.x")).toBeUndefined();
    expect(configAtPath(doc)).toBe(doc);
  });

  test("set writes one path through the host endpoint", async () => {
    const controller = new AbortController();
    const api = extensionConfig("demo", "sha", controller.signal);
    const fetchMock = vi.fn(
      async (_url: string, _init?: RequestInit) =>
        new Response(null, { status: 204 }),
    );
    vi.stubGlobal("fetch", fetchMock);
    await api.set("providers.+", { name: "neuraldeep" });
    expect(fetchMock).toHaveBeenCalledWith(
      "/coddy/extensions/demo/config",
      expect.objectContaining({ method: "POST" }),
    );
    const body = JSON.parse(
      String(
        (fetchMock.mock.calls[0] as unknown as [string, RequestInit])[1].body,
      ),
    );
    expect(body).toEqual({
      sha256: "sha",
      path: "providers.+",
      value: { name: "neuraldeep" },
    });
    await expect(api.set("bad path", 1)).rejects.toThrow(/configuration path/);
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });
});
