import { expect, test } from "vitest";

import {
  ShadowTranscriptCache,
  sidsToEvict,
  touchLruSid,
} from "./sessionTranscriptCache";

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

test("cache: every set records recency, so a side-path write is still evictable", () => {
  const c = new ShadowTranscriptCache<number[]>();
  c.set("s1", [1]);
  c.set("s2", [2]);
  c.set("s3", [3]);
  c.set("s4", [4]);
  expect(c.keys()).toEqual(["s1", "s2", "s3", "s4"]);
  const victims = c.evict({
    viewedSid: "s4",
    activeStreamSids: new Set(),
    cap: 2,
  });
  expect(victims).toEqual(["s1"]);
  expect(c.has("s1")).toBe(false);
  expect(c.size).toBe(3);
});

test("cache: re-setting an existing sid moves it to the most recent end", () => {
  const c = new ShadowTranscriptCache<string>();
  c.set("s1", "a");
  c.set("s2", "b");
  c.set("s1", "a2");
  expect(c.keys()).toEqual(["s2", "s1"]);
  expect(c.get("s1")).toBe("a2");
});

test("cache: touch bumps recency only for cached sids", () => {
  const c = new ShadowTranscriptCache<string>();
  c.set("s1", "a");
  c.set("s2", "b");
  c.touch("s1");
  c.touch("missing");
  expect(c.keys()).toEqual(["s2", "s1"]);
  expect(c.has("missing")).toBe(false);
});

test("cache: viewed and streaming sessions are pinned through evict", () => {
  const c = new ShadowTranscriptCache<string>();
  for (const sid of ["s1", "s2", "s3", "s4", "s5"]) c.set(sid, sid);
  const victims = c.evict({
    viewedSid: "s1",
    activeStreamSids: new Set(["s2"]),
    cap: 1,
  });
  expect(victims).toEqual(["s3", "s4"]);
  expect(c.keys()).toEqual(["s1", "s2", "s5"]);
});

test("cache: delete drops the entry and its LRU slot", () => {
  const c = new ShadowTranscriptCache<string>();
  c.set("s1", "a");
  c.set("s2", "b");
  expect(c.delete("s1")).toBe(true);
  expect(c.delete("s1")).toBe(false);
  expect(c.keys()).toEqual(["s2"]);
});

test("cache: rename keeps the value under the new sid and frees the old one", () => {
  const c = new ShadowTranscriptCache<string>();
  c.set("draft", "items");
  c.set("other", "x");
  c.rename("draft", "sess_real");
  expect(c.has("draft")).toBe(false);
  expect(c.get("sess_real")).toBe("items");
  expect(c.keys()).toEqual(["other", "sess_real"]);
  c.rename("missing", "whatever");
  expect(c.keys()).toEqual(["other", "sess_real"]);
});

test("cache: blank sids are ignored and keys are trimmed", () => {
  const c = new ShadowTranscriptCache<string>();
  c.set("  ", "nope");
  c.set(" s1 ", "a");
  expect(c.size).toBe(1);
  expect(c.get("s1")).toBe("a");
  expect(c.has(" s1")).toBe(true);
});
