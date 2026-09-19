/**
 * What Enter does in the composer. The input device decides, not the
 * viewport width: a narrow desktop window still has a keyboard.
 */
export type ComposerEnterAction =
  | "none"
  | "send"
  | "newline-native"
  | "newline-insert";

/** The fields of a keydown the rule reads. */
export interface ComposerEnterKey {
  key: string;
  shiftKey: boolean;
  ctrlKey: boolean;
  altKey: boolean;
  metaKey: boolean;
  isComposing: boolean;
  keyCode: number;
  repeat: boolean;
}

/**
 * `touchOnly` is true on a device with no hovering pointer and a coarse one
 * (a phone): its keyboard has no Shift+Enter, so Return has to stay a newline
 * and sending is the button.
 */
export function composerEnterAction(
  ev: ComposerEnterKey,
  touchOnly: boolean,
): ComposerEnterAction {
  if (ev.key !== "Enter") return "none";
  // An IME confirming a candidate reports Enter too (keyCode 229 in Safari).
  if (ev.isComposing || ev.keyCode === 229) return "none";
  if (ev.shiftKey) return "newline-native";
  // Browsers insert nothing for Ctrl+Enter or Alt+Enter in a textarea.
  if (ev.ctrlKey || ev.altKey) return "newline-insert";
  if (touchOnly && !ev.metaKey) return "newline-native";
  if (ev.repeat) return "none";
  return "send";
}

/** The draft with a newline in place of the selection, and the caret after it. */
export function insertNewline(
  value: string,
  start: number,
  end: number,
): { text: string; caret: number } {
  return { text: value.slice(0, start) + "\n" + value.slice(end), caret: start + 1 };
}
