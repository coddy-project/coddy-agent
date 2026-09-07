import { expect, test } from "vitest";
import { parseSSEBlocks } from "./sse";

test("named events and data are parsed per block", () => {
  const carry = { buf: "" };
  const events = parseSSEBlocks(
    `event: tool_call\ndata: {"a":1}\n\ndata: plain\n\n`,
    carry,
  );
  expect(events).toEqual([
    { event: "tool_call", data: '{"a":1}', id: "" },
    { event: "", data: "plain", id: "" },
  ]);
  expect(carry.buf).toBe("");
});

// The composer relay prefixes each frame with its sequence so a reconnecting client can
// resume from it; dropping the line would make resume impossible.
test("the id line is surfaced with its frame", () => {
  const carry = { buf: "" };
  const events = parseSSEBlocks(`id: 42\ndata: hello\n\n`, carry);
  expect(events).toEqual([{ event: "", data: "hello", id: "42" }]);
});

test("a partial block is carried to the next chunk", () => {
  const carry = { buf: "" };
  expect(parseSSEBlocks(`id: 7\ndata: half`, carry)).toEqual([]);
  expect(parseSSEBlocks(`-done\n\n`, carry)).toEqual([
    { event: "", data: "half-done", id: "7" },
  ]);
});

test("multi-line data keeps its line breaks", () => {
  const carry = { buf: "" };
  expect(parseSSEBlocks(`data: one\ndata: two\n\n`, carry)).toEqual([
    { event: "", data: "one\ntwo", id: "" },
  ]);
});
