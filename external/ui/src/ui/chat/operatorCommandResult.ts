// Reading the result of a finished operator_command call: only the card does,
// so it lives apart from operatorCommand.ts and loads with the card's chunk.

/** How a finished call ended, read off the result text the tool wrote. */
export type OperatorCommandOutcome =
  | {
      kind: "ran";
      exitCode: number;
      output: string;
      shell: string;
      host: string;
      cwd: string;
    }
  | {
      kind: "stopped";
      exitCode: number;
      output: string;
      shell: string;
      host: string;
      cwd: string;
    }
  | { kind: "declined"; note: string }
  | { kind: "other"; text: string };

// The headers internal/tools/shell/operator_tool.go writes: where the command
// ran comes first, so a card rebuilt from the history still names it.
const RAN_RE =
  /^The operator ran the command in (\S+) on (.+?), in ([^\n]*) \(exit code (-?\d+)\):\n?/;
const STOPPED_RE =
  /^The operator stopped the command in (\S+) on (.+?), in ([^\n]*) \(exit code (-?\d+)\)\. Output so far:\n?/;
const DECLINED_PREFIX = "The operator declined to run the command.";
const NOTE_PREFIX = " Their note: ";

/**
 * The tool's own sentences (internal/tools/shell/operator_tool.go,
 * operatorResult): the head says how it ended, the rest is the output.
 */
export function parseOperatorCommandResult(
  resultText: string | undefined,
): OperatorCommandOutcome | null {
  const text = resultText || "";
  if (!text.trim()) return null;
  const ran = RAN_RE.exec(text);
  if (ran) {
    return {
      kind: "ran",
      shell: ran[1] ?? "",
      host: ran[2] ?? "",
      cwd: ran[3] ?? "",
      exitCode: Number(ran[4]),
      output: text.slice(ran[0].length),
    };
  }
  const stopped = STOPPED_RE.exec(text);
  if (stopped) {
    return {
      kind: "stopped",
      shell: stopped[1] ?? "",
      host: stopped[2] ?? "",
      cwd: stopped[3] ?? "",
      exitCode: Number(stopped[4]),
      output: text.slice(stopped[0].length),
    };
  }
  if (text.startsWith(DECLINED_PREFIX)) {
    const rest = text.slice(DECLINED_PREFIX.length);
    return {
      kind: "declined",
      note: rest.startsWith(NOTE_PREFIX)
        ? rest.slice(NOTE_PREFIX.length).trim()
        : "",
    };
  }
  return { kind: "other", text };
}
