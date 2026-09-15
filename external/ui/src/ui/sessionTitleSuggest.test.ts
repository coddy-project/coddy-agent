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
  expect(JSON.parse(String(bodies[0]))).toEqual({ title: "My title" });
});
