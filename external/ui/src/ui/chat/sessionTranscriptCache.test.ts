import { expect, test } from "vitest";

import { sidsToEvict, touchLruSid } from "./sessionTranscriptCache";

test("evicts the least recently used sids beyond the cap", () => {
  expect(
    sidsToEvict({
      cachedSids: ["s1", "s2", "s3", "s4", "s5"],
      viewedSid: "s5",
      activeStreamSids: new Set(),
      cap: 2,
    }),
  ).toEqual(["s1", "s2"]);
});

test("never evicts the viewed session", () => {
  expect(
    sidsToEvict({
      cachedSids: ["s1", "s2", "s3"],
      viewedSid: "s1",
      activeStreamSids: new Set(),
      cap: 1,
    }),
  ).toEqual(["s2"]);
});

test("never evicts sessions with an active stream", () => {
  expect(
    sidsToEvict({
      cachedSids: ["s1", "s2", "s3", "s4"],
      viewedSid: "s4",
      activeStreamSids: new Set(["s1", "s2"]),
      cap: 0,
    }),
  ).toEqual(["s3"]);
});

test("no eviction when at or under the cap", () => {
  expect(
    sidsToEvict({
      cachedSids: ["s1", "s2", "s3"],
      viewedSid: "s3",
      activeStreamSids: new Set(),
      cap: 2,
    }),
  ).toEqual([]);
});

test("cap zero evicts every non-pinned sid", () => {
  expect(
    sidsToEvict({
      cachedSids: ["s1", "s2", "s3"],
      viewedSid: "s3",
      activeStreamSids: new Set(),
      cap: 0,
    }),
  ).toEqual(["s1", "s2"]);
});

test("touchLruSid moves an existing sid to the most-recent end", () => {
  const order = ["s1", "s2", "s3"];
  touchLruSid(order, "s1");
  expect(order).toEqual(["s2", "s3", "s1"]);
});

test("touchLruSid appends a new sid and ignores blanks", () => {
  const order = ["s1"];
  touchLruSid(order, "s2");
  touchLruSid(order, "  ");
  expect(order).toEqual(["s1", "s2"]);
});
