import test from "node:test";
import assert from "node:assert/strict";
import { consumeSSE } from "../sse.js";
test("chat events survive split UTF-8, permissions and CRLF boundaries", async () => {
  const input = new TextEncoder().encode(
    'event: permission\r\ndata: {"toolCall":{"toolCallId":"x"}}\r\n\r\ndata: {"text":"Привет"}\r\n\r\ndata: [DONE]\r\n\r\n',
  );
  const stream = new ReadableStream({
    start(c) {
      for (const byte of input) c.enqueue(new Uint8Array([byte]));
      c.close();
    },
  });
  const events = [];
  await consumeSSE(stream, (event, data) => events.push({ event, data }));
  assert.equal(events.length, 2);
  assert.equal(events[0].event, "permission");
  assert.equal(events[1].data.text, "Привет");
});
