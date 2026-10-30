import { afterEach, expect, test, vi } from "vitest";
import { extensionSettings } from "./settings";

vi.mock("../settings/settingsConfigStore", () => ({
  noteSettingsConfigReloaded: vi.fn(),
  noteSettingsConfigSaved: vi.fn(),
}));
afterEach(() => vi.unstubAllGlobals());
test("SDK settings reads defaults and writes only its owner with a revision", async () => {
  let doc = {
    revision: "r1",
    agent: { model: "x" },
    extension_values: { other: { badge: "other" }, demo: { keep: "yes" } },
  };
  const fetcher = vi.fn(async (_url: string, init?: RequestInit) => {
    if (init?.method === "PUT") {
      doc = JSON.parse(init.body as string);
      return Response.json({ ok: true });
    }
    return Response.json(doc);
  });
  vi.stubGlobal("fetch", fetcher);
  const controller = new AbortController();
  const api = extensionSettings(
    "demo",
    controller.signal,
    vi.fn(),
    vi.fn(),
    () => [
      {
        id: "badge",
        section: "agent",
        title: "Badge",
        type: "string",
        default: "DEFAULT",
      },
    ],
  );
  expect(await api.get("badge")).toBe("DEFAULT");
  await api.set("badge", "CUSTOM");
  expect(doc).toEqual({
    revision: "r1",
    agent: { model: "x" },
    extension_values: {
      other: { badge: "other" },
      demo: { keep: "yes", badge: "CUSTOM" },
    },
  });
  expect(await api.get("badge")).toBe("CUSTOM");
  await api.set("badge", null);
  expect(await api.get("badge")).toBe("DEFAULT");
  await expect(api.set("badge", 1)).rejects.toThrow(
    "Invalid extension setting value",
  );
  controller.abort();
  await expect(api.get("badge")).rejects.toMatchObject({ name: "AbortError" });
});
