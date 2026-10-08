import { describe, expect, test } from "vitest";
import {
  applyCommandArg,
  commandArgDraftAtCaret,
  commandReasoningChoices,
} from "./draftCommandArg";

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
      command: "/compact",
      from: 17,
      to: 17,
      prefix: "",
    });
  });

  test("filters by what is typed of the value, and replaces the whole token", () => {
    expect(at("/compact --model qw|en keep paths")).toEqual({
      open: true,
      kind: "model",
      command: "/compact",
      from: 17,
      to: 21,
      prefix: "qw",
    });
  });

  test("reads the --model=value spelling", () => {
    expect(at("/compact --model=qw|")).toEqual({
      open: true,
      kind: "model",
      command: "/compact",
      from: 17,
      to: 19,
      prefix: "qw",
    });
  });

  test("offers the option name while a dash token is typed", () => {
    expect(at("/compact --mo|")).toEqual({
      open: true,
      kind: "flag",
      command: "/compact",
      from: 9,
      to: 13,
      prefix: "--mo",
    });
  });

  test("survives leading whitespace and a newline between the words", () => {
    expect(at("  /compact\n--model\tqw|")).toMatchObject({
      open: true,
      kind: "model",
      command: "/compact",
      prefix: "qw",
    });
  });

  test.each([
    ["a bare command keeps Enter for sending it", "/compact |"],
    ["the value is complete", "/compact --model qwen |"],
    ["the instructions began", "/compact keep --model |"],
    [
      "inside the instructions after a value",
      "/compact --model qwen keep --mo|",
    ],
    ["another command", "/export --model |"],
    ["a longer command name", "/compacted --model |"],
    ["the command is not the start of the draft", "please /compact --model |"],
    ["the caret is inside the command word", "/comp|act --model x"],
    // parseCompactCommand takes a space, a tab or a line break after the
    // command and nothing else: a no-break space leaves a prompt for the model.
    ["a no-break space follows the command", "/compact\u00a0--model |"],
    // Only a `--` word is an option to parseCompactCommand: a lone dash may
    // start a Markdown list in the instructions.
    ["a single dash is typed", "/compact -|"],
    ["the instructions are a list", "/compact\n- keep the paths\n-|"],
    // `--model --model` never runs, so the value slot offers no option.
    ["an option is typed where the model goes", "/compact --model --|"],
  ])("stays closed when %s", (_name, marked) => {
    expect(at(marked)).toEqual({ open: false });
  });
});

describe("/goal options", () => {
  test("offers both of its option names", () => {
    expect(at("/goal --|")).toEqual({
      open: true,
      kind: "flag",
      command: "/goal",
      from: 6,
      to: 8,
      prefix: "--",
    });
  });

  test("completes the model, then the reasoning level of that model", () => {
    expect(at("/goal --model |")).toMatchObject({
      kind: "model",
      command: "/goal",
    });
    expect(at("/goal --model mini --reasoning h|")).toEqual({
      open: true,
      kind: "reasoning",
      command: "/goal",
      from: 31,
      to: 32,
      prefix: "h",
      model: "mini",
    });
    expect(at("/goal --model=hub/qwen --reasoning=|")).toMatchObject({
      kind: "reasoning",
      model: "hub/qwen",
      prefix: "",
    });
  });

  test("closes once the objective begins", () => {
    expect(at("/goal ship it --model |")).toEqual({ open: false });
    expect(at("/goal --reasoning high ship |")).toEqual({ open: false });
  });
});

describe("short options and /compact --reasoning", () => {
  test("-m and -r read like their long names", () => {
    expect(at("/compact -m |")).toMatchObject({ kind: "model" });
    expect(at("/compact -m qwen -r |")).toEqual({
      open: true,
      kind: "reasoning",
      command: "/compact",
      from: 20,
      to: 20,
      prefix: "",
      model: "qwen",
    });
    expect(at("/goal -m=hub/q -r=h|")).toMatchObject({
      kind: "reasoning",
      model: "hub/q",
      prefix: "h",
    });
    expect(at("/compact --|")).toMatchObject({
      kind: "flag",
      command: "/compact",
    });
  });

  test("any other dash word starts the text", () => {
    expect(at("/compact -x |")).toEqual({ open: false });
    expect(at("/goal - fix it -m |")).toEqual({ open: false });
    expect(at("/compact -m -r|")).toEqual({ open: false });
  });

  test("a command the server will refuse completes nothing past the fault", () => {
    // An option where a value goes leaves the first without one.
    expect(at("/compact -m -r |")).toEqual({ open: false });
    expect(at("/goal --reasoning --model |")).toEqual({ open: false });
    // An empty inline value is a missing one.
    expect(at("/compact -m= -r |")).toEqual({ open: false });
    expect(at("/goal --model= --reasoning |")).toEqual({ open: false });
    // An option the command does not take, which takes no value either.
    expect(at("/compact --frob -m |")).toEqual({ open: false });
    expect(at("/goal --frob --reasoning |")).toEqual({ open: false });
  });
});

describe("commandReasoningChoices", () => {
  const byModel = {
    "p/mini": ["low", "medium", "high"],
    "p/big": ["minimal", "high"],
  };

  test("the levels of the model --model names, default first", () => {
    expect(commandReasoningChoices("mini", byModel, ["x"])).toEqual([
      "default",
      "low",
      "medium",
      "high",
    ]);
    expect(commandReasoningChoices("P/BIG", byModel, [])).toEqual([
      "default",
      "minimal",
      "high",
    ]);
  });

  test("the session's levels without a model, or for an unknown one", () => {
    expect(commandReasoningChoices(undefined, byModel, ["low"])).toEqual([
      "default",
      "low",
    ]);
    expect(commandReasoningChoices("p/", byModel, ["low"])).toEqual([
      "default",
      "low",
    ]);
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
