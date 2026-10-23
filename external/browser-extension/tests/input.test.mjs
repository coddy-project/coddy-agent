import test from "node:test";
import assert from "node:assert/strict";
import { chromeInput } from "../input.js";

function mock(frames, cancelled) {
  const calls = [];
  const api = {
    debugger: {
      attach: async () => {},
      detach: async () => {},
      sendCommand: async (_, method, data) => {
        calls.push({ method, data });
        if (method === "Page.getFrameTree") {
          const frame = frames.length > 1 ? frames.shift() : frames[0];
          cancelled?.();
          return { frameTree: { frame } };
        }
        return { data: "png" };
      },
    },
  };
  return { api, calls };
}
const approved = { url: "https://approved.test/", loaderId: "one" };
test("native input and screenshots refuse an unapproved origin", async () => {
  for (const action of ["click", "type", "screenshot"]) {
    const { api, calls } = mock([
      { url: "https://other.test/", loaderId: "two" },
    ]);
    await assert.rejects(
      chromeInput(api, 1, action, {
        expected_origin: "https://approved.test",
        text: "secret",
        x: 1,
        y: 1,
      }),
      /Page changed/,
    );
    assert.equal(
      calls.filter((c) => c.method !== "Page.getFrameTree").length,
      0,
    );
  }
});
test("navigation during drag stops moves and releases the mouse", async () => {
  const { api, calls } = mock([approved, { ...approved, loaderId: "two" }]);
  await assert.rejects(
    chromeInput(api, 1, "drag", {
      expected_origin: "https://approved.test",
      x: 1,
      y: 1,
      to_x: 2,
      to_y: 2,
    }),
    /Page changed/,
  );
  assert.deepEqual(
    calls
      .filter((c) => c.method === "Input.dispatchMouseEvent")
      .map((c) => c.data.type),
    ["mousePressed", "mouseReleased"],
  );
});
test("stop during document check prevents dispatch", async () => {
  let stopped = false;
  const { api, calls } = mock([approved], () => {
    stopped = true;
  });
  await assert.rejects(
    chromeInput(api, 1, "type", {
      expected_origin: "https://approved.test",
      text: "secret",
      cancelled: () => stopped,
    }),
    /Stopped/,
  );
  assert.equal(
    calls.some((c) => c.method === "Input.insertText"),
    false,
  );
});
