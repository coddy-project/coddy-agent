import { describe, expect, test } from "vitest";
import { applyCommandArg, commandArgDraftAtCaret } from "./draftCommandArg";

/** The draft with `|` marking the caret. */
function at(marked: string) {
  const caret = marked.indexOf("|");
  return commandArgDraftAtCaret(marked.replace("|", ""), caret);
}

describe("commandArgDraftAtCaret", () => {
  test("opens the model list right after --model", () => {
    expect(at("/compact --model |")).toEqual({
      open: true,
      kind: "model",
      from: 17,
      to: 17,
      prefix: "",
    });
  });

  test("filters by what is typed of the value, and replaces the whole token", () => {
    expect(at("/compact --model qw|en keep paths")).toEqual({
      open: true,
      kind: "model",
      from: 17,
      to: 21,
      prefix: "qw",
    });
  });

  test("reads the --model=value spelling", () => {
    expect(at("/compact --model=qw|")).toEqual({
      open: true,
      kind: "model",
      from: 17,
      to: 19,
      prefix: "qw",
    });
  });

  test("offers the option name while a dash token is typed", () => {
    expect(at("/compact --mo|")).toEqual({
      open: true,
      kind: "flag",
      from: 9,
      to: 13,
      prefix: "--mo",
    });
  });

  test("survives leading whitespace and a newline between the words", () => {
    expect(at("  /compact\n--model\tqw|")).toMatchObject({
      open: true,
      kind: "model",
      prefix: "qw",
    });
  });

  test.each([
    ["a bare command keeps Enter for sending it", "/compact |"],
    ["the value is complete", "/compact --model qwen |"],
    ["the instructions began", "/compact keep --model |"],
    ["inside the instructions after a value", "/compact --model qwen keep --mo|"],
    ["another command", "/export --model |"],
    ["a longer command name", "/compacted --model |"],
    ["the command is not the start of the draft", "please /compact --model |"],
    ["the caret is inside the command word", "/comp|act --model x"],
  ])("stays closed when %s", (_name, marked) => {
    expect(at(marked)).toEqual({ open: false });
  });
});

describe("applyCommandArg", () => {
  test("puts a space after the value and the caret behind it", () => {
    expect(applyCommandArg("/compact --model qw", 17, 19, "hub/qwen3")).toEqual(
      { next: "/compact --model hub/qwen3 ", pos: 27 },
    );
  });

  test("does not double the space that already follows", () => {
    expect(
      applyCommandArg("/compact --model qw keep paths", 17, 19, "hub/qwen3"),
    ).toEqual({ next: "/compact --model hub/qwen3 keep paths", pos: 27 });
  });
});
