/**
 * The background-task API of an extension: the pool the agent's work runs
 * in - shell commands, subagent runs, memory runs - read through the routes
 * the Tasks panel reads, and stopped through the route it stops with. Rows
 * come back frozen with the server's own fields, so an extension draws a
 * task list or watches a child session's run without parsing the stream.
 */
export type BackgroundTaskRow = Readonly<
  Record<string, unknown> & { id: string }
>;
export type BackgroundTaskList = Readonly<{
  sessionId: string;
  running: number;
  tasks: readonly BackgroundTaskRow[];
}>;
export type ExtensionTasksAPI = {
  /** The session's tasks, finished ones included until they are cleared. */
  list: (sessionId: string) => Promise<BackgroundTaskList>;
  /** One task by id. */
  get: (
    sessionId: string,
    taskId: string,
  ) => Promise<BackgroundTaskRow | undefined>;
  /** Stops a running task. */
  stop: (sessionId: string, taskId: string) => Promise<void>;
  /** Clears the finished tasks of the listing. */
  clearFinished: (sessionId: string) => Promise<number>;
};

export function extensionTasks(
  signal: AbortSignal,
  opts: {
    request?: (input: string, init?: RequestInit) => Promise<Response>;
  } = {},
): ExtensionTasksAPI {
  const call = opts.request ?? fetch;
  const active = () => signal.throwIfAborted();
  const base = (sessionId: string) =>
    `/coddy/sessions/${encodeURIComponent(sessionId)}/background-tasks`;
  const getJSON = async <T>(path: string, init?: RequestInit): Promise<T> => {
    active();
    const response = await call(path, {
      ...init,
      headers: { "X-Coddy-Session-ID": sessionIdOf(path) },
      signal,
    });
    if (!response.ok) throw new Error(`Tasks API: HTTP ${response.status}`);
    return (await response.json()) as T;
  };
  const sessionIdOf = (path: string) =>
    decodeURIComponent(
      /^\/coddy\/sessions\/([^/]+)\/background-tasks/.exec(path)?.[1] ?? "",
    );
  return {
    async list(sessionId) {
      const body = await getJSON<{
        sessionId?: string;
        running?: number;
        data?: unknown[];
      }>(base(sessionId));
      return Object.freeze({
        sessionId: body.sessionId ?? sessionId,
        running: body.running ?? 0,
        tasks: Object.freeze(
          (body.data ?? []).map(
            (row) => Object.freeze({ ...(row as object) }) as BackgroundTaskRow,
          ),
        ),
      });
    },
    async get(sessionId, taskId) {
      let body: { task?: unknown; output?: unknown } | undefined;
      try {
        body = await getJSON<{ task?: unknown; output?: unknown }>(
          `${base(sessionId)}/${encodeURIComponent(taskId)}`,
        );
      } catch (error) {
        if (error instanceof Error && /HTTP 404/.test(error.message))
          return undefined;
        throw error;
      }
      if (!body?.task || typeof body.task !== "object") return undefined;
      return Object.freeze({
        ...(body.task as Record<string, unknown>),
        output: typeof body.output === "string" ? body.output : "",
      }) as unknown as BackgroundTaskRow;
    },
    async stop(sessionId, taskId) {
      active();
      const response = await call(
        `${base(sessionId)}/${encodeURIComponent(taskId)}/stop`,
        {
          method: "POST",
          headers: { "X-Coddy-Session-ID": sessionId },
          signal,
        },
      );
      if (!response.ok) throw new Error(`Tasks API: HTTP ${response.status}`);
    },
    async clearFinished(sessionId) {
      active();
      const response = await call(base(sessionId), {
        method: "DELETE",
        headers: { "X-Coddy-Session-ID": sessionId },
        signal,
      });
      if (!response.ok) throw new Error(`Tasks API: HTTP ${response.status}`);
      const body = (await response.json()) as { cleared?: number };
      return body.cleared ?? 0;
    },
  };
}
