import { afterEach, expect, test, vi } from "vitest";
import {
  render,
  screen,
  fireEvent,
  waitFor,
  cleanup,
} from "@testing-library/react";
import { ExtensionEvents } from "./events";
import { ExtensionRuntime, type ExtensionAPI } from "./runtime";
import { validateField, validateRenderer } from "./contributions";
import { ExtensionFields } from "./ExtensionFields";
import { ExtensionToolView } from "./ExtensionToolView";
import { I18nProvider } from "../i18n/I18nProvider";

let runtime: ExtensionRuntime | null = null;
vi.mock("./ExtensionsProvider", () => ({
  useExtensions: () => (runtime ? { runtime } : null),
}));
afterEach(() => {
  cleanup();
  runtime = null;
  vi.restoreAllMocks();
});
function setup() {
  runtime = new ExtensionRuntime(() => {});
  const controller = new AbortController();
  const api = { id: "demo", signal: controller.signal } as ExtensionAPI;
  return { runtime, controller, api };
}

test("navigation steps and turns reach listeners with context", async () => {
  const events = new ExtensionEvents(() => {});
  const seen: unknown[] = [];
  const ctl = new AbortController();
  events.on("demo", ctl.signal, "tabChanged", (event) => {
    seen.push(event);
  });
  events.on("demo", ctl.signal, "stepEnded", (event) => {
    seen.push(event);
  });
  events.on("demo", ctl.signal, "turnEnded", (event) => {
    seen.push(event);
  });
  events.emit("tabChanged", { from: "#/s/a", to: "#/s/b", sessionId: "b" });
  events.observe({
    sessionId: "b",
    transport: "post",
    event: "turn_progress",
    id: "1",
    data: JSON.stringify({ startedAt: "2026-10-28T12:00:00Z" }),
    sequence: "1",
  });
  events.observe({
    sessionId: "b",
    transport: "post",
    event: "token_usage",
    id: "2",
    data: JSON.stringify({ inputTokens: 3, outputTokens: 4, totalTokens: 7 }),
    sequence: "2",
  });
  events.turnEnded("b", "2026-10-28T12:01:00Z");
  expect(seen).toMatchObject([
    { to: "#/s/b", sessionId: "b" },
    { sessionId: "b", usage: { totalTokens: 7 } },
    { sessionId: "b" },
  ]);
  ctl.abort();
  events.emit("tabChanged", { from: "#/s/b", to: "#/s/c", sessionId: "c" });
  expect(seen).toHaveLength(3);
});

test("custom MCP card updates and preserves original details", async () => {
  const { runtime, api, controller } = setup();
  const dispose = vi.fn();
  const mount = vi.fn((element: HTMLElement, call: { resultText: string }) => {
    const text = document.createElement("strong");
    text.textContent = `Custom: ${call.resultText}`;
    element.append(text);
    return {
      update: (next: { resultText: string }) => {
        text.textContent = `Custom: ${next.resultText}`;
      },
      dispose,
    };
  });
  runtime.renderers.register(
    "demo",
    api,
    { id: "card", server: "github", tool: "list_repos", mount },
    validateRenderer,
  );
  const view = (resultText: string) => (
    <I18nProvider>
      <ExtensionToolView
        toolCallId="call-1"
        rowId="row-1"
        title="mcp__github__list_repos"
        status="completed"
        resultText={resultText}
        fallback={<p>Default call</p>}
        original={<p>Raw tool details</p>}
      />
    </I18nProvider>
  );
  const rendered = render(view("first"));
  await screen.findByText("Custom: first");
  rendered.rerender(view("second"));
  await screen.findByText("Custom: second");
  expect(mount).toHaveBeenCalledOnce();
  fireEvent.click(screen.getByText("Original tool details"));
  expect(screen.getByText("Raw tool details")).toBeVisible();
  controller.abort();
  await screen.findByText("Default call");
  expect(dispose).toHaveBeenCalledOnce();
});

test("registered Agent field updates its configuration namespace", () => {
  const { runtime, api } = setup();
  runtime.fields.register(
    "demo",
    api,
    {
      id: "badge",
      section: "agent",
      title: "Card badge",
      type: "string",
      default: "LOCAL",
    },
    validateField,
  );
  let doc: Record<string, unknown> = {
    agent: { model: "demo/model" },
    extension_values: { other: { keep: "yes" }, demo: { untouched: 12 } },
  };
  const setDoc = vi.fn((next: Record<string, unknown>) => {
    doc = next;
  });
  render(
    <I18nProvider>
      <ExtensionFields section="agent" doc={doc} setDoc={setDoc} />
    </I18nProvider>,
  );
  expect(screen.getByLabelText("Card badge")).toHaveValue("LOCAL");
  expect(setDoc).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("Card badge"), {
    target: { value: "CUSTOM" },
  });
  expect(doc).toEqual({
    agent: { model: "demo/model" },
    extension_values: {
      other: { keep: "yes" },
      demo: { untouched: 12, badge: "CUSTOM" },
    },
  });
});

test("a broken renderer falls back without losing the tool row", async () => {
  const { runtime, api } = setup();
  runtime.renderers.register(
    "demo",
    api,
    {
      id: "broken",
      server: "github",
      tool: "list_repos",
      mount() {
        throw new Error("bad card");
      },
    },
    validateRenderer,
  );
  render(
    <I18nProvider>
      <ExtensionToolView
        toolCallId="call-1"
        title="github__list_repos"
        status="failed"
        fallback={<p>Default failure</p>}
        original={<p>Original</p>}
      />
    </I18nProvider>,
  );
  await screen.findByText("Default failure");
  expect(runtime.snapshot().errors.demo).toContain("bad card");
});

test("completion listeners deduplicate replay and isolate rejected callbacks", async () => {
  const errors = vi.fn();
  const events = new ExtensionEvents(errors);
  const ctl = new AbortController();
  const good = vi.fn();
  events.on("bad", ctl.signal, "stepEnded", async () => {
    throw new Error("callback failure");
  });
  events.on("good", ctl.signal, "stepEnded", good);
  const frame = {
    sessionId: "s",
    transport: "post" as const,
    event: "token_usage",
    id: "2",
    sequence: "2",
    data: JSON.stringify({ totalTokens: 8 }),
  };
  events.observe({
    ...frame,
    event: "turn_progress",
    data: JSON.stringify({ startedAt: "one" }),
  });
  events.observe(frame);
  events.observe({ ...frame, transport: "relay" });
  await waitFor(() => expect(errors).toHaveBeenCalledOnce());
  expect(good).toHaveBeenCalledOnce();
  expect(Object.isFrozen(good.mock.calls[0]![0].usage)).toBe(true);
  events.observe({
    ...frame,
    event: "turn_progress",
    data: JSON.stringify({ startedAt: "two" }),
  });
  events.observe(frame);
  expect(good).toHaveBeenCalledTimes(2);
  expect(errors).toHaveBeenCalledOnce();
});

test("a stream without progress never inherits a previous turn start", () => {
  const events = new ExtensionEvents(() => {});
  const listener = vi.fn();
  events.on("demo", new AbortController().signal, "stepEnded", listener);
  const frame = {
    sessionId: "s",
    transport: "post" as const,
    event: "token_usage",
    id: "2",
    data: '{"totalTokens":1}',
    sequence: "2",
  };
  events.observe({
    ...frame,
    streamId: "first",
    event: "turn_progress",
    data: '{"startedAt":"old"}',
  });
  events.observe({ ...frame, streamId: "first" });
  events.observe({ ...frame, streamId: "second" });
  expect(listener).toHaveBeenCalledTimes(2);
  expect(listener.mock.calls[1]![0].turnStartedAt).toBeNull();
});
