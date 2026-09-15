import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { initLocale } from "../i18n/i18n";
import { fetchSubagentCatalog } from "./subagentsApi";

beforeEach(() => {
  initLocale("en");
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function stubFetch(response: {
  ok: boolean;
  status?: number;
  body?: unknown;
  reject?: boolean;
}) {
  const calls: Array<{ url: string; init?: RequestInit }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      calls.push({ url: String(url), ...(init ? { init } : {}) });
      if (response.reject) {
        return Promise.reject(new Error("offline"));
      }
      return Promise.resolve({
        ok: response.ok,
        status: response.status ?? (response.ok ? 200 : 500),
        json: async () => response.body,
      });
    }),
  );
  return calls;
}

test("the catalog asks about the session workspace when one is known", async () => {
  const calls = stubFetch({
    ok: true,
    body: { workspace: "/work/repo", policy: "ask", items: [] },
  });
  const res = await fetchSubagentCatalog("  /work/repo ");
  expect(calls[0]?.url).toBe("/coddy/subagents?cwd=%2Fwork%2Frepo");
  expect(res).toEqual({
    ok: true,
    data: { workspace: "/work/repo", policy: "ask", items: [] },
  });

  await fetchSubagentCatalog(undefined);
  expect(calls[1]?.url).toBe("/coddy/subagents");
});

test("a catalog body with missing fields is normalised, not trusted", async () => {
  stubFetch({ ok: true, body: { items: "nope" } });
  const res = await fetchSubagentCatalog();
  expect(res).toEqual({
    ok: true,
    data: { workspace: "", policy: "ask", items: [] },
  });
});

test("the server's error message is what the caller gets", async () => {
  stubFetch({
    ok: false,
    status: 400,
    body: { error: { message: "cwd must be an absolute path" } },
  });
  expect(await fetchSubagentCatalog("relative")).toEqual({
    ok: false,
    error: "cwd must be an absolute path",
  });
});

test("an unreachable server is reported in words", async () => {
  stubFetch({ ok: false, reject: true });
  expect(await fetchSubagentCatalog("/work/repo")).toEqual({
    ok: false,
    error: "the server could not be reached",
  });
});
