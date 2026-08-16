import { expect, test } from "vitest";
import { pickRicherToolArgs, toolCallArgsDisplay } from "./toolCallArgsDisplay";

test("run_command shows command line only", () => {
  expect(
    toolCallArgsDisplay('{"command":"ls -la"}', { kind: "run_command" }),
  ).toBe("ls -la");
});

test("unknown tool keeps pretty JSON", () => {
  const out = toolCallArgsDisplay('{"foo":1}', { kind: "other" });
  expect(out).toContain('"foo"');
});

test("apply_patch returns file path only (diff rendered separately)", () => {
  const args = JSON.stringify({ filePath: "src/app.ts", patch: "--- a/src/app.ts\n..." });
  expect(toolCallArgsDisplay(args, { kind: "apply_patch" })).toBe("src/app.ts");
});

test("apply_patch with no filePath returns empty string", () => {
  const args = JSON.stringify({ patch: "@@ -1 +1 @@\n+new" });
  expect(toolCallArgsDisplay(args, { kind: "apply_patch" })).toBe("");
});

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
