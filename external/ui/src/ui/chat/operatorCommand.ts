/**
 * The operator_command tool: the agent asks the person at the web UI to run a
 * shell command it was refused. The server announces the request with an
 * `operator_command` SSE event, reports the run on the call's in_progress
 * frames (`_meta.coddy.operatorCommand`), and takes the answer on
 * `POST /coddy/sessions/{id}/operator-command`. This module is the model of
 * that exchange: the shapes, their parsers, the merge of a frame into the
 * call's state, the reading of a finished call's result and the POST.
 */

/** The name of the tool, as a tool row carries it. */
export const OPERATOR_COMMAND_TOOL = "operator_command";

/** The request as the `operator_command` event carries it (acp.OperatorCommandRequest). */
export type OperatorCommandRequest = {
  sessionId: string;
  requestId: string;
  toolCallId: string;
  command: string;
  reason: string;
  cwd: string;
  shell: string;
  shellPath: string;
  host: string;
  multiplexer?: string;
  attachCommand?: string;
};

export type OperatorCommandPhase = "running" | "stopping" | "stopped" | "ended";

export type OperatorCommandAction = "run" | "decline" | "stop";

/**
 * What the transcript knows of one operator command, kept on its tool_call
 * row and merged by tool call id from the event and from every frame.
 */
export type OperatorCommandState = {
  /** The request while the server waits for an answer (or after one was given). */
  request?: OperatorCommandRequest;
  requestId?: string;
  phase?: OperatorCommandPhase;
  shell?: string;
  host?: string;
  cwd?: string;
  /** The last lines of the output, as the latest frame carried them. */
  output?: string;
  droppedBytes?: number;
  exitCode?: number;
  multiplexer?: string;
  /** The answer this browser gave, kept across a reload with the request. */
  answered?: OperatorCommandAction;
};

function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

function num(v: unknown): number | undefined {
  return typeof v === "number" && Number.isFinite(v) ? v : undefined;
}

export function isOperatorCommandTool(
  title: string | undefined,
  kind: string | undefined,
): boolean {
  const name = (title || kind || "").trim().toLowerCase();
  return name === OPERATOR_COMMAND_TOOL;
}

/** The `operator_command` event's data, or null when it names no request. */
export function parseOperatorCommandRequest(
  raw: unknown,
): OperatorCommandRequest | null {
  if (!raw || typeof raw !== "object") return null;
  const o = raw as Record<string, unknown>;
  const sessionId = str(o.sessionId).trim();
  const requestId = str(o.requestId).trim();
  const toolCallId = str(o.toolCallId).trim();
  const command = str(o.command);
  if (!sessionId || !requestId || !toolCallId || !command) return null;
  const out: OperatorCommandRequest = {
    sessionId,
    requestId,
    toolCallId,
    command,
    reason: str(o.reason),
    cwd: str(o.cwd),
    shell: str(o.shell),
    shellPath: str(o.shellPath),
    host: str(o.host),
  };
  const mux = str(o.multiplexer).trim();
  if (mux) out.multiplexer = mux;
  const attach = str(o.attachCommand).trim();
  if (attach) out.attachCommand = attach;
  return out;
}

const PHASES: ReadonlySet<string> = new Set([
  "running",
  "stopping",
  "stopped",
  "ended",
]);

/** `_meta.coddy.operatorCommand` of an in_progress frame, or null. */
export function operatorCommandFromMeta(
  raw: unknown,
): OperatorCommandState | null {
  if (!raw || typeof raw !== "object") return null;
  const o = raw as Record<string, unknown>;
  const phase = str(o.phase);
  if (!PHASES.has(phase)) return null;
  const out: OperatorCommandState = {
    phase: phase as OperatorCommandPhase,
  };
  const rid = str(o.requestId).trim();
  if (rid) out.requestId = rid;
  if (typeof o.shell === "string") out.shell = o.shell;
  if (typeof o.host === "string") out.host = o.host;
  if (typeof o.cwd === "string") out.cwd = o.cwd;
  if (typeof o.output === "string") out.output = o.output;
  const dropped = num(o.droppedBytes);
  if (dropped !== undefined) out.droppedBytes = dropped;
  const code = num(o.exitCode);
  if (code !== undefined) out.exitCode = code;
  const mux = str(o.multiplexer).trim();
  if (mux) out.multiplexer = mux;
  return out;
}

/** Later facts win; a frame never takes away the request or the answer. */
export function mergeOperatorCommandState(
  cur: OperatorCommandState | undefined,
  upd: OperatorCommandState,
): OperatorCommandState {
  if (!cur) return { ...upd };
  const out: OperatorCommandState = { ...cur };
  for (const [k, v] of Object.entries(upd)) {
    if (v !== undefined) (out as Record<string, unknown>)[k] = v;
  }
  return out;
}

/** The call's arguments: `{command, reason, cwd?}`. */
export type OperatorCommandArgs = {
  command: string;
  reason: string;
  cwd: string;
};

export function parseOperatorCommandArgs(
  argsText: string | undefined,
): OperatorCommandArgs | null {
  const raw = (argsText || "").trim();
  if (!raw) return null;
  try {
    const o = JSON.parse(raw) as Record<string, unknown>;
    if (!o || typeof o !== "object") return null;
    const command = str(o.command);
    if (!command) return null;
    return { command, reason: str(o.reason), cwd: str(o.cwd) };
  } catch {
    return null;
  }
}

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

export type OperatorCommandSubmitResult =
  | { ok: true }
  | { ok: false; status: number };

const HDR = "X-Coddy-Session-ID";

/**
 * POST the operator's answer. 204 is taken; 404 means the request has ended,
 * 409 that the action does not fit its state (another surface answered first),
 * 400 a bad body. A network failure reports status 0.
 */
export async function submitOperatorCommandAction(
  sessionId: string,
  requestId: string,
  action: OperatorCommandAction,
  note?: string,
): Promise<OperatorCommandSubmitResult> {
  const sid = sessionId.trim();
  const body: Record<string, string> = {
    requestId: requestId.trim(),
    action,
  };
  if (note && note.trim()) body.note = note.trim();
  try {
    const res = await fetch(
      `/coddy/sessions/${encodeURIComponent(sid)}/operator-command`,
      {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          [HDR]: sid,
        },
        body: JSON.stringify(body),
      },
    );
    if (res.ok) return { ok: true };
    return { ok: false, status: res.status };
  } catch {
    return { ok: false, status: 0 };
  }
}
