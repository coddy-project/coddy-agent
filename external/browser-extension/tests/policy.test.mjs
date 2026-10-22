import test from "node:test";
import assert from "node:assert/strict";
import { siteOf, serverURL, canRun } from "../policy.js";
import { pageAction } from "../page.js";

test("page navigation cannot reuse a grant checked before injection", async () => {
  globalThis.location = { origin: "https://changed.example" };
  try {
    await assert.rejects(
      pageAction("snapshot", { expected_origin: "https://approved.example" }),
      /Page changed/,
    );
  } finally {
    delete globalThis.location;
  }
});
test("remote listener requires TLS; loopback works without a proxy", () => {
  assert.equal(serverURL("http://localhost:12348"), "http://localhost:12348");
  assert.equal(
    serverURL("https://agent.example:12348"),
    "https://agent.example:12348",
  );
  for (const s of [
    "http://agent.example",
    "https://user:pass@host",
    "https://host/?key=secret",
    "https://host/path",
    "file:///tmp",
  ])
    assert.throws(() => serverURL(s));
});
test("site grants are exact origins and cannot share an unselected tab", () => {
  assert.equal(siteOf("https://example.com/a?q=b"), "https://example.com");
  assert.equal(
    canRun([1], 1, "https://example.com/path", ["https://example.com"]),
    true,
  );
  assert.equal(
    canRun([1], 1, "https://other.example", ["https://example.com"]),
    false,
  );
  assert.throws(() =>
    canRun([2], 1, "https://example.com", ["https://example.com"]),
  );
  for (const s of [
    "chrome://settings",
    "about:config",
    "file:///etc/passwd",
    "javascript:alert(1)",
  ])
    assert.throws(() => siteOf(s));
});
