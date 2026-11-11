import { parseAppHash } from "../scheduler/hashRoute";

/**
 * The conversation API of an extension: the reads and writes of a chat,
 * wrapped over the routes the web UI already speaks. A read declares the
 * `sessions.read` manifest scope and a send declares `sessions.prompt`;
 * enablement stays the authority gate until the scopes are enforced.
 * Payloads come back frozen and shallow-shaped: the fields of a row are the
 * server's own, and the wrapper only adds the ones an interface needs on
 * every surface.
 */
export type SessionSummary = Readonly<{
  id: string;
  title: string;
  cwd: string;
  repoRoot: string;
  archived: boolean;
  pinned: boolean;
  origin: string;
  tags: readonly string[];
  updatedAt: string;
  turnActive: boolean;
  permissionPending: boolean;
  questionPending: boolean;
  backgroundRunning: number;
}>;
export type SessionMessage = Readonly<{
  id?: string;
  role?: string;
  type?: string;
  [key: string]: unknown;
}>;
export type SessionLiveEvent = Readonly<
  | { type: "text"; delta: string }
  | { type: "tool"; toolCallId: string; name: string; argsText: string }
  | {
      type: "tool_update";
      toolCallId: string;
      status?: string;
      resultText?: string;
    }
  | { type: "done" }
  | { type: "error"; message: string }
>;
export type SessionSendResult = Readonly<{
  /** False when the session already holds a turn (HTTP 409). */
  accepted: boolean;
  status: number;
  error: string;
}>;
export type ExtensionSessionsAPI = {
  /** The session the address is on now, or null on the start screen. */
  current: () => string | null;
  /** One page of the session listing, filtered like the History screen. */
  list: (query?: {
    archive?: "exclude" | "only" | "all";
    limit?: number;
    offset?: number;
  }) => Promise<readonly SessionSummary[]>;
  /** The listing's row of one session: state for badges and buttons. */
  state: (sessionId: string) => Promise<SessionSummary | undefined>;
  /** One page of the transcript, newest first. */
  messages: (
    sessionId: string,
    opts?: { limit?: number; before?: number },
  ) => Promise<readonly SessionMessage[]>;
  /** The tool calls of the transcript, newest first. */
  toolCalls: (
    sessionId: string,
    opts?: { limit?: number; before?: number },
  ) => Promise<readonly SessionMessage[]>;
  /** Sends a prompt into the session. Slash commands and @-mentions in the
   * text are resolved by the server; `model` and `reasoning` set the
   * session's choice. Resolves when the server has taken the message. */
  send: (req: {
    sessionId: string;
    text: string;
    files?: readonly { name: string; dataUrl: string }[];
    model?: string;
    reasoning?: string;
  }) => Promise<SessionSendResult>;
  /** Writes the listing's own fields of a session. */
  rename: (sessionId: string, title: string) => Promise<void>;
  setTags: (sessionId: string, tags: readonly string[]) => Promise<void>;
  pin: (sessionId: string, pinned: boolean) => Promise<void>;
  archive: (sessionId: string, archived: boolean) => Promise<void>;
  /** Removes the session bundle from the listing. */
  remove: (sessionId: string) => Promise<void>;
  /** Stops the running turn. */
  stop: (sessionId: string) => Promise<void>;
  /** Rewinds the session to before its last user prompt. */
  rewind: (sessionId: string) => Promise<void>;
  /** The live turn of a session, decoded: text deltas, tool calls and the
   * end of the turn. Read-only; the turn itself is the host's. */
  onMessage: (
    sessionId: string,
    listener: (event: SessionLiveEvent) => void,
  ) => { remove: () => void };
};

/** One decoded stream frame. The relay and the fresh POST answer carry the
 * same frame family the SPA consumes; this maps the few an interface draws. */
export function decodeLiveEvent(
  event: string,
  data: string,
): SessionLiveEvent[] {
  if (data === "[DONE]" || event === "done") return [{ type: "done" }];
  if (!data) return [];
  if (event === "error") {
    let message = data;
    try {
      const parsed = JSON.parse(data) as { message?: unknown; error?: unknown };
      message = String(parsed.message ?? parsed.error ?? data);
    } catch {
      /* The raw payload is the message. */
    }
    return [{ type: "error", message }];
  }
  if (event === "tool_call" || event === "tool_call_update") {
    let row: Record<string, unknown> = {};
    try {
      const parsed = JSON.parse(data) as unknown;
      if (parsed && typeof parsed === "object")
        row = parsed as Record<string, unknown>;
    } catch {
      return [];
    }
    const toolCallId = String(row.toolCallId ?? row.tool_call_id ?? "");
    const name = String(row.title ?? row.kind ?? row.name ?? "");
    if (event === "tool_call")
      return [
        {
          type: "tool",
          toolCallId,
          name,
          argsText: String(row.argsText ?? row.args ?? ""),
        },
      ];
    return [
      {
        type: "tool_update",
        toolCallId,
        ...(row.status !== undefined ? { status: String(row.status) } : {}),
        ...(row.resultText !== undefined
          ? { resultText: String(row.resultText) }
          : {}),
      },
    ];
  }
  // A text chunk: the OpenAI shape, or the relay's own `{delta}`.
  try {
    const parsed = JSON.parse(data) as {
      delta?: { content?: unknown; reasoning_content?: unknown };
      choices?: Array<{ delta?: { content?: unknown } }>;
    };
    const delta = parsed.delta ?? parsed.choices?.[0]?.delta;
    const content = delta?.content;
    if (typeof content === "string" && content)
      return [{ type: "text", delta: content }];
  } catch {
    /* Not a chunk this decoder draws. */
  }
  return [];
}

function freezeRows<T>(rows: unknown[]): readonly T[] {
  return rows.map((row) => Object.freeze({ ...(row as object) }) as T);
}

export function extensionSessions(
  signal: AbortSignal,
  opts: {
    /** The transport; tests swap it for a stub. */
    request?: (input: string, init?: RequestInit) => Promise<Response>;
  } = {},
): ExtensionSessionsAPI {
  const call = opts.request ?? fetch;
  const active = () => signal.throwIfAborted();
  const getJSON = async <T>(path: string, init?: RequestInit): Promise<T> => {
    active();
    const response = await call(path, { ...init, signal });
    if (!response.ok) throw new Error(`Session API: HTTP ${response.status}`);
    return (await response.json()) as T;
  };
  const list = async (query?: {
    archive?: "exclude" | "only" | "all";
    limit?: number;
    offset?: number;
  }) => {
    const params = new URLSearchParams();
    if (query?.archive) params.set("archive", query.archive);
    if (query?.limit !== undefined) params.set("limit", String(query.limit));
    if (query?.offset !== undefined) params.set("offset", String(query.offset));
    const suffix = params.toString() ? `?${params}` : "";
    const body = await getJSON<{ items?: unknown[] }>(
      `/coddy/sessions${suffix}`,
    );
    return freezeRows<SessionSummary>(body.items ?? []);
  };
  const patch = async (sessionId: string, body: Record<string, unknown>) => {
    active();
    const response = await call(
      `/coddy/sessions/${encodeURIComponent(sessionId)}`,
      {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
        signal,
      },
    );
    if (!response.ok) throw new Error(`Session API: HTTP ${response.status}`);
  };
  return {
    current() {
      const route = parseAppHash();
      return route.branch === "session" ? route.sessionId : null;
    },
    list,
    async state(sessionId) {
      const rows = await list();
      return rows.find((row) => row.id === sessionId);
    },
    async messages(sessionId, opts2) {
      const params = new URLSearchParams();
      if (opts2?.limit !== undefined) params.set("limit", String(opts2.limit));
      if (opts2?.before !== undefined)
        params.set("before", String(opts2.before));
      const suffix = params.toString() ? `?${params}` : "";
      const body = await getJSON<{ messages?: unknown[] }>(
        `/coddy/sessions/${encodeURIComponent(sessionId)}/messages${suffix}`,
      );
      return freezeRows<SessionMessage>(body.messages ?? []);
    },
    async toolCalls(sessionId, opts2) {
      const params = new URLSearchParams();
      if (opts2?.limit !== undefined) params.set("limit", String(opts2.limit));
      if (opts2?.before !== undefined)
        params.set("before", String(opts2.before));
      const suffix = params.toString() ? `?${params}` : "";
      const body = await getJSON<{ items?: unknown[]; toolCalls?: unknown[] }>(
        `/coddy/sessions/${encodeURIComponent(sessionId)}/tool-calls${suffix}`,
      );
      return freezeRows<SessionMessage>(body.items ?? body.toolCalls ?? []);
    },
    async send(req) {
      active();
      const metadata: Record<string, unknown> = {};
      if (req.model) metadata.model = req.model;
      if (req.reasoning) metadata.reasoning = req.reasoning;
      const body = {
        input: req.text,
        ...(req.files?.length ? { inline_files: [...req.files] } : {}),
        ...(Object.keys(metadata).length ? { metadata } : {}),
      };
      const response = await call("/v1/responses", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-Coddy-Session-ID": req.sessionId,
        },
        body: JSON.stringify(body),
        signal,
      });
      if (response.ok)
        return Object.freeze({
          accepted: true,
          status: response.status,
          error: "",
        });
      let error = `HTTP ${response.status}`;
      try {
        const parsed = (await response.json()) as {
          error?: { message?: string };
        };
        if (parsed.error?.message) error = parsed.error.message;
      } catch {
        /* Status is enough for non-JSON errors. */
      }
      return Object.freeze({ accepted: false, status: response.status, error });
    },
    async rename(sessionId, title) {
      await patch(sessionId, { title });
    },
    async setTags(sessionId, tags) {
      await patch(sessionId, { tags: [...tags] });
    },
    async pin(sessionId, pinned) {
      await patch(sessionId, { pinned });
    },
    async archive(sessionId, archived) {
      await patch(sessionId, { archived });
    },
    async remove(sessionId) {
      active();
      const response = await call(
        `/coddy/sessions/${encodeURIComponent(sessionId)}`,
        { method: "DELETE", signal },
      );
      if (!response.ok) throw new Error(`Session API: HTTP ${response.status}`);
    },
    async stop(sessionId) {
      active();
      const response = await call(
        `/coddy/sessions/${encodeURIComponent(sessionId)}/cancel`,
        { method: "POST", signal },
      );
      if (!response.ok) throw new Error(`Session API: HTTP ${response.status}`);
    },
    async rewind(sessionId) {
      active();
      const response = await call(
        `/coddy/sessions/${encodeURIComponent(sessionId)}/rewind`,
        { method: "POST", signal },
      );
      if (!response.ok) throw new Error(`Session API: HTTP ${response.status}`);
    },
    onMessage(sessionId, listener) {
      const controller = new AbortController();
      const dropped = () => {
        controller.abort();
        signal.removeEventListener("abort", dropped);
      };
      signal.addEventListener("abort", dropped, { once: true });
      void (async () => {
        try {
          const response = await call(
            `/coddy/sessions/${encodeURIComponent(sessionId)}/composer-stream`,
            { signal: controller.signal },
          );
          if (!response.ok || !response.body) return;
          const reader = response.body.getReader();
          const decoder = new TextDecoder();
          let buffer = "";
          for (;;) {
            const step = await reader.read();
            if (step.done) break;
            buffer += decoder.decode(step.value, { stream: true });
            for (const frame of splitFrames(
              () => buffer,
              (rest) => (buffer = rest),
            )) {
              for (const event of decodeLiveEvent(frame.event, frame.data))
                listener(event);
            }
          }
        } catch {
          /* A dropped stream ends the subscription quietly. */
        }
      })();
      return { remove: dropped };
    },
  };
}

/** Yields the complete SSE frames of a growing buffer and leaves the tail. */
export function* splitFrames(
  read: () => string,
  keep: (rest: string) => void,
): Generator<{ event: string; data: string }> {
  let rest = read();
  for (;;) {
    const end = rest.indexOf("\n\n");
    if (end < 0) break;
    const block = rest.slice(0, end);
    rest = rest.slice(end + 2);
    let event = "";
    const lines: string[] = [];
    for (const line of block.split("\n")) {
      if (line.startsWith("event:")) event = line.slice(6).trim();
      else if (line.startsWith("data:")) lines.push(line.slice(5).trimStart());
    }
    if (lines.length) yield { event, data: lines.join("\n") };
  }
  keep(rest);
}
