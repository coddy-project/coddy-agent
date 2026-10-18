import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { CanvasBoard, resetCanvasDraftsForTests } from "./CanvasBoard";
import { resetCanvasNodeCachesForTests } from "./CanvasNodes";
import { FRAME_SANDBOX, LEFT_CANVAS_MS } from "./CanvasFrame";
import { BRIDGE_PROTOCOL } from "./bridgeProtocol";
import type { CanvasBoard as Board, CanvasFrame } from "./api";

/**
 * A .canvas file as the Files window shows it: a board of live frames and the
 * file's other nodes. A frame is a page in a sandboxed iframe at its own size;
 * the board speaks the bridge protocol with it, generation by generation; a
 * pick becomes a chip of the prompt bar and, sent, a prompt of the chat. A pan
 * or a zoom moves the world layer and draws nothing again.
 */

const FILE = "canvas/site.canvas";

function frame(over: Partial<CanvasFrame> = {}): CanvasFrame {
  return {
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
    expires_at: new Date(Date.now() + 12 * 3600 * 1000).toISOString(),
    title: "Shop",
    ...over,
  };
}

function board(frames: CanvasFrame[], over: Partial<Board> = {}): Board {
  return {
    session_id: "sess_1",
    file: FILE,
    current: true,
    revision: 1,
    stamp: "f1",
    frames,
    items: [],
    edges: [],
    ...over,
  };
}

let fetches: { url: string; init?: RequestInit }[] = [];
// What GET .../canvas?file= answers, changed by a test between reads.
let served: Board = board([frame()]);
let noteBody = "# One";
let frameClock: { mockRestore: () => void } | null = null;

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

beforeEach(() => {
  fetches = [];
  served = board([frame()]);
  noteBody = "# One";
  resetCanvasDraftsForTests();
  resetCanvasNodeCachesForTests();
  // The camera writes once per animation frame; here at once.
  frameClock = vi
    .spyOn(window, "requestAnimationFrame")
    .mockImplementation((cb) => {
      cb(0);
      return 1;
    });
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const url = String(input);
      fetches.push({ url, ...(init ? { init } : {}) });
      const method = init?.method ?? "GET";
      if (url.includes("/workspace/text")) {
        return json({
          lines: noteBody.split("\n"),
          offset: 0,
          next_offset: 1,
          has_more: false,
          etag: "e",
        });
      }
      if (url.includes("/canvas/open")) return json(served);
      if (/\/canvas\?file=/.test(url) && method === "GET") return json(served);
      if (url.includes("/rotate"))
        return json({ frame: frame({ gen: 2 }), revision: 2 });
      if (url.includes("/report")) return new Response(null, { status: 204 });
      return json({ revision: 2 });
    }),
  );
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  frameClock?.mockRestore();
  frameClock = null;
  vi.unstubAllGlobals();
});

// Fake timers leave the frame clock to the spy above.
const TIMERS: (
  | "setTimeout"
  | "clearTimeout"
  | "setInterval"
  | "clearInterval"
)[] = ["setTimeout", "clearTimeout", "setInterval", "clearInterval"];

type Props = Parameters<typeof CanvasBoard>[0];

function props(over: Partial<Props> = {}): Props {
  return {
    sessionId: "sess_1",
    file: FILE,
    epoch: 0,
    focusFrameId: "",
    focusSeq: 0,
    busy: false,
    onSend: vi.fn(),
    onStop: vi.fn(),
    onClose: vi.fn(),
    ...over,
  };
}

/** Renders the board and waits for its first read. */
async function renderBoard(over: Partial<Props> = {}) {
  const p = props(over);
  const view = render(<CanvasBoard {...p} />);
  await waitFor(() =>
    expect(
      fetches.some((f) => /\/canvas\?file=/.test(f.url) && !f.init?.method),
    ).toBe(true),
  );
  await act(async () => {});
  return { props: p, view };
}

/** Moves the epoch, as a canvas_changed does, and waits for the read. */
async function reread(view: ReturnType<typeof render>, p: Props) {
  p.epoch += 1;
  view.rerender(<CanvasBoard {...p} />);
  await act(async () => {});
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

test("opening the board makes its file the chat's canvas and reads it", async () => {
  await renderBoard();
  const open = fetches.find((f) => f.url.includes("/canvas/open"));
  expect(open?.init?.method).toBe("POST");
  expect(open?.url).toContain(`file=${encodeURIComponent(FILE)}`);
  expect(iframe()).toBeTruthy();
});

test("a frame is its page in a sandbox that grants no origin, at its own size", async () => {
  await renderBoard();
  const el = iframe();
  expect(el.getAttribute("sandbox")).toBe(FRAME_SANDBOX);
  expect(FRAME_SANDBOX).not.toMatch(
    /allow-same-origin|allow-top-navigation|allow-popups/,
  );
  expect(el.getAttribute("referrerpolicy")).toBe("no-referrer");
  expect(el.getAttribute("src")).toBe(
    new URL("/coddy/canvas/tok.sig/index.html", window.location.origin).href,
  );
  // At its own size in the world layer; the camera scales the layer.
  const box = screen.getByTestId("canvas-frame");
  expect(box.style.width).toBe("1280px");
  expect(box.style.height).toBe("800px");
  expect(el.style.transform).toBe("");
  expect(screen.getByTestId("canvas-frame-bar").textContent).toContain("Shop");
});

test("the wheel zooms at the pointer and redraws no frame", async () => {
  await renderBoard();
  const before = iframe();
  const world = screen.getByTestId("canvas-world");
  expect(screen.getByTestId("canvas-fit").textContent).toBe("");
  fireEvent.wheel(screen.getByTestId("canvas-board"), {
    deltaY: -500,
    clientX: 100,
    clientY: 50,
  });
  // One turn of 500 pixels doubles the board around the pointer.
  expect(world.style.transform).toBe("translate(-100px, -50px) scale(2)");
  expect(
    screen.getByTestId("canvas-board").style.getPropertyValue("--canvas-inv"),
  ).toBe("0.5");
  expect(screen.getByTestId("canvas-fit").textContent).toBe("200%");
  // The same iframe, untouched: nothing on the board was drawn again.
  expect(iframe()).toBe(before);
  expect(iframe().getAttribute("data-seq")).toBe("0");
  fireEvent.click(screen.getByTestId("canvas-zoom-out"));
  expect(screen.getByTestId("canvas-fit").textContent).toBe("160%");
});

test("dragging the empty board pans it", async () => {
  await renderBoard();
  const b = screen.getByTestId("canvas-board");
  pointer("pointerdown", b, 10, 10);
  pointer("pointermove", b, 40, 30);
  pointer("pointerup", b, 40, 30);
  expect(screen.getByTestId("canvas-world").style.transform).toBe(
    "translate(30px, 20px) scale(1)",
  );
});

test("a board read again with nothing changed is not drawn again", async () => {
  const { view, props: p } = await renderBoard();
  const before = iframe();
  // Every read signs fresh addresses; the frame keeps the one it loaded.
  served = board([frame({ url: "/coddy/canvas/fresh.sig/index.html" })]);
  await reread(view, p);
  expect(iframe()).toBe(before);
  expect(iframe().getAttribute("src")).toContain("tok.sig");
});

test("the board greets every new document with its generation and drops older ones", async () => {
  await renderBoard();
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

function pick() {
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
}

test("a pick in Comment mode becomes a chip and, sent, the prompt of the chat", async () => {
  const { props: p } = await renderBoard();
  fireEvent.click(screen.getByTestId("canvas-mode-comment"));
  fromFrame({ type: "ready" });
  pick();
  fireEvent.change(screen.getByTestId("canvas-popover-note"), {
    target: { value: "Bigger" },
  });
  fireEvent.click(screen.getByTestId("canvas-popover-add"));
  expect(screen.getByTestId("canvas-pick").textContent).toContain("h1.hero");
  fireEvent.change(screen.getByTestId("canvas-bar-field"), {
    target: { value: "And blue." },
  });
  fireEvent.click(screen.getByTestId("canvas-send"));
  expect(p.onSend).toHaveBeenCalledTimes(1);
  const sent = (p.onSend as ReturnType<typeof vi.fn>).mock
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

test("page text is shown as text, never as markup", async () => {
  await renderBoard();
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

test("a load that brings no bridge is a page that left the canvas: its frame is rotated", async () => {
  await renderBoard();
  vi.useFakeTimers({ toFake: TIMERS });
  act(() => {
    iframe().dispatchEvent(new Event("load"));
  });
  act(() => {
    vi.advanceTimersByTime(LEFT_CANVAS_MS + 10);
  });
  expect(screen.getByTestId("canvas-frame-left")).toBeTruthy();
  const rotate = fetches.find((f) => f.url.includes("/rotate"));
  expect(rotate?.url).toContain(
    `/canvas/frames/aaaaaaaaaaaaaaaa/rotate?file=${encodeURIComponent(FILE)}`,
  );
});

test("a new stamp reloads that frame alone", async () => {
  const two = (over: Partial<CanvasFrame>) =>
    frame({
      id: "bbbbbbbbbbbbbbbb",
      x: 1400,
      stamp: "t1",
      url: "/coddy/canvas/two.sig/index.html",
      ...over,
    });
  served = board([frame(), two({})]);
  const { view, props: p } = await renderBoard();
  const srcs = () =>
    [...document.querySelectorAll("iframe")].map((f) => f.getAttribute("src"));
  const [oneBefore, twoBefore] = srcs();
  served = board([
    frame({ stamp: "s2", url: "/coddy/canvas/tok2.sig/index.html" }),
    two({ url: "/coddy/canvas/two2.sig/index.html" }),
  ]);
  await reread(view, p);
  const [oneAfter, twoAfter] = srcs();
  expect(oneAfter).not.toBe(oneBefore);
  expect(oneAfter).toContain("/coddy/canvas/tok2.sig/");
  expect(twoAfter).toBe(twoBefore);
});

test("a document that leaves with no next one saying ready left the canvas", async () => {
  await renderBoard();
  vi.useFakeTimers({ toFake: TIMERS });
  fromFrame({ type: "ready" });
  fromFrame({ type: "leaving", gen: 1, frame: "aaaaaaaaaaaaaaaa" });
  fromFrame({ type: "ready" });
  // The leaving document is already discarded: its message has no source.
  fromFrame({ type: "leaving", gen: 0, frame: "ffffffffffffffff" }, null);
  fromFrame({ type: "leaving", gen: 2, frame: "aaaaaaaaaaaaaaaa" }, null);
  act(() => {
    iframe().dispatchEvent(new Event("load"));
  });
  act(() => {
    vi.advanceTimersByTime(LEFT_CANVAS_MS + 10);
  });
  expect(screen.getByTestId("canvas-frame-left")).toBeTruthy();
  expect(fetches.some((f) => f.url.includes("/rotate"))).toBe(true);
});

test("Escape leaves Comment mode before it closes the window", async () => {
  const { props: p } = await renderBoard();
  fireEvent.click(screen.getByTestId("canvas-mode-comment"));
  fromFrame({ type: "ready" });
  fromFrame({ type: "escape", gen: 1 });
  expect(
    screen.getByTestId("canvas-mode-interact").getAttribute("aria-pressed"),
  ).toBe("true");
  expect(p.onClose).not.toHaveBeenCalled();
  fromFrame({ type: "escape", gen: 1 });
  expect(p.onClose).toHaveBeenCalledTimes(1);
});

test("an empty file says where frames come from", async () => {
  served = board([]);
  await renderBoard();
  expect(screen.getByTestId("canvas-empty")).toBeTruthy();
});

test("a file that cannot be read says so and shows nothing of it", async () => {
  served = board([], { error: "unexpected end of JSON input" });
  await renderBoard();
  expect(screen.getByTestId("canvas-file-error").textContent).toContain(
    "unexpected end of JSON input",
  );
  expect(screen.queryByTestId("canvas-empty")).toBeNull();
});

test("the bar has one button, as the composer: Stop while a turn runs and the field is empty, Queue once it is not", async () => {
  const { props: p } = await renderBoard({ busy: true });
  expect(screen.queryByTestId("canvas-send")).toBeNull();
  const stop = screen.getByTestId("canvas-stop");
  expect(stop.className).toContain("composer-send-stop");
  expect(
    stop.querySelector(".composer-send-glyph .composer-stop-square"),
  ).not.toBeNull();
  fireEvent.click(stop);
  expect(p.onStop).toHaveBeenCalledTimes(1);
  fireEvent.change(screen.getByTestId("canvas-bar-field"), {
    target: { value: "later" },
  });
  expect(screen.queryByTestId("canvas-stop")).toBeNull();
  const queue = screen.getByTestId("canvas-send");
  expect(queue.getAttribute("aria-label")).toBe("Queue for the agent");
  expect(queue.className).toContain("composer-run-icon--queue");
  fireEvent.click(queue);
  expect(p.onSend).toHaveBeenCalledWith("later", expect.any(Function));
});

test("idle, the bar's one button is Send, held back while there is nothing to send", async () => {
  await renderBoard();
  expect(screen.queryByTestId("canvas-stop")).toBeNull();
  const send = screen.getByTestId("canvas-send") as HTMLButtonElement;
  expect(send.disabled).toBe(true);
  fireEvent.change(screen.getByTestId("canvas-bar-field"), {
    target: { value: "bigger" },
  });
  expect(send.disabled).toBe(false);
  expect(send.getAttribute("aria-label")).toBe("Send");
});

function obsidianBoard(): Board {
  return board([frame({ id: "9a8b7c6d5e4f3a2b", x: 400, y: 0 })], {
    file: "design/shop.canvas",
    revision: 4,
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
  });
}

test("a .canvas file draws its cards, groups and edges beside the frames", async () => {
  served = obsidianBoard();
  await renderBoard({ file: "design/shop.canvas" });
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

test("dragging a group moves it with what it holds in one change of the file", async () => {
  served = obsidianBoard();
  await renderBoard({ file: "design/shop.canvas" });
  const group = screen.getByTestId("canvas-group");
  pointer("pointerdown", group, 10, 10);
  pointer("pointermove", group, 30, 10);
  pointer("pointerup", group, 30, 10);
  const patch = fetches.find((f) => f.url.includes("/canvas/nodes"));
  expect(patch?.init?.method).toBe("PATCH");
  expect(patch?.url).toContain("file=design%2Fshop.canvas");
  const body = JSON.parse(String(patch!.init!.body)) as {
    nodes: { id: string; x: number; y: number }[];
  };
  expect(body.nodes.map((n) => n.id).sort()).toEqual(["g1", "t1"]);
  const g = body.nodes.find((n) => n.id === "g1")!;
  const t = body.nodes.find((n) => n.id === "t1")!;
  // Both moved by the same distance on the board, and only sideways.
  expect(g.x).toBe(-480);
  expect(t.x).toBe(-430);
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
  expect(fetches.some((f) => f.url.includes("/canvas/nodes"))).toBe(false);
});

test("a drag on a zoomed board moves the node by the board's distance", async () => {
  served = obsidianBoard();
  await renderBoard({ file: "design/shop.canvas" });
  fireEvent.wheel(screen.getByTestId("canvas-board"), {
    deltaY: -500,
    clientX: 0,
    clientY: 0,
  });
  const text = document.querySelector('[data-node-id="t1"]') as HTMLElement;
  pointer("pointerdown", text, 10, 10);
  pointer("pointermove", text, 50, 10);
  pointer("pointerup", text, 50, 10);
  const patch = fetches.find((f) => f.url.includes("/canvas/nodes"));
  const body = JSON.parse(String(patch!.init!.body)) as {
    nodes: { id: string; x: number }[];
  };
  // 40 screen pixels at 200% are 20 on the board.
  expect(body.nodes).toEqual([{ id: "t1", x: -430, y: 0 }]);
});

test("a frame's device menu resizes it through a change of the file's nodes", async () => {
  await renderBoard();
  fireEvent.click(screen.getByRole("button", { name: "Size" }));
  fireEvent.click(screen.getByRole("menuitemradio", { name: /Phone/ }));
  const patch = fetches.find((f) => f.url.includes("/canvas/nodes"));
  expect(JSON.parse(String(patch!.init!.body))).toEqual({
    nodes: [{ id: "aaaaaaaaaaaaaaaa", device: "phone" }],
  });
});

test("a frame renews its address on its own clock, never on every read of the board", async () => {
  vi.useFakeTimers({ toFake: ["Date"] });
  const t0 = new Date("2026-10-18T12:00:00Z").getTime();
  vi.setSystemTime(t0);
  // The server's clock is a day behind this page's: every address it signs
  // already reads as expired here.
  const skewed = (n: number) =>
    board([
      frame({
        url: `/coddy/canvas/tok${n}.sig/index.html`,
        expires_at: new Date(t0 - 12 * 3600 * 1000).toISOString(),
      }),
    ]);
  served = skewed(1);
  const { view, props: p } = await renderBoard();
  const seq = () => iframe().getAttribute("data-seq");
  expect(seq()).toBe("0");
  served = skewed(2);
  await reread(view, p);
  served = skewed(3);
  await reread(view, p);
  expect(seq()).toBe("0");
  // Eleven and a half hours on, the next address the board signs is taken.
  vi.setSystemTime(t0 + 11.6 * 3600 * 1000);
  served = skewed(4);
  await reread(view, p);
  expect(seq()).toBe("1");
  expect(iframe().getAttribute("src")).toContain("tok4.sig");
});

test("a request the server never took comes back to the bar with its picks", async () => {
  const onSend = vi.fn();
  await renderBoard({ onSend });
  fireEvent.click(screen.getByTestId("canvas-mode-comment"));
  fromFrame({ type: "ready" });
  pick();
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

test("a frame pointed at another page of its root loads that page", async () => {
  const { view, props: p } = await renderBoard();
  expect(iframe().getAttribute("data-seq")).toBe("0");
  served = board([
    frame({
      path: "site/about.html",
      url: "/coddy/canvas/tok2.sig/about.html",
    }),
  ]);
  await reread(view, p);
  expect(iframe().getAttribute("data-seq")).toBe("1");
  expect(iframe().getAttribute("src")).toContain("/about.html");
});

test("a note card reads its file again while the board is open", async () => {
  served = board([], {
    file: "design/notes.canvas",
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
  });
  // The card's own clock, which also runs on as the test waits.
  vi.useFakeTimers({ toFake: TIMERS, shouldAdvanceTime: true });
  await renderBoard({ file: "design/notes.canvas" });
  const heading = () =>
    document.querySelector('[data-node-id="n1"] h1')?.textContent;
  await waitFor(() => expect(heading()).toBe("One"));
  noteBody = "# Two";
  act(() => {
    vi.advanceTimersByTime(11000);
  });
  await waitFor(() => expect(heading()).toBe("Two"));
});

test("what the board tells a frame names no address, token or file", async () => {
  // The board has to post to an opaque-origin frame with "*", so whatever
  // document sits in the frame then reads it: it must learn nothing it could use.
  await renderBoard();
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
    expect(text).not.toContain(FILE);
  }
});

test("a permission the turn waits for is asked on the canvas, which covers the chat", async () => {
  const onPermissionResolved = vi.fn();
  await renderBoard({
    busy: true,
    prompts: [
      {
        id: "pp_1",
        type: "permission_prompt",
        payload: {
          sessionId: "sess_1",
          toolCall: {
            toolCallId: "call_1",
            title: "Run: run_command",
            kind: "run_command",
            content: [
              {
                type: "content",
                content: { type: "text", text: 'Arguments: {"command":"ls"}' },
              },
            ],
          },
          options: [
            { optionId: "allow", name: "Allow", kind: "allow_once" },
            { optionId: "reject", name: "Reject", kind: "reject_once" },
          ],
        },
      },
    ],
    onPermissionResolved,
  });
  const strip = screen.getByTestId("canvas-prompts");
  expect(strip.closest("[data-testid=canvas-view]")).not.toBeNull();
  expect(strip.textContent).toContain("The agent is waiting for your answer");
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Allow" }));
  });
  expect(
    fetches.some(
      (f) =>
        f.url.includes("/coddy/sessions/sess_1/permission") &&
        f.init?.method === "POST",
    ),
  ).toBe(true);
  expect(onPermissionResolved).toHaveBeenCalledWith(
    "sess_1",
    "pp_1",
    expect.objectContaining({}),
  );
});

test("with nothing waiting the board shows no prompt strip", async () => {
  await renderBoard({ prompts: [] });
  expect(screen.queryByTestId("canvas-prompts")).toBeNull();
});
