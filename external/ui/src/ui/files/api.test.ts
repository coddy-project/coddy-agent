import { afterEach, expect, test, vi } from "vitest";
import { getEnv, setEnv } from "../env/remoteEnv";
import { mediaUrl, relativeFilePath } from "./api";
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
