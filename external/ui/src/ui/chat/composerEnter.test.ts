import { describe, expect, test } from "vitest";
import {
  composerEnterAction,
  insertNewline,
  type ComposerEnterKey,
} from "./composerEnter";

function key(over: Partial<ComposerEnterKey> = {}): ComposerEnterKey {
  return {
    key: "Enter",
    shiftKey: false,
    ctrlKey: false,
    altKey: false,
    metaKey: false,
    isComposing: false,
    keyCode: 13,
    repeat: false,
    ...over,
  };
}

describe("composerEnterAction with a keyboard (not a touch-only device)", () => {
  test("Enter sends", () => {
    expect(composerEnterAction(key(), false)).toBe("send");
  });

  test("Shift+Enter leaves the newline to the browser", () => {
    expect(composerEnterAction(key({ shiftKey: true }), false)).toBe("newline-native");
  });

  test("Ctrl+Enter inserts a newline, because the browser inserts none", () => {
    expect(composerEnterAction(key({ ctrlKey: true }), false)).toBe("newline-insert");
  });

  test("Alt+Enter inserts a newline like Ctrl+Enter", () => {
    expect(composerEnterAction(key({ altKey: true }), false)).toBe("newline-insert");
  });

  test("Cmd+Enter sends", () => {
    expect(composerEnterAction(key({ metaKey: true }), false)).toBe("send");
  });

  test("Enter that confirms an IME candidate does nothing", () => {
    expect(composerEnterAction(key({ isComposing: true }), false)).toBe("none");
    expect(composerEnterAction(key({ keyCode: 229 }), false)).toBe("none");
  });

  test("a held Enter does not send again", () => {
    expect(composerEnterAction(key({ repeat: true }), false)).toBe("none");
  });

  test("other keys are none of its business", () => {
    expect(composerEnterAction(key({ key: "a", keyCode: 65 }), false)).toBe("none");
    expect(composerEnterAction(key({ key: "Tab", keyCode: 9 }), false)).toBe("none");
  });
});

describe("composerEnterAction on a touch-only device", () => {
  test("Return leaves the newline to the browser: a phone keyboard has no Shift+Enter", () => {
    expect(composerEnterAction(key(), true)).toBe("newline-native");
  });

  test("an attached keyboard keeps its combinations", () => {
    expect(composerEnterAction(key({ shiftKey: true }), true)).toBe("newline-native");
    expect(composerEnterAction(key({ ctrlKey: true }), true)).toBe("newline-insert");
    expect(composerEnterAction(key({ metaKey: true }), true)).toBe("send");
  });

  test("an IME confirmation still does nothing", () => {
    expect(composerEnterAction(key({ isComposing: true }), true)).toBe("none");
  });
});

describe("insertNewline", () => {
  test("puts a newline at a collapsed caret", () => {
    expect(insertNewline("hello", 3, 3)).toEqual({ text: "hel\nlo", caret: 4 });
  });

  test("replaces a selection with the newline", () => {
    expect(insertNewline("hello", 1, 4)).toEqual({ text: "h\no", caret: 2 });
  });

  test("works at both ends", () => {
    expect(insertNewline("ab", 0, 0)).toEqual({ text: "\nab", caret: 1 });
    expect(insertNewline("ab", 2, 2)).toEqual({ text: "ab\n", caret: 3 });
  });
});
