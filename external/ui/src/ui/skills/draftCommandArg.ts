/**
 * Option completion for a built-in command that is already typed: today the
 * `--model` option of `/compact`. The grammar is the one `parseCompactCommand`
 * reads in `internal/agent/compact.go`: the command opens the draft, options
 * come first, and the first word that is not an option starts the summarizer
 * instructions, where nothing is completed any more.
 */

export type CommandArgDraft =
  | { open: false }
  | {
      open: true;
      /** `flag` completes an option name, `model` the value of `--model`. */
      kind: "flag" | "model";
      /** The range of the draft a picked row replaces. */
      from: number;
      to: number;
      /** What is typed of the token, up to the caret. */
      prefix: string;
    };

const COMMAND = "/compact";
const MODEL_FLAG = "--model";

/** The option names `/compact` takes, for the `flag` rows. */
export const COMPACT_FLAGS: readonly string[] = [MODEL_FLAG];

const isSpace = (ch: string | undefined) => ch !== undefined && /\s/.test(ch);

export function commandArgDraftAtCaret(
  text: string,
  caret: number,
): CommandArgDraft {
  if (caret < 0 || caret > text.length) {
    return { open: false };
  }
  let pos = 0;
  while (isSpace(text[pos])) {
    pos++;
  }
  if (!text.startsWith(COMMAND, pos) || !isSpace(text[pos + COMMAND.length])) {
    return { open: false };
  }
  pos += COMMAND.length;
  if (caret <= pos) {
    return { open: false };
  }

  let awaitingModel = false;
  for (;;) {
    while (pos < caret && isSpace(text[pos])) {
      pos++;
    }
    if (pos >= caret) {
      // The caret follows whitespace. Only a value `--model` still waits for
      // opens here: a bare `/compact ` keeps Enter for sending the command.
      return awaitingModel
        ? { open: true, kind: "model", from: caret, to: caret, prefix: "" }
        : { open: false };
    }
    let end = pos;
    while (end < text.length && !isSpace(text[end])) {
      end++;
    }
    const token = text.slice(pos, end);
    if (caret <= end) {
      if (awaitingModel && !token.startsWith("--")) {
        return {
          open: true,
          kind: "model",
          from: pos,
          to: end,
          prefix: text.slice(pos, caret),
        };
      }
      const valueStart = pos + MODEL_FLAG.length + 1;
      if (token.startsWith(`${MODEL_FLAG}=`) && caret >= valueStart) {
        return {
          open: true,
          kind: "model",
          from: valueStart,
          to: end,
          prefix: text.slice(valueStart, caret),
        };
      }
      if (token.startsWith("-")) {
        return {
          open: true,
          kind: "flag",
          from: pos,
          to: end,
          prefix: text.slice(pos, caret),
        };
      }
      return { open: false };
    }
    // A whole token before the caret.
    if (awaitingModel && !token.startsWith("--")) {
      awaitingModel = false;
    } else if (token === MODEL_FLAG) {
      awaitingModel = true;
    } else if (token.startsWith("--")) {
      awaitingModel = false;
    } else {
      return { open: false }; // the instructions began
    }
    pos = end;
  }
}

/**
 * The draft after a row is picked, and where the caret lands: the value gets a
 * space after it unless one is already there.
 */
export function applyCommandArg(
  text: string,
  from: number,
  to: number,
  value: string,
): { next: string; pos: number } {
  const tail = text.slice(to);
  const gap = isSpace(tail[0]) ? "" : " ";
  const next = text.slice(0, from) + value + gap + tail;
  return { next, pos: from + value.length + 1 };
}
