import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { CanvasView, resetCanvasDraftsForTests } from "./CanvasView";
import { resetCanvasNodeCachesForTests } from "./CanvasNodes";
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
  return { session_id: "sess_1", revision: 1, frames, items: [], edges: [] };
}

let fetches: { url: string; init?: RequestInit }[] = [];

beforeEach(() => {
  fetches = [];
  resetCanvasDraftsForTests();
  resetCanvasNodeCachesForTests();
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
  expect(props.onSend).toHaveBeenCalledWith("later", expect.any(Function));
});

function linkedBoard(): CanvasBoard {
  return {
    session_id: "sess_1",
    revision: 4,
    frames: [frame({ id: "9a8b7c6d5e4f3a2b", x: 400, y: 0 })],
    link: { path: "design/shop.canvas", stamp: "[10,20]" },
    items: [
      {
        id: "g1",
        type: "group",
        x: -500,
        y: -100,
        width: 450,
        height: 400,
        label: "Notes",
        color: "4",
      },
      {
        id: "t1",
        type: "text",
        x: -450,
        y: 0,
        width: 300,
        height: 120,
        text: "## Checkout\nTwo **steps**",
      },
      {
        id: "l1",
        type: "link",
        x: 2000,
        y: 0,
        width: 300,
        height: 100,
        url: "javascript:alert(1)",
      },
    ],
    edges: [
      { id: "e1", fromNode: "t1", toNode: "9a8b7c6d5e4f3a2b", label: "opens" },
    ],
  };
}

test("a linked .canvas file draws its cards, groups and edges beside the frames", () => {
  renderView({ board: linkedBoard() });
  expect(screen.getByTestId("canvas-subtitle").textContent).toContain(
    "design/shop.canvas",
  );
  const text = document.querySelector('[data-node-id="t1"]')!;
  expect(text.querySelector("h2")?.textContent).toBe("Checkout");
  expect(text.querySelector("strong")?.textContent).toBe("steps");
  expect(screen.getByTestId("canvas-group").textContent).toBe("Notes");
  expect(
    screen
      .getByTestId("canvas-group")
      .style.getPropertyValue("--canvas-node-color"),
  ).toBe("#44cf6e");
  const edges = screen.getByTestId("canvas-edges");
  expect(edges.querySelectorAll("path.canvas-edge")).toHaveLength(1);
  expect(edges.querySelectorAll("polygon.canvas-edge-arrow")).toHaveLength(1);
  expect(document.querySelector(".canvas-edge-label")?.textContent).toBe(
    "opens",
  );
  // A link that is not a web address is shown, never made clickable.
  const link = document.querySelector('[data-node-id="l1"]')!;
  expect(link.querySelector("a")).toBeNull();
  expect(link.textContent).toContain("javascript:alert(1)");
});

// jsdom carries no PointerEvent: a MouseEvent under the pointer event's name
// carries the coordinates the board reads.
function pointer(
  type: "pointerdown" | "pointermove" | "pointerup",
  el: Element,
  x: number,
  y: number,
  id = 1,
) {
  const e = new MouseEvent(type, {
    bubbles: true,
    cancelable: true,
    button: 0,
    clientX: x,
    clientY: y,
  });
  Object.defineProperty(e, "pointerId", { value: id });
  fireEvent(el, e);
}

test("dragging a group moves it with what it holds in one change of the file", () => {
  renderView({ board: linkedBoard() });
  const group = screen.getByTestId("canvas-group");
  pointer("pointerdown", group, 10, 10);
  pointer("pointermove", group, 30, 10);
  pointer("pointerup", group, 30, 10);
  const patch = fetches.find((f) => f.url.endsWith("/canvas/nodes"));
  expect(patch?.init?.method).toBe("PATCH");
  const body = JSON.parse(String(patch!.init!.body)) as {
    nodes: { id: string; x: number; y: number }[];
  };
  expect(body.nodes.map((n) => n.id).sort()).toEqual(["g1", "t1"]);
  const g = body.nodes.find((n) => n.id === "g1")!;
  const t = body.nodes.find((n) => n.id === "t1")!;
  // Both moved by the same distance on the board, and only sideways.
  expect(g.x + 500).toBeGreaterThan(0);
  expect(t.x + 450).toBe(g.x + 500);
  expect(t.y).toBe(0);
  expect(g.y).toBe(-100);
  // The frame outside the group stays where it was.
  expect(body.nodes.some((n) => n.id === "9a8b7c6d5e4f3a2b")).toBe(false);
  // A press that does not travel is no change.
  fetches = [];
  const text = document.querySelector('[data-node-id="t1"]') as HTMLElement;
  pointer("pointerdown", text, 5, 5, 2);
  pointer("pointermove", text, 6, 5, 2);
  pointer("pointerup", text, 6, 5, 2);
  expect(fetches.some((f) => f.url.endsWith("/canvas/nodes"))).toBe(false);
});

test("the file menu saves the chat's own board as a .canvas file", async () => {
  const props = renderView();
  fireEvent.click(screen.getByTestId("canvas-file-menu"));
  fireEvent.click(screen.getByTestId("canvas-file-save"));
  const input = screen.getByTestId("canvas-file-path") as HTMLInputElement;
  fireEvent.change(input, { target: { value: "design/board.txt" } });
  fireEvent.click(screen.getByTestId("canvas-file-submit"));
  expect(screen.getByTestId("canvas-file-error").textContent).toMatch(
    /\.canvas/,
  );
  fireEvent.change(input, { target: { value: "design/board.canvas" } });
  await act(async () => {
    fireEvent.click(screen.getByTestId("canvas-file-submit"));
  });
  const post = fetches.find((f) => f.url.endsWith("/canvas/export"));
  expect(post?.init?.method).toBe("POST");
  expect(JSON.parse(String(post!.init!.body))).toEqual({
    path: "design/board.canvas",
  });
  expect(props.reloadBoard).toHaveBeenCalled();
  expect(screen.queryByTestId("canvas-file-form")).toBeNull();
});

test("a linked file that cannot be read says so and offers the chat's own board", () => {
  const b = linkedBoard();
  renderView({
    board: {
      ...b,
      frames: [],
      items: [],
      edges: [],
      link: { ...b.link!, error: "unexpected end of JSON input" },
    },
  });
  expect(screen.getByTestId("canvas-link-error").textContent).toContain(
    "unexpected end of JSON input",
  );
  expect(screen.queryByTestId("canvas-empty")).toBeNull();
  fireEvent.click(screen.getByTestId("canvas-file-menu"));
  fireEvent.click(screen.getByTestId("canvas-file-unlink"));
  expect(
    fetches.some(
      (f) => f.url.endsWith("/canvas/link") && f.init?.method === "DELETE",
    ),
  ).toBe(true);
});

test("a frame renews its address on its own clock, never on every read of the board", () => {
  vi.useFakeTimers({ toFake: ["Date"] });
  const t0 = new Date("2026-10-18T12:00:00Z").getTime();
  vi.setSystemTime(t0);
  const props = {
    sessionId: "sess_1",
    reloadBoard: vi.fn(),
    focusFrameId: "",
    focusSeq: 0,
    busy: false,
    onSend: vi.fn(),
    onStop: vi.fn(),
    onClose: vi.fn(),
  };
  // The server's clock is a day behind this page's: every address it signs
  // already reads as expired here.
  const skewed = (n: number) =>
    board([
      frame({
        url: `/coddy/canvas/tok${n}.sig/index.html`,
        expires_at: new Date(t0 - 12 * 3600 * 1000).toISOString(),
      }),
    ]);
  const { rerender } = render(<CanvasView {...props} board={skewed(1)} />);
  const seq = () => iframe().getAttribute("data-seq");
  expect(seq()).toBe("0");
  rerender(<CanvasView {...props} board={skewed(2)} />);
  rerender(<CanvasView {...props} board={skewed(3)} />);
  expect(seq()).toBe("0");
  // Eleven and a half hours on, the next address the board signs is taken.
  vi.setSystemTime(t0 + 11.6 * 3600 * 1000);
  rerender(<CanvasView {...props} board={skewed(4)} />);
  expect(seq()).toBe("1");
  expect(iframe().getAttribute("src")).toContain("tok4.sig");
});

test("a request the server never took comes back to the bar with its picks", () => {
  const onSend = vi.fn();
  renderView({ onSend });
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
  fireEvent.click(screen.getByTestId("canvas-popover-add"));
  const field = screen.getByTestId("canvas-bar-field") as HTMLTextAreaElement;
  fireEvent.change(field, { target: { value: "Make it blue." } });
  fireEvent.click(screen.getByTestId("canvas-send"));
  expect(field.value).toBe("");
  expect(screen.queryByTestId("canvas-pick")).toBeNull();
  // Written meanwhile, then the refusal: both stay, the request first.
  fireEvent.change(field, { target: { value: "Also the footer." } });
  const giveBack = onSend.mock.calls[0]![1] as () => void;
  act(() => giveBack());
  expect(field.value).toBe("Make it blue.\n\nAlso the footer.");
  expect(screen.getByTestId("canvas-pick").textContent).toContain("h1.hero");
});

test("a frame pointed at another page of its root loads that page", () => {
  const props = {
    sessionId: "sess_1",
    reloadBoard: vi.fn(),
    focusFrameId: "",
    focusSeq: 0,
    busy: false,
    onSend: vi.fn(),
    onStop: vi.fn(),
    onClose: vi.fn(),
  };
  const { rerender } = render(
    <CanvasView {...props} board={board([frame()])} />,
  );
  expect(iframe().getAttribute("data-seq")).toBe("0");
  rerender(
    <CanvasView
      {...props}
      board={board([
        frame({
          path: "site/about.html",
          url: "/coddy/canvas/tok2.sig/about.html",
        }),
      ])}
    />,
  );
  expect(iframe().getAttribute("data-seq")).toBe("1");
  expect(iframe().getAttribute("src")).toContain("/about.html");
});

test("a note card reads its file again while the board is open", async () => {
  vi.useFakeTimers({ toFake: ["Date"] });
  const t0 = new Date("2026-10-18T12:00:00Z").getTime();
  vi.setSystemTime(t0);
  let body = "# One";
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      if (String(url).includes("/workspace/text")) {
        return new Response(
          JSON.stringify({
            lines: body.split("\n"),
            offset: 0,
            next_offset: 1,
            has_more: false,
            etag: "e",
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        );
      }
      return new Response("{}", { status: 200 });
    }),
  );
  const noteBoard = (): CanvasBoard => ({
    session_id: "sess_1",
    revision: 1,
    frames: [],
    link: { path: "design/notes.canvas", stamp: "[1,2]" },
    items: [
      {
        id: "n1",
        type: "file",
        file: "notes/brief.md",
        x: 0,
        y: 0,
        width: 400,
        height: 300,
      },
    ],
    edges: [],
  });
  const props = {
    sessionId: "sess_1",
    reloadBoard: vi.fn(),
    focusFrameId: "",
    focusSeq: 0,
    busy: false,
    onSend: vi.fn(),
    onStop: vi.fn(),
    onClose: vi.fn(),
  };
  const { rerender } = render(<CanvasView {...props} board={noteBoard()} />);
  const heading = () =>
    document.querySelector('[data-node-id="n1"] h1')?.textContent;
  await waitFor(() => expect(heading()).toBe("One"));
  body = "# Two";
  // The next read of the board, a little later: the note is read again.
  vi.setSystemTime(t0 + 11000);
  rerender(<CanvasView {...props} board={noteBoard()} />);
  await waitFor(() => expect(heading()).toBe("Two"));
});

test("what the window tells a frame names no address, token or file", () => {
  // The window has to post to an opaque-origin frame with "*", so whatever
  // document sits in the frame then reads it: it must learn nothing it could use.
  renderView();
  const win = iframe().contentWindow!;
  const post = vi.spyOn(win, "postMessage");
  fromFrame({ type: "ready" });
  fireEvent.click(screen.getByTestId("canvas-mode-comment"));
  fromFrame({ type: "ready" });
  expect(post.mock.calls.length).toBeGreaterThan(0);
  for (const [msg, target] of post.mock.calls) {
    expect(target).toBe("*");
    const m = msg as Record<string, unknown>;
    expect(
      Object.keys(m).every((k) =>
        ["proto", "type", "gen", "frame", "mode"].includes(k),
      ),
    ).toBe(true);
    const text = JSON.stringify(m);
    expect(text).not.toContain("tok.sig");
    expect(text).not.toContain("/coddy/");
    expect(text).not.toContain("site/index.html");
  }
});
