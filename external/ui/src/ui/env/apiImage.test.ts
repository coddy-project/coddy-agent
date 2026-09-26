import { afterEach, expect, test } from "vitest";

import { apiImageRequest } from "./apiImage";
import { setEnv } from "./remoteEnv";

afterEach(() => setEnv({ mode: "local" }));

const thumb = "/coddy/sessions/s1/assets/pasted-1.png/thumbnail";

test("on the local origin an <img> loads the server's URL itself", () => {
  setEnv({ mode: "local" });
  expect(apiImageRequest(thumb)).toBeNull();
});

test("in a remote environment the URL is asked of it, the token in a header", () => {
  setEnv({ mode: "remote", baseUrl: "http://relay.example/swarm/nodes/node/", token: "tok" });
  const req = apiImageRequest(thumb);
  expect(req?.url).toBe("http://relay.example/swarm/nodes/node" + thumb);
  expect(new Headers(req?.init.headers).get("Authorization")).toBe("Bearer tok");
  expect(req?.url).not.toContain("tok");
});

test("a remote environment without a token sends no Authorization header", () => {
  setEnv({ mode: "remote", baseUrl: "http://node.example", token: "" });
  const req = apiImageRequest(thumb);
  expect(req?.url).toBe("http://node.example" + thumb);
  expect(new Headers(req?.init.headers).has("Authorization")).toBe(false);
});

test("what is not the server's API loads as it is, remote or not", () => {
  setEnv({ mode: "remote", baseUrl: "http://relay.example/swarm/nodes/node", token: "tok" });
  for (const url of [
    "blob:http://relay.example/5f1c",
    "data:image/png;base64,aGVsbG8=",
    "https://raw.githubusercontent.com/org/repo/main/shot.png",
    "/assets/coddy-logo.svg",
  ]) {
    expect(apiImageRequest(url)).toBeNull();
  }
});
