import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { CanvasView, resetCanvasDraftsForTests } from "./CanvasView";
import { FRAME_SANDBOX, LEFT_CANVAS_MS } from "./CanvasFrame";
import { BRIDGE_PROTOCOL } from "./bridgeProtocol";
import type { CanvasBoard, CanvasFrame } from "./api";

/**
 * The canvas window: a board of live frames over the chat. A frame is a page
 * in a sandboxed iframe at its own size; the window speaks the bridge protocol
 * with it, generation by generation; a pick becomes a chip of the prompt bar
 * and, sent, a prompt of the chat.
 */

function frame(over: Partial<CanvasFrame> = {}): CanvasFrame {
  return {
    id: "fr_one",
    path: "site/index.html",
    root: "site",
    device: "desktop",
    width: 1280,
    height: 800,
    x: 0,
    y: 0,
    added_by: "agent",
    added_at: "2026-10-18T12:00:00Z",
    shown_at: "2026-10-18T12:00:00Z",
    gen: 1,
    stamp: "s1",
    url: "/coddy/canvas/tok.sig/index.html",
    expires_at: new Date(Date.now() + 12 * 3600 * 1000).toISOString(),
    title: "Shop",
    ...over,
  };
}

function board(frames: CanvasFrame[]): CanvasBoard {
  return { session_id: "sess_1", revision: 1, frames };
}

let fetches: { url: string; init?: RequestInit }[] = [];

beforeEach(() => {
  fetches = [];
  resetCanvasDraftsForTests();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      fetches.push({ url: String(url), ...(init ? { init } : {}) });
      return new Response(
        JSON.stringify({ frame: frame({ gen: 2 }), revision: 2 }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      );
    }),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

function renderView(over: Partial<Parameters<typeof CanvasView>[0]> = {}) {
  const props = {
    sessionId: "sess_1",
    board: board([frame()]),
    reloadBoard: vi.fn(),
    focusFrameId: "",
    focusSeq: 0,
    busy: false,
    onSend: vi.fn(),
    onStop: vi.fn(),
    onClose: vi.fn(),
    ...over,
  };
  render(<CanvasView {...props} />);
  return props;
}

function iframe(): HTMLIFrameElement {
  return document.querySelector(
    "iframe.canvas-frame-page",
  ) as HTMLIFrameElement;
}

function fromFrame(data: Record<string, unknown>, source?: Window | null) {
  act(() => {
    window.dispatchEvent(
      new MessageEvent("message", {
        data: { proto: BRIDGE_PROTOCOL, ...data },
        source: source === undefined ? iframe().contentWindow : source,
      }),
    );
  });
}

test("a frame is its page in a sandbox that grants no origin, at its own size", () => {
  renderView();
  const el = iframe();
  expect(el.getAttribute("sandbox")).toBe(FRAME_SANDBOX);
  expect(FRAME_SANDBOX).not.toMatch(
    /allow-same-origin|allow-top-navigation|allow-popups/,
  );
  expect(el.getAttribute("referrerpolicy")).toBe("no-referrer");
  expect(el.getAttribute("src")).toBe(
    new URL("/coddy/canvas/tok.sig/index.html", window.location.origin).href,
  );
  expect(el.style.width).toBe("1280px");
  expect(el.style.height).toBe("800px");
  expect(screen.getByTestId("canvas-frame-bar").textContent).toContain("Shop");
});

test("the window greets every new document with its generation and drops older ones", () => {
  renderView();
  const win = iframe().contentWindow!;
  const post = vi.spyOn(win, "postMessage");
  fromFrame({ type: "ready" });
  expect(post).toHaveBeenCalledWith(
    expect.objectContaining({ proto: BRIDGE_PROTOCOL, type: "hello", gen: 1 }),
    "*",
  );
  // A message of another window is never taken.
  fromFrame(
    { type: "console", gen: 1, level: "error", message: "boom" },
    window,
  );
  expect(screen.queryByTestId("canvas-frame-errors")).toBeNull();
  // A second document of the frame: the first one's late lines are dropped.
  fromFrame({ type: "ready" });
  fromFrame({ type: "console", gen: 1, level: "error", message: "old" });
  expect(screen.queryByTestId("canvas-frame-errors")).toBeNull();
  fromFrame({ type: "console", gen: 2, level: "error", message: "new" });
  expect(screen.getByTestId("canvas-frame-errors").textContent).toBe("1");
});

test("a pick in Comment mode becomes a chip and, sent, the prompt of the chat", () => {
  const props = renderView();
  fireEvent.click(screen.getByTestId("canvas-mode-comment"));
  fromFrame({ type: "ready" });
  fromFrame({
    type: "pick",
    gen: 1,
    selector: "main > h1",
    tag: "h1",
    id: "",
    classes: ["hero"],
    text: "Welcome",
    html: '<h1 class="hero">Welcome</h1>',
    rect: { x: 10, y: 10, width: 200, height: 40 },
  });
  fireEvent.change(screen.getByTestId("canvas-popover-note"), {
    target: { value: "Bigger" },
  });
  fireEvent.click(screen.getByTestId("canvas-popover-add"));
  expect(screen.getByTestId("canvas-pick").textContent).toContain("h1.hero");
  fireEvent.change(screen.getByTestId("canvas-bar-field"), {
    target: { value: "And blue." },
  });
  fireEvent.click(screen.getByTestId("canvas-send"));
  expect(props.onSend).toHaveBeenCalledTimes(1);
  const sent = (props.onSend as ReturnType<typeof vi.fn>).mock
    .calls[0]![0] as string;
  expect(sent).toContain(
    "On the canvas, `site/index.html` (desktop 1280x800):",
  );
  expect(sent).toContain("Element `main > h1`:");
  expect(sent).toContain('<h1 class="hero">Welcome</h1>');
  expect(sent).toContain("Bigger");
  expect(sent.endsWith("And blue.")).toBe(true);
  // The bar is empty again, the board back on Interact.
  expect(screen.queryByTestId("canvas-pick")).toBeNull();
  expect(
    screen.getByTestId("canvas-mode-interact").getAttribute("aria-pressed"),
  ).toBe("true");
});

test("page text is shown as text, never as markup", () => {
  renderView();
  fromFrame({ type: "ready" });
  fromFrame({
    type: "pick",
    gen: 1,
    selector: "x",
    tag: "img",
    id: '"><img src=x onerror=alert(1)>',
    classes: [],
    html: "<img src=x onerror=alert(1)>",
    rect: { x: 0, y: 0, width: 1, height: 1 },
  });
  const pop = screen.getByTestId("canvas-popover");
  expect(pop.querySelector("img")).toBeNull();
  expect(pop.textContent).toContain('img#"><img src=x onerror=alert(1)>');
});

test("a load that brings no bridge is a page that left the canvas: its frame is rotated", () => {
  vi.useFakeTimers();
  const props = renderView();
  act(() => {
    iframe().dispatchEvent(new Event("load"));
  });
  act(() => {
    vi.advanceTimersByTime(LEFT_CANVAS_MS + 10);
  });
  expect(screen.getByTestId("canvas-frame-left")).toBeTruthy();
  expect(
    fetches.some((f) => f.url.endsWith("/canvas/frames/fr_one/rotate")),
  ).toBe(true);
  void props;
});

test("a load with the bridge is fine, and a new stamp reloads the frame alone", () => {
  vi.useFakeTimers();
  const base = {
    sessionId: "sess_1",
    reloadBoard: vi.fn(),
    focusFrameId: "",
    focusSeq: 0,
    busy: false,
    onSend: vi.fn(),
    onStop: vi.fn(),
    onClose: vi.fn(),
  };
  const two = (over: Partial<CanvasFrame>) =>
    frame({
      id: "fr_two",
      x: 1400,
      stamp: "t1",
      url: "/coddy/canvas/two.sig/index.html",
      ...over,
    });
  const view = render(
    <CanvasView {...base} board={board([frame(), two({})])} />,
  );
  fromFrame({ type: "ready" });
  act(() => {
    iframe().dispatchEvent(new Event("load"));
    vi.advanceTimersByTime(LEFT_CANVAS_MS + 10);
  });
  expect(screen.queryByTestId("canvas-frame-left")).toBeNull();
  const srcs = () =>
    [...document.querySelectorAll("iframe")].map((f) => f.getAttribute("src"));
  const [oneBefore, twoBefore] = srcs();
  // A fresh read of the board signs fresh addresses; only the frame whose
  // files changed takes its new one.
  view.rerender(
    <CanvasView
      {...base}
      board={board([
        frame({ stamp: "s2", url: "/coddy/canvas/tok2.sig/index.html" }),
        two({ url: "/coddy/canvas/two2.sig/index.html" }),
      ])}
    />,
  );
  const [oneAfter, twoAfter] = srcs();
  expect(oneAfter).not.toBe(oneBefore);
  expect(oneAfter).toContain("/coddy/canvas/tok2.sig/");
  expect(twoAfter).toBe(twoBefore);
});

test("a document that leaves with no next one saying ready left the canvas", () => {
  vi.useFakeTimers();
  renderView();
  fromFrame({ type: "ready" });
  // A page of the frame that leaves before the window greets it, then its load.
  fromFrame({ type: "leaving", gen: 1, frame: "fr_one" });
  fromFrame({ type: "ready" });
  // The leaving document is already discarded: its message has no source.
  fromFrame({ type: "leaving", gen: 0, frame: "fr_other" }, null);
  fromFrame({ type: "leaving", gen: 2, frame: "fr_one" }, null);
  act(() => {
    iframe().dispatchEvent(new Event("load"));
  });
  act(() => {
    vi.advanceTimersByTime(LEFT_CANVAS_MS + 10);
  });
  expect(screen.getByTestId("canvas-frame-left")).toBeTruthy();
  expect(
    fetches.some((f) => f.url.endsWith("/canvas/frames/fr_one/rotate")),
  ).toBe(true);
});

test("Escape leaves Comment mode before it closes the window", () => {
  const props = renderView();
  fireEvent.click(screen.getByTestId("canvas-mode-comment"));
  fromFrame({ type: "ready" });
  fromFrame({ type: "escape", gen: 1 });
  expect(
    screen.getByTestId("canvas-mode-interact").getAttribute("aria-pressed"),
  ).toBe("true");
  expect(props.onClose).not.toHaveBeenCalled();
  fromFrame({ type: "escape", gen: 1 });
  expect(props.onClose).toHaveBeenCalledTimes(1);
});

test("an empty board says where frames come from", () => {
  renderView({ board: board([]) });
  expect(screen.getByTestId("canvas-empty")).toBeTruthy();
});

test("while a turn runs, Send queues and Stop stops", () => {
  const props = renderView({ busy: true });
  fireEvent.click(screen.getByTestId("canvas-stop"));
  expect(props.onStop).toHaveBeenCalledTimes(1);
  fireEvent.change(screen.getByTestId("canvas-bar-field"), {
    target: { value: "later" },
  });
  fireEvent.click(screen.getByTestId("canvas-send"));
  expect(props.onSend).toHaveBeenCalledWith("later");
});
