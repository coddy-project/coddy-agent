/**
 * Option completion for a built-in command that is already typed: the
 * `--model` option of `/compact`, and `--model` and `--reasoning` of `/goal`.
 * The grammar is the one `parseCompactCommand` (`internal/agent/compact.go`)
 * and `ParseGoalCommand` (`internal/session/goal_command.go`) read: the
 * command opens the draft, options come first, and the first word that is
 * not an option starts the instructions or the objective, where nothing is
 * completed any more.
 */

/** What a completion row is: an option name, a model id or a reasoning level. */
export type CommandArgKind = "flag" | "model" | "reasoning";

export type CommandArgDraft =
  | { open: false }
  | {
      open: true;
      kind: CommandArgKind;
      /** The command the options belong to. */
      command: string;
      /** The range of the draft a picked row replaces. */
      from: number;
      to: number;
      /** What is typed of the token, up to the caret. */
      prefix: string;
      /** The value a `--model` before the caret names, when one does. */
      model?: string;
    };

const MODEL_FLAG = "--model";
const REASONING_FLAG = "--reasoning";

/** The option names each command takes, for the `flag` rows. */
export const COMMAND_FLAGS: Readonly<Record<string, readonly string[]>> = {
  "/compact": [MODEL_FLAG],
  "/goal": [MODEL_FLAG, REASONING_FLAG],
};

/** The option names `/compact` takes (kept for its callers). */
export const COMPACT_FLAGS: readonly string[] = COMMAND_FLAGS["/compact"]!;

/** What kind of value an option takes. */
const VALUE_KIND: Readonly<Record<string, CommandArgKind>> = {
  [MODEL_FLAG]: "model",
  [REASONING_FLAG]: "reasoning",
};

const isSpace = (ch: string | undefined) => ch !== undefined && /\s/.test(ch);

/** What may follow the command word: the separators the parsers cut at. */
const isCommandSeparator = (ch: string | undefined) =>
  ch !== undefined && " \t\n\r".includes(ch);

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
  const command = Object.keys(COMMAND_FLAGS).find(
    (c) => text.startsWith(c, pos) && isCommandSeparator(text[pos + c.length]),
  );
  if (!command) {
    return { open: false };
  }
  const flags = COMMAND_FLAGS[command]!;
  pos += command.length;
  if (caret <= pos) {
    return { open: false };
  }

  // The option whose value the next word is, and the --model value seen.
  let awaiting: string | null = null;
  let model: string | undefined;
  const opened = (
    kind: CommandArgKind,
    from: number,
    to: number,
  ): CommandArgDraft => ({
    open: true,
    kind,
    command,
    from,
    to,
    prefix: text.slice(from, caret),
    ...(model !== undefined && kind !== "model" ? { model } : {}),
  });
  for (;;) {
    while (pos < caret && isSpace(text[pos])) {
      pos++;
    }
    if (pos >= caret) {
      // The caret follows whitespace. Only a value an option still waits for
      // opens here: a bare `/compact ` keeps Enter for sending the command.
      return awaiting
        ? opened(VALUE_KIND[awaiting]!, caret, caret)
        : { open: false };
    }
    let end = pos;
    while (end < text.length && !isSpace(text[end])) {
      end++;
    }
    const token = text.slice(pos, end);
    if (caret <= end) {
      if (awaiting) {
        // An option where the value goes leaves the first option without a
        // value, which the server refuses: nothing to offer there.
        return token.startsWith("--")
          ? { open: false }
          : opened(VALUE_KIND[awaiting]!, pos, end);
      }
      const eq = token.indexOf("=");
      if (token.startsWith("--") && eq > 0) {
        const name = token.slice(0, eq);
        const valueStart = pos + eq + 1;
        if (flags.includes(name) && caret >= valueStart) {
          return opened(VALUE_KIND[name]!, valueStart, end);
        }
      }
      // Only a `--` word is an option; a lone dash may open a list in the
      // instructions.
      return token.startsWith("--")
        ? opened("flag", pos, end)
        : { open: false };
    }
    // A whole token before the caret.
    if (awaiting && !token.startsWith("--")) {
      if (awaiting === MODEL_FLAG) {
        model = token;
      }
      awaiting = null;
    } else if (flags.includes(token)) {
      awaiting = token;
    } else if (
      token.startsWith(`${MODEL_FLAG}=`) &&
      flags.includes(MODEL_FLAG)
    ) {
      model = token.slice(MODEL_FLAG.length + 1);
      awaiting = null;
    } else if (token.startsWith("--")) {
      awaiting = null;
    } else {
      return { open: false }; // the instructions or the objective began
    }
    pos = end;
  }
}

/**
 * The reasoning levels `/goal --reasoning` may take: those of the model the
 * draft names with `--model` (resolved like the server does - the exact id,
 * else the one id that contains it), else the session's, with `default`
 * first, which goes back to the model's own level.
 */
export function goalReasoningChoices(
  model: string | undefined,
  levelsByModel: Readonly<Record<string, readonly string[]>>,
  sessionLevels: readonly string[],
): string[] {
  let levels: readonly string[] = sessionLevels;
  const want = (model ?? "").trim().toLowerCase();
  if (want) {
    const ids = Object.keys(levelsByModel);
    const exact = ids.find((id) => id.toLowerCase() === want);
    const partial = ids.filter((id) => id.toLowerCase().includes(want));
    const id = exact ?? (partial.length === 1 ? partial[0] : undefined);
    if (id) {
      levels = levelsByModel[id] ?? [];
    }
  }
  return ["default", ...levels.filter((l) => l && l !== "default")];
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
