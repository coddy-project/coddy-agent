import React from "react";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";
import { ConfirmProvider } from "./components/useConfirm";
import { initLocale } from "./i18n/i18n";

/**
 * The composer's per-turn image control: it appears only while the chosen
 * model opted in to native image output (models[].image_generation, shown by
 * GET /v1/models as image_generation), and a turn started with it on rides in
 * as metadata.image_generation.
 */

const MODEL = "fake/alpha-model";

/** Whether the model row of GET /v1/models carries image_generation. */
let imageGeneration: boolean;
let failStream: boolean;
let holdStream: boolean;
let posted: Array<{ input?: string; metadata?: Record<string, unknown> }>;

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

function emptyStream() {
  return new Response(
    new ReadableStream<Uint8Array>({ start: (c) => c.close() }),
    { headers: { "Content-Type": "text/event-stream" } },
  );
}

let eventsController: ReadableStreamDefaultController<Uint8Array> | null = null;
function eventsStream() {
  return new Response(
    new ReadableStream<Uint8Array>({
      start: (c) => {
        eventsController = c;
      },
    }),
    { headers: { "Content-Type": "text/event-stream" } },
  );
}

const fetchMock = vi.fn(
  async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/coddy/events") return eventsStream();
    if (path === "/coddy/info") {
      return json({
        object: "coddy.info",
        version: "1.2.3",
        hostname: "box",
        permissionMode: "ask",
        toolsets: [],
      });
    }
    if (path === "/v1/responses") {
      posted.push(JSON.parse(String(init?.body)));
      if (holdStream) return eventsStream();
      if (failStream)
        return new Response(
          `event: error\ndata: {"error":{"message":"Image failed"}}\n\n`,
          { headers: { "Content-Type": "text/event-stream" } },
        );
      return emptyStream();
    }
    if (path === "/v1/models") {
      return json({
        data: [
          { id: "agent", owned_by: "coddy", max_context_tokens: 128000 },
          {
            id: MODEL,
            owned_by: "fake",
            max_context_tokens: 128000,
            multimodal: true,
            ...(imageGeneration ? { image_generation: true } : {}),
          },
          {
            id: "fake/beta-model",
            owned_by: "fake",
            max_context_tokens: 128000,
            ...(imageGeneration ? { image_generation: true } : {}),
          },
        ],
      });
    }
    if (path.startsWith("/coddy/sessions?")) return json({ sessions: [] });
    const match = path.match(/^\/coddy\/sessions\/([^/?]+)(.*)$/);
    if (match) {
      const sid = decodeURIComponent(match[1]!);
      const suffix = match[2]!.split("?")[0];
      if (suffix === "/canvas")
        return json({
          session_id: sid,
          file: "canvas/site.canvas",
          current: true,
          revision: 1,
          stamp: "f1",
          frames: [
            {
              id: "aaaaaaaaaaaaaaaa",
              path: "site/index.html",
              root: "site",
              device: "desktop",
              width: 1280,
              height: 800,
              x: 0,
              y: 0,
              added_by: "agent",
              gen: 1,
              stamp: "s1",
              url: "/coddy/canvas/tok.sig/index.html",
              expires_at: "2099-01-01T00:00:00Z",
              title: "Shop",
            },
          ],
          items: [],
          edges: [],
        });
      if (suffix === "/canvas/open") return json({});
      if (suffix === "/messages") return json({ messages: [] });
      if (suffix === "/composer-stream") return emptyStream();
      if (suffix === "/tool-calls") return json({ toolCalls: [] });
      if (suffix === "/stats") return json({ stats: {} });
      if (suffix === "/background-tasks") return json({ data: [], running: 0 });
      if (suffix === "/workspace")
        return json({ cwd: "/projects/app", is_git_repo: false });
      if (suffix === "/activity")
        return json({ sessionId: sid, turnActive: holdStream });
      if (!suffix) return json({});
    }
    if (path === "/coddy/config") return json({});
    if (path.startsWith("/coddy/slash-commands")) return json({ items: [] });
    if (path.startsWith("/coddy/workspace/context"))
      return json({ cwd: "/workspace", is_git_repo: false });
    return json({}, 404);
  },
);

beforeEach(() => {
  eventsController = null;
  imageGeneration = false;
  failStream = false;
  holdStream = false;
  posted = [];
  initLocale("en");
  localStorage.clear();
  document.cookie = "coddy_llm_model=; Path=/; Max-Age=0";
  document.cookie = "coddy_workspace_dir=; Path=/; Max-Age=0";
  history.replaceState(null, "", "/");
  fetchMock.mockClear();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
});

const settle = () => act(async () => new Promise((r) => setTimeout(r, 50)));

async function mountHome() {
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Model" })).toHaveTextContent(
      "alpha-model",
    ),
  );
  await settle();
}

async function sendHello() {
  fireEvent.change(screen.getByRole("textbox", { name: "Message" }), {
    target: { value: "hello" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await waitFor(() => expect(posted.length).toBeGreaterThan(0));
}

test("a model without the opt-in offers no image control", async () => {
  await mountHome();
  expect(screen.queryByTestId("composer-imagegen")).toBeNull();
  await sendHello();
  expect(posted[0]!.metadata?.image_generation).toBeUndefined();
});

test("a model without the opt-in offers a CTA to its settings row", async () => {
  await mountHome();
  const cta = screen.getByTestId("composer-imagegen-cta");
  fireEvent.click(cta);
  expect(window.location.hash).toBe("#/settings/models?id=fake%2Falpha-model");
});

test("an opted-in model shows the toggle, not the CTA", async () => {
  imageGeneration = true;
  await mountHome();
  expect(screen.getByTestId("composer-imagegen")).toBeInTheDocument();
  expect(screen.queryByTestId("composer-imagegen-cta")).toBeNull();
});

test("a config reload replaces the CTA with the toggle once the model opts in", async () => {
  await mountHome();
  expect(screen.getByTestId("composer-imagegen-cta")).toBeInTheDocument();
  imageGeneration = true;
  act(() => {
    eventsController!.enqueue(
      new TextEncoder().encode("event: config_reloaded\ndata: {}\n\n"),
    );
  });
  await waitFor(() =>
    expect(screen.getByTestId("composer-imagegen")).toBeInTheDocument(),
  );
  expect(screen.queryByTestId("composer-imagegen-cta")).toBeNull();
});

test("the control appears for an opted-in model and stays off", async () => {
  imageGeneration = true;
  await mountHome();
  const chip = screen.getByTestId("composer-imagegen");
  expect(chip.getAttribute("aria-pressed")).toBe("false");
  await sendHello();
  expect(posted[0]!.metadata?.image_generation).toBeUndefined();
});

test("a turn started with the control on carries image_generation", async () => {
  imageGeneration = true;
  await mountHome();
  fireEvent.click(screen.getByTestId("composer-imagegen"));
  expect(
    screen.getByTestId("composer-imagegen").getAttribute("aria-pressed"),
  ).toBe("true");
  await sendHello();
  expect(posted[0]!.input).toBe("hello");
  expect(posted[0]!.metadata?.image_generation).toBe(true);
});

test("the opt-in stays on for the next send of the chat", async () => {
  imageGeneration = true;
  await mountHome();
  fireEvent.click(screen.getByTestId("composer-imagegen"));
  await sendHello();
  await sendHello();
  expect(posted.length).toBe(2);
  expect(posted[1]!.metadata?.image_generation).toBe(true);
});

test("the image opt-in is cleared on switching to another session", async () => {
  imageGeneration = true;
  await mountHome();
  fireEvent.click(screen.getByTestId("composer-imagegen"));
  act(() => {
    window.location.hash = "#/s/sess_other";
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });
  await waitFor(() =>
    expect(screen.getByTestId("composer-imagegen")).toHaveAttribute(
      "aria-pressed",
      "false",
    ),
  );
});

test("switching models clears the image opt-in, including when switching back", async () => {
  imageGeneration = true;
  await mountHome();
  fireEvent.click(screen.getByTestId("composer-imagegen"));
  fireEvent.click(screen.getByRole("button", { name: "Model" }));
  fireEvent.click(screen.getByText("beta-model"));
  expect(screen.getByTestId("composer-imagegen")).toHaveAttribute(
    "aria-pressed",
    "false",
  );
  fireEvent.click(screen.getByRole("button", { name: "Model" }));
  fireEvent.click(screen.getByText("alpha-model"));
  expect(screen.getByTestId("composer-imagegen")).toHaveAttribute(
    "aria-pressed",
    "false",
  );
});

test("retry retains the image choice of the failed turn", async () => {
  imageGeneration = true;
  failStream = true;
  await mountHome();
  fireEvent.click(screen.getByTestId("composer-imagegen"));
  await sendHello();
  const retry = await screen.findByRole(
    "button",
    { name: /retry/i },
    { timeout: 5000 },
  );
  fireEvent.click(screen.getByTestId("composer-imagegen"));
  fireEvent.click(retry);
  await waitFor(() => expect(posted.length).toBe(2));
  expect(posted[1]!.metadata?.image_generation).toBe(true);
});

test("canvas refuses an image-enabled queued prompt and retains its draft and picks", async () => {
  imageGeneration = true;
  holdStream = true;
  await mountHome();
  fireEvent.click(screen.getByTestId("composer-imagegen"));
  await sendHello();
  const file = new File(["image"], "keep.png", { type: "image/png" });
  fireEvent.change(screen.getByTestId("composer-file-input"), {
    target: { files: [file] },
  });
  await screen.findByText("keep.png");
  const request = fetchMock.mock.calls.find(
    ([url]) => String(url) === "/v1/responses",
  )!;
  const headers = request[1]?.headers as Record<string, string>;
  const sid = headers["X-Coddy-Session-ID"];
  expect(sid).toBeTruthy();
  act(() => {
    window.location.hash = `#/s/${sid}/files?path=canvas%2Fsite.canvas`;
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });
  await screen.findByTestId("canvas-view", {}, { timeout: 10000 });
  await waitFor(() =>
    expect(document.querySelector("iframe.canvas-frame-page")).not.toBeNull(),
  );
  fireEvent.click(screen.getByTestId("canvas-mode-comment"));
  const source = (
    document.querySelector("iframe.canvas-frame-page") as HTMLIFrameElement
  ).contentWindow;
  const dispatch = (data: object) =>
    act(() =>
      window.dispatchEvent(
        new MessageEvent("message", {
          data: { proto: "coddy-canvas/1", ...data },
          source,
        }),
      ),
    );
  dispatch({ type: "ready" });
  dispatch({
    type: "pick",
    gen: 1,
    selector: "main > h1",
    tag: "h1",
    id: "",
    classes: ["hero"],
    text: "Welcome",
    html: "<h1>Welcome</h1>",
    rect: { x: 10, y: 10, width: 200, height: 40 },
  });
  fireEvent.click(screen.getByTestId("canvas-popover-add"));
  fireEvent.change(screen.getByTestId("canvas-bar-field"), {
    target: { value: "Make it blue" },
  });
  expect(screen.getByTestId("canvas-send")).toHaveAttribute(
    "aria-label",
    "Queue for the agent",
  );
  fireEvent.click(screen.getByTestId("canvas-send"));
  expect(screen.getByTestId("canvas-bar-field")).toHaveValue("Make it blue");
  expect(screen.getByTestId("canvas-pick")).toBeVisible();
  expect(screen.getByText("keep.png")).toBeInTheDocument();
  expect(
    within(screen.getByTestId("canvas-view")).getByText(
      "Turn off Images to queue a message, or wait for the current turn to finish.",
    ),
  ).toBeVisible();
  expect(
    fetchMock.mock.calls.some(
      ([url, init]) =>
        String(url).includes("/queue") && init?.method === "POST",
    ),
  ).toBe(false);
  expect(posted).toHaveLength(1);
});
