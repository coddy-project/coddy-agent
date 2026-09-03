import { expect, test } from "vitest";

import type { TranscriptItem } from "./types";
import { transcriptItemsAffectAutoScroll } from "./transcriptAutoScroll";

const plan: TranscriptItem = {
  id: "p1",
  type: "plan_document",
  slug: "demo",
  name: "Demo",
  overview: "o",
  content: "---\n---\n# Hi",
  body: "# Hi",
  expanded: false,
};

test("plan expand or discard alone does not affect auto scroll", () => {
  const prev = [plan];
  expect(
    transcriptItemsAffectAutoScroll(prev, [{ ...plan, expanded: true }]),
  ).toBe(false);
  expect(
    transcriptItemsAffectAutoScroll(prev, [{ ...plan, discarded: true }]),
  ).toBe(false);
});

test("plan body change affects auto scroll", () => {
  const prev = [plan];
  expect(
    transcriptItemsAffectAutoScroll(prev, [
      { ...plan, body: "# Hi\n\nmore", expanded: true },
    ]),
  ).toBe(true);
});

test("new transcript row affects auto scroll", () => {
  const prev: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "hi" },
  ];
  const next: TranscriptItem[] = [
    ...prev,
    { id: "a1", type: "assistant_message", content: "hello" },
  ];
  expect(transcriptItemsAffectAutoScroll(prev, next)).toBe(true);
});

test("same array reference does not affect auto scroll", () => {
  const items: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "hi" },
    { id: "a1", type: "assistant_message", content: "hello" },
  ];
  expect(transcriptItemsAffectAutoScroll(items, items)).toBe(false);
});

test("new array with same item references does not affect auto scroll", () => {
  const prev: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "hi" },
    { id: "a1", type: "assistant_message", content: "hello" },
  ];
  expect(transcriptItemsAffectAutoScroll(prev, [...prev])).toBe(false);
});

test("single mutated item among stable references affects auto scroll", () => {
  const prev: TranscriptItem[] = [
    { id: "u1", type: "user_message", content: "hi" },
    { id: "a1", type: "assistant_message", content: "hel", streaming: true },
  ];
  const next: TranscriptItem[] = [
    prev[0]!,
    { id: "a1", type: "assistant_message", content: "hello", streaming: true },
  ];
  expect(transcriptItemsAffectAutoScroll(prev, next)).toBe(true);
});

test("assistant streaming update affects auto scroll", () => {
  const prev: TranscriptItem[] = [
    { id: "a1", type: "assistant_message", content: "hel", streaming: true },
  ];
  const next: TranscriptItem[] = [
    { id: "a1", type: "assistant_message", content: "hello", streaming: true },
  ];
  expect(transcriptItemsAffectAutoScroll(prev, next)).toBe(true);
});
