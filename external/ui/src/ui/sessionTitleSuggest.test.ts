import { afterEach, expect, test, vi } from "vitest";
import { startSuggestSessionTitle } from "./sessionTitleSuggest";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

test("describe is requested before session id promise resolves", async () => {
  let releaseSid!: (id: string) => void;
  const sidPromise = new Promise<string>((resolve) => {
    releaseSid = resolve;
  });

  const order: string[] = [];

  const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input.toString();
    if (url.includes("/coddy/describe")) {
      order.push("describe");
      return new Response(
        JSON.stringify({ object: "coddy.describe", short: "My title" }),
        {
          status: 200,
          headers: { "Content-Type": "application/json" },
        },
      );
    }
    if (url.includes("/coddy/sessions/sess_x") && !url.includes("/messages")) {
      order.push("patch");
      return new Response(JSON.stringify({ object: "coddy.session_patched" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }
    return new Response("not found", { status: 404 });
  });

  startSuggestSessionTitle({
    userText: "please explain async rust patterns in detail",
    sessionIdPromise: sidPromise,
    fetchImpl: fetchImpl as unknown as typeof fetch,
  });

  await vi.waitFor(() => {
    expect(order).toContain("describe");
  });
  expect(order).not.toContain("patch");

  releaseSid("sess_x");

  await vi.waitFor(() => {
    expect(order).toEqual(["describe", "patch"]);
  });
});

test("onShortReady runs after describe and before PATCH resolves", async () => {
  let releaseSid!: (id: string) => void;
  const sidPromise = new Promise<string>((resolve) => {
    releaseSid = resolve;
  });

  const order: string[] = [];
  const preview: Array<{ sid: string; title: string }> = [];

  const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input.toString();
    if (url.includes("/coddy/describe")) {
      order.push("describe");
      return new Response(JSON.stringify({ short: "Fast title" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }
    if (url.includes("/coddy/sessions/sess_x") && !url.includes("/messages")) {
      order.push("patch");
      return new Response(JSON.stringify({ object: "coddy.session_patched" }), {
        status: 200,
      });
    }
    return new Response("not found", { status: 404 });
  });

  startSuggestSessionTitle({
    userText: "four word message here",
    sessionIdPromise: sidPromise,
    getPreviewSessionId: () => "sess_x",
    onShortReady: (sid, title) => {
      order.push("short_ready");
      preview.push({ sid, title });
    },
    fetchImpl: fetchImpl as unknown as typeof fetch,
  });

  await vi.waitFor(() => {
    expect(order).toEqual(["describe", "short_ready"]);
  });
  expect(order).not.toContain("patch");

  releaseSid("sess_x");

  await vi.waitFor(() => {
    expect(order).toEqual(["describe", "short_ready", "patch"]);
  });
  expect(preview).toEqual([{ sid: "sess_x", title: "Fast title" }]);
});

test("retries PATCH when session returns 404 until ok", async () => {
  let patchAttempts = 0;
  const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input.toString();
    if (url.includes("/coddy/describe")) {
      return new Response(JSON.stringify({ short: "T" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }
    if (url.includes("/coddy/sessions/sid1")) {
      patchAttempts++;
      if (patchAttempts < 3) {
        return new Response("missing", { status: 404 });
      }
      return new Response(JSON.stringify({ object: "coddy.session_patched" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }
    return new Response("no", { status: 404 });
  });

  const applied: Array<{ sid: string; title: string }> = [];

  startSuggestSessionTitle({
    userText: "one two three four",
    sessionIdPromise: Promise.resolve("sid1"),
    fetchImpl: fetchImpl as unknown as typeof fetch,
    onApplied: (sid, title) => applied.push({ sid, title }),
  });

  await vi.waitFor(() => {
    expect(applied).toEqual([{ sid: "sid1", title: "T" }]);
  });
});

test("the tags describe proposed are filed with the title in one PATCH", async () => {
  const bodies: string[] = [];
  const fetchImpl = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === "string" ? input : input.toString();
      if (url.includes("/coddy/describe")) {
        return new Response(
          JSON.stringify({
            object: "coddy.describe",
            short: "Refactor the memory API",
            tags: ["backend", "memory"],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        );
      }
      bodies.push(String(init?.body ?? ""));
      return new Response(JSON.stringify({ object: "coddy.session_patched" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    },
  );

  startSuggestSessionTitle({
    userText: "please refactor the memory tree endpoint and add tests",
    sessionIdPromise: Promise.resolve("sess_x"),
    fetchImpl: fetchImpl as unknown as typeof fetch,
  });

  await vi.waitFor(() => expect(bodies).toHaveLength(1));
  expect(JSON.parse(String(bodies[0]))).toEqual({
    title: "Refactor the memory API",
    titleIfUnpinned: true,
    tags: ["backend", "memory"],
  });
});

test("a model that proposed no tags patches the title alone", async () => {
  const bodies: string[] = [];
  const fetchImpl = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === "string" ? input : input.toString();
      if (url.includes("/coddy/describe")) {
        return new Response(
          JSON.stringify({
            object: "coddy.describe",
            short: "My title",
            tags: [],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        );
      }
      bodies.push(String(init?.body ?? ""));
      return new Response(JSON.stringify({ object: "coddy.session_patched" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    },
  );

  startSuggestSessionTitle({
    userText: "please explain async rust patterns in detail",
    sessionIdPromise: Promise.resolve("sess_x"),
    fetchImpl: fetchImpl as unknown as typeof fetch,
  });

  await vi.waitFor(() => expect(bodies).toHaveLength(1));
  // An empty array would clear tags the operator may have set by hand; the
  // field stays out of the body instead.
  // titleIfUnpinned keeps this a suggestion: a session named during the first
  // turn keeps its name and this answer only files it.
  expect(JSON.parse(String(bodies[0]))).toEqual({
    title: "My title",
    titleIfUnpinned: true,
  });
});

// Issue #435: a first message that invokes a slash command is named by what
// the command does, and the command is the one of the chat's own workspace.
// The new chat's session may not exist on the server yet, so the folder rides
// as the cwd query next to the session header.
test("describe names the workspace of the new chat", async () => {
  const calls: Array<{ url: string; headers: Record<string, string> }> = [];
  const fetchImpl = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === "string" ? input : input.toString();
      calls.push({
        url,
        headers: (init?.headers ?? {}) as Record<string, string>,
      });
      return new Response(JSON.stringify({ short: "" }), { status: 200 });
    },
  );

  startSuggestSessionTitle({
    userText: "/rpa-init",
    sessionIdPromise: Promise.resolve("sess_x"),
    scope: {
      headers: { "X-Coddy-Session-ID": "sess_x" },
      cwd: "/work/папка",
    },
    fetchImpl: fetchImpl as unknown as typeof fetch,
  });

  await vi.waitFor(() => {
    expect(calls.length).toBe(1);
  });
  const [call] = calls;
  expect(call?.url).toBe(
    `/coddy/describe?cwd=${encodeURIComponent("/work/папка")}`,
  );
  expect(call?.headers["X-Coddy-Session-ID"]).toBe("sess_x");
  expect(call?.headers["Content-Type"]).toBe("application/json");
});

// A text that names nothing (settings commands alone) comes back with an
// empty short: the chat keeps its name and nothing is patched.
test("an empty short patches nothing", async () => {
  const urls: string[] = [];
  const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input.toString();
    urls.push(url);
    return new Response(JSON.stringify({ short: "", tags: [] }), {
      status: 200,
    });
  });
  const onShortReady = vi.fn();

  startSuggestSessionTitle({
    userText: "/plan",
    sessionIdPromise: Promise.resolve("sess_x"),
    getPreviewSessionId: () => "sess_x",
    onShortReady,
    fetchImpl: fetchImpl as unknown as typeof fetch,
  });

  await vi.waitFor(() => {
    expect(urls.length).toBe(1);
  });
  await new Promise((resolve) => setTimeout(resolve, 20));
  expect(urls).toEqual(["/coddy/describe"]);
  expect(onShortReady).not.toHaveBeenCalled();
});

// While describe is in flight the chat header and its History row show a
// placeholder instead of a name (issue #435). The placeholder needs one signal
// that the naming is over, whatever its outcome.
test("describe settling is reported after the short is ready", async () => {
  const order: string[] = [];
  const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input.toString();
    if (url.includes("/coddy/describe")) {
      return new Response(JSON.stringify({ short: "Named" }), { status: 200 });
    }
    return new Response("{}", { status: 200 });
  });
  startSuggestSessionTitle({
    userText: "/rpa-init",
    sessionIdPromise: Promise.resolve("sess_x"),
    getPreviewSessionId: () => "sess_x",
    onShortReady: () => order.push("short"),
    onDescribeSettled: () => order.push("settled"),
    fetchImpl: fetchImpl as unknown as typeof fetch,
  });
  await vi.waitFor(() => {
    expect(order).toEqual(["short", "settled"]);
  });
});

test.each([
  [
    "an empty short",
    () => new Response(JSON.stringify({ short: "" }), { status: 200 }),
  ],
  ["a refusal", () => new Response("{}", { status: 503 })],
  [
    "a network failure",
    () => {
      throw new TypeError("network");
    },
  ],
])("describe settling is reported after %s", async (_name, answer) => {
  const settled = vi.fn();
  const onShortReady = vi.fn();
  const fetchImpl = vi.fn(async () => answer());
  startSuggestSessionTitle({
    userText: "/plan",
    sessionIdPromise: Promise.resolve("sess_x"),
    getPreviewSessionId: () => "sess_x",
    onShortReady,
    onDescribeSettled: settled,
    fetchImpl: fetchImpl as unknown as typeof fetch,
  });
  await vi.waitFor(() => {
    expect(settled).toHaveBeenCalledTimes(1);
  });
  expect(onShortReady).not.toHaveBeenCalled();
});

// A model slow to answer must not leave the placeholder shimmering for the
// life of the page: it gives way after a bound, and a name that arrives
// later still names the chat.
test("the placeholder gives way after its bound and a late name still lands", async () => {
  const order: string[] = [];
  let answer!: (res: Response) => void;
  const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input.toString();
    if (url.includes("/coddy/describe")) {
      return new Promise<Response>((resolve) => {
        answer = resolve;
      });
    }
    order.push("patch");
    return new Response("{}", { status: 200 });
  });
  startSuggestSessionTitle({
    userText: "/rpa-init",
    sessionIdPromise: Promise.resolve("sess_x"),
    getPreviewSessionId: () => "sess_x",
    placeholderTimeoutMs: 20,
    onShortReady: () => order.push("short"),
    onDescribeSettled: () => order.push("settled"),
    fetchImpl: fetchImpl as unknown as typeof fetch,
  });
  await vi.waitFor(() => {
    expect(order).toEqual(["settled"]);
  });
  answer(new Response(JSON.stringify({ short: "Late name" }), { status: 200 }));
  await vi.waitFor(() => {
    expect(order).toEqual(["settled", "short", "patch"]);
  });
});

// A first send of attachments alone has no text to name the chat by: nothing
// is asked, and the placeholder that was put up for it gives way at once.
test("a send without text settles the naming at once", async () => {
  const settled = vi.fn();
  const fetchImpl = vi.fn();
  startSuggestSessionTitle({
    userText: "   ",
    sessionIdPromise: Promise.resolve("sess_x"),
    onDescribeSettled: settled,
    fetchImpl: fetchImpl as unknown as typeof fetch,
  });
  await vi.waitFor(() => {
    expect(settled).toHaveBeenCalledTimes(1);
  });
  expect(fetchImpl).not.toHaveBeenCalled();
});

// The first send was not admitted, so the chat will never exist: a name that
// comes afterwards is neither shown nor sent, the describe request is
// aborted, and the placeholder goes at once.
test("a cancelled naming reports and patches nothing", async () => {
  let answerDescribe!: () => void;
  let describeSignal: AbortSignal | undefined;
  const patches: string[] = [];
  const fetchImpl = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/coddy/describe")) {
        describeSignal = init?.signal ?? undefined;
        await new Promise<void>((resolve) => {
          answerDescribe = resolve;
        });
        return new Response(JSON.stringify({ short: "Too late" }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      patches.push(url);
      return new Response("not found", { status: 404 });
    },
  );
  const onShortReady = vi.fn();
  const onApplied = vi.fn();
  const onDescribeSettled = vi.fn();

  const naming = startSuggestSessionTitle({
    userText: "/rpa-init",
    sessionIdPromise: Promise.resolve("sess_gone"),
    getPreviewSessionId: () => "sess_gone",
    fetchImpl: fetchImpl as unknown as typeof fetch,
    onShortReady,
    onApplied,
    onDescribeSettled,
  });
  await vi.waitFor(() => expect(describeSignal).toBeDefined());

  naming.cancel();
  expect(describeSignal?.aborted).toBe(true);
  expect(onDescribeSettled).toHaveBeenCalledTimes(1);

  answerDescribe();
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(onShortReady).not.toHaveBeenCalled();
  expect(onApplied).not.toHaveBeenCalled();
  expect(patches).toEqual([]);
  expect(onDescribeSettled).toHaveBeenCalledTimes(1);
});
