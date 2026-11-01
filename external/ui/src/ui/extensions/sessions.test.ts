import { describe, expect, test, vi } from "vitest";
import { decodeLiveEvent, extensionSessions, splitFrames } from "./sessions";

describe("the sessions API", () => {
  test("the decoder maps the frames an interface draws", () => {
    expect(
      decodeLiveEvent("", '{"choices":[{"delta":{"content":"Hi"}}]}'),
    ).toEqual([{ type: "text", delta: "Hi" }]);
    expect(
      decodeLiveEvent(
        "tool_call",
        '{"toolCallId":"t1","title":"run_command","argsText":"ls"}',
      ),
    ).toEqual([
      { type: "tool", toolCallId: "t1", name: "run_command", argsText: "ls" },
    ]);
    expect(
      decodeLiveEvent(
        "tool_call_update",
        '{"toolCallId":"t1","status":"completed","resultText":"ok"}',
      ),
    ).toEqual([
      {
        type: "tool_update",
        toolCallId: "t1",
        status: "completed",
        resultText: "ok",
      },
    ]);
    expect(decodeLiveEvent("", "[DONE]")).toEqual([{ type: "done" }]);
    expect(decodeLiveEvent("error", '{"message":"boom"}')).toEqual([
      { type: "error", message: "boom" },
    ]);
    // Frames the decoder does not draw are dropped, not invented.
    expect(decodeLiveEvent("turn_progress", '{"startedAt":"t"}')).toEqual([]);
  });

  test("frames split from a growing buffer and keep the tail", () => {
    let buffer =
      'event: tool_call\ndata: {"toolCallId":"t1"}\n\ndata: {"choices":[{"delta":{"content":"a"}}]}\n\nevent: par';
    const frames = [
      ...splitFrames(
        () => buffer,
        (rest) => (buffer = rest),
      ),
    ];
    expect(frames).toEqual([
      { event: "tool_call", data: '{"toolCallId":"t1"}' },
      { event: "", data: '{"choices":[{"delta":{"content":"a"}}]}' },
    ]);
    expect(buffer).toBe("event: par");
  });

  test("reads wrap the listing and transcript routes", async () => {
    const calls: string[] = [];
    const request = vi.fn(async (url: string) => {
      calls.push(url);
      return new Response(
        JSON.stringify({
          items: [{ id: "s1", title: "Chat", cwd: "H:/w", turnActive: true }],
          messages: [{ id: "m1", role: "user", content: "hi" }],
        }),
        { headers: { "Content-Type": "application/json" } },
      );
    });
    const api = extensionSessions(new AbortController().signal, { request });
    const rows = await api.list({ archive: "exclude", limit: 10 });
    expect(rows[0]).toMatchObject({ id: "s1", turnActive: true });
    expect(Object.isFrozen(rows[0])).toBe(true);
    expect(calls[0]).toBe("/coddy/sessions?archive=exclude&limit=10");
    const state = await api.state("s1");
    expect(state?.title).toBe("Chat");
    const messages = await api.messages("s1", { limit: 5 });
    expect(messages[0]).toMatchObject({ id: "m1", role: "user" });
    expect(calls.at(-1)).toBe("/coddy/sessions/s1/messages?limit=5");
  });

  test("a send posts to the responses route and reports a busy session", async () => {
    const request = vi.fn(async () => new Response(null, { status: 200 }));
    const api = extensionSessions(new AbortController().signal, { request });
    const result = await api.send({
      sessionId: "s1",
      text: "Summarize",
      model: "neuraldeep/qwen3.6-35b-a3b",
    });
    expect(result).toEqual({ accepted: true, status: 200, error: "" });
    const [url, init] = request.mock.calls[0] as unknown as [
      string,
      RequestInit,
    ];
    expect(url).toBe("/v1/responses");
    expect((init.headers as Record<string, string>)["X-Coddy-Session-ID"]).toBe(
      "s1",
    );
    expect(JSON.parse(String(init.body))).toMatchObject({
      input: "Summarize",
      metadata: { model: "neuraldeep/qwen3.6-35b-a3b" },
    });
    const busy = vi.fn(
      async () =>
        new Response(JSON.stringify({ error: { message: "chat busy" } }), {
          status: 409,
        }),
    );
    const api2 = extensionSessions(new AbortController().signal, {
      request: busy,
    });
    const refused = await api2.send({ sessionId: "s1", text: "x" });
    expect(refused).toEqual({
      accepted: false,
      status: 409,
      error: "chat busy",
    });
  });

  test("stop and rewind name their routes", async () => {
    const calls: string[] = [];
    const request = vi.fn(async (url: string) => {
      calls.push(url);
      return new Response(null, { status: 204 });
    });
    const api = extensionSessions(new AbortController().signal, { request });
    await api.stop("s1");
    await api.rewind("s1");
    expect(calls).toEqual([
      "/coddy/sessions/s1/cancel",
      "/coddy/sessions/s1/rewind",
    ]);
  });

  test("the address names the current session", () => {
    window.location.hash = "#/s/sess_abc";
    const api = extensionSessions(new AbortController().signal);
    expect(api.current()).toBe("sess_abc");
    window.location.hash = "#/history";
    expect(api.current()).toBeNull();
    window.location.hash = "";
  });
});
