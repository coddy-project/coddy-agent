import { expect, test } from "vitest";
import { pickRicherToolArgs } from "./toolCallArgs";

test("pickRicherToolArgs keeps existing complete JSON over a truncated preview", () => {
  const full = JSON.stringify({ path: "a.ts", content: "x".repeat(400) });
  const truncated = full.slice(0, 200) + "...";
  expect(pickRicherToolArgs(full, truncated)).toBe(full);
});

test("pickRicherToolArgs falls back to the preview when nothing richer exists", () => {
  const truncated = '{"path":"a.ts","content":"start of a long';
  expect(pickRicherToolArgs(undefined, truncated)).toBe(truncated);
  expect(pickRicherToolArgs("", truncated)).toBe(truncated);
  expect(pickRicherToolArgs('{"broken', truncated)).toBe(truncated);
});

test("pickRicherToolArgs prefers the persisted preview when both parse", () => {
  const fromMessage = '{"path":"a.ts","content":"hi"}';
  const preview = '{"path":"a.ts","content":"hi"}';
  expect(pickRicherToolArgs(fromMessage, preview)).toBe(preview);
});
