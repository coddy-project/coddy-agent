import { describe, expect, test } from "vitest";
import {
  highlightLine,
  highlightLines,
  isHighlightable,
} from "./highlightLine";

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

  // highlight.js gives a sub-scoped token two classes ("hljs-title class_");
  // the stylesheet colours the first, so that is the one a span keeps.
  test("a sub-scoped token keeps its highlight.js class", () => {
    const spans = highlightLine(
      "class Foo { run() { return this.baz(); } }",
      "javascript",
    )!;
    const classOf = (text: string) =>
      spans.find((s) => s.text === text)?.className;
    expect(classOf("Foo")).toBe("hljs-title");
    expect(classOf("run")).toBe("hljs-title");
    expect(classOf("this")).toBe("hljs-variable");
  });

  test("survives a fragment that is not valid on its own", () => {
    // A diff shows one line of a larger construct, so the highlighter is
    // routinely handed something that does not parse as a whole program.
    const spans = highlightLine("  } else if (x) {", "typescript");
    expect(joined(spans)).toBe("  } else if (x) {");
  });
});

describe("highlightLines", () => {
  function text(spans: { text: string }[] | null | undefined): string {
    return (spans ?? []).map((s) => s.text).join("");
  }

  // An open file is coloured as one text, so a construct that spans lines
  // keeps its colour on every line it covers, not only the first.
  test("colours a block comment on every line it spans", () => {
    const lines = ["/* first", "   second */", "const x = 1;"];
    const out = highlightLines(lines, "typescript");
    expect(out).toHaveLength(3);
    expect(out[1]!.every((s) => s.className === "hljs-comment")).toBe(true);
    expect(out[2]!.some((s) => s.className === "hljs-keyword")).toBe(true);
  });

  test("keeps a string that spans lines a string", () => {
    const lines = ['s = """first', "inside the docstring", '"""', "x = 1"];
    const out = highlightLines(lines, "python");
    expect(out[1]!.every((s) => s.className === "hljs-string")).toBe(true);
    expect(out[3]!.some((s) => s.className === "hljs-number")).toBe(true);
  });

  test("reproduces every line exactly, empty and indented ones included", () => {
    const lines = ["func main() {", "", "\treturn", "}", "  "];
    const out = highlightLines(lines, "go");
    expect(out.map(text)).toEqual(lines);
  });

  // A minified line can stall the highlighter; it stays plain and the lines
  // around it are still coloured.
  test("leaves a line too long to colour plain and colours the rest", () => {
    const long = "x".repeat(5000);
    const lines = ["/* a", "b */", long, "/* c", "d */"];
    const out = highlightLines(lines, "typescript");
    expect(out[2]).toBeNull();
    expect(out[1]!.every((s) => s.className === "hljs-comment")).toBe(true);
    expect(out[4]!.every((s) => s.className === "hljs-comment")).toBe(true);
  });

  test("declines for a language it does not know", () => {
    expect(highlightLines(["a", "b"], "")).toEqual([null, null]);
    expect(highlightLines(["a"], "not-a-language")).toEqual([null]);
  });
});

describe("the grammars of the chat", () => {
  // The Files and edits windows colour with the same grammars the chat's code
  // blocks do, the vendored ones and the extra ones included.
  test.each([
    ["dart", "void main() { print('hi'); }"],
    ["elixir", "defmodule Demo do\nend"],
    ["haskell", 'main = putStrLn "hi"'],
    ["dockerfile", "FROM alpine:3.20"],
    ["groovy", "def answer = 42"],
    ["protobuf", "message Demo { string name = 1; }"],
    ["cmake", "cmake_minimum_required(VERSION 3.20)"],
    ["dos", "@echo off"],
    ["gdscript", "extends Node"],
  ])("colours %s", (language, source) => {
    expect(isHighlightable(language)).toBe(true);
    expect(highlightLines(source.split("\n"), language)[0]).not.toBeNull();
  });
});
