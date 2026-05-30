import { expect, test } from "vitest";
import { toolCallArgsDisplay } from "./toolCallArgsDisplay";

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
