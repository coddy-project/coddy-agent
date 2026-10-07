import { afterEach, expect, test, vi } from "vitest";
import { getEnv, setEnv } from "../env/remoteEnv";
import { mediaUrl, relativeFilePath, rereadTree } from "./api";
const original = getEnv();
afterEach(() => {
  setEnv(original);
  vi.unstubAllGlobals();
});

test("native media URLs keep a remote path prefix and use a path-bound token", async () => {
  setEnv({
    mode: "remote",
    baseUrl: "https://remote.example/node/alpha",
    token: "bearer-secret",
  });
  const fetcher = vi.fn(
    async () =>
      new Response(JSON.stringify({ token: "signed-capability" }), {
        status: 200,
      }),
  );
  vi.stubGlobal("fetch", fetcher);
  const url = await mediaUrl("sess_1", "clips/a.mp4", true);
  expect(url).toContain(
    "https://remote.example/node/alpha/coddy/sessions/sess_1/workspace/raw",
  );
  expect(url).toContain("access_token=signed-capability");
  expect(url).not.toContain("bearer-secret");
  const request = fetcher.mock.calls[0] as unknown as [string, RequestInit];
  expect(new Headers(request[1].headers).get("Authorization")).toBe(
    "Bearer bearer-secret",
  );
});
test("Markdown links normalize parents without escaping the workspace", () => {
  expect(relativeFilePath("../images/plot.png", "docs/readme.md")).toBe(
    "images/plot.png",
  );
  expect(relativeFilePath("../../outside", "docs/readme.md")).toBeNull();
  expect(relativeFilePath("C:\\project\\src\\a.go", "", "C:\\project")).toBe(
    "src/a.go",
  );
});

// A server that says there is more and gives nothing more must not hold the
// refresh of a folder forever.
test("reading a folder again stops when a page brings nothing new", async () => {
  let calls = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => {
      calls += 1;
      return new Response(
        JSON.stringify({
          entries:
            calls === 1
              ? [{ name: "a", path_rel: "a", kind: "file", size_bytes: 1, mod_time: "" }]
              : [],
          has_more: true,
          next_cursor: "f/a",
        }),
      );
    }),
  );
  const page = await rereadTree("sess_1", "", 500, false);
  expect(page.entries.map((e) => e.name)).toEqual(["a"]);
  expect(calls).toBeLessThanOrEqual(3);
});
