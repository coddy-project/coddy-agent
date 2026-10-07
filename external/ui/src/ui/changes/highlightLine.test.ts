import { describe, expect, test } from "vitest";
import { highlightLine } from "./highlightLine";

function joined(spans: { text: string }[] | null): string {
  return (spans ?? []).map((s) => s.text).join("");
}

describe("highlightLine", () => {
  test("splits a line into classed spans", () => {
    const spans = highlightLine("const x = 1;", "typescript");
    expect(spans).not.toBeNull();
    expect(spans!.some((s) => s.className === "hljs-keyword")).toBe(true);
    expect(spans!.some((s) => s.className === "hljs-number")).toBe(true);
  });

  // The diff renders with white-space: pre, so a highlighter that drops or
  // reflows a single character would shift the code away from its line number.
  test("reproduces the line exactly, indentation included", () => {
    const line = "    return { ok: true };  ";
    expect(joined(highlightLine(line, "typescript"))).toBe(line);
  });

  test("keeps text that carries no token class", () => {
    const spans = highlightLine("x = 1", "python");
    expect(joined(spans)).toBe("x = 1");
    expect(spans!.some((s) => s.className === "")).toBe(true);
  });

  test("flattens nested tokens to the innermost class", () => {
    const spans = highlightLine('s = "a" + b', "python");
    expect(joined(spans)).toBe('s = "a" + b');
    for (const s of spans!) {
      expect(s.className.split(" ").length).toBe(1);
    }
  });

  test("declines rather than guessing when there is no language", () => {
    expect(highlightLine("const x = 1;", "")).toBeNull();
    expect(highlightLine("const x = 1;", "not-a-language")).toBeNull();
  });

  test("declines on an empty line, which has nothing to colour", () => {
    expect(highlightLine("", "typescript")).toBeNull();
  });

  test("survives a fragment that is not valid on its own", () => {
    // A diff shows one line of a larger construct, so the highlighter is
    // routinely handed something that does not parse as a whole program.
    const spans = highlightLine("  } else if (x) {", "typescript");
    expect(joined(spans)).toBe("  } else if (x) {");
  });
});
