/*
 * Where the terminal window stands and how big it is.
 *
 * The arithmetic is here rather than in the component because it is what a
 * drag, a resize, a reload and a window the viewport outgrew all share: a
 * frame that was saved on a 2560px monitor must not leave the window off a
 * laptop's screen, and a resize must not push an edge past the one it is
 * anchored to. None of it needs the DOM, so all of it is tested.
 */

export type TerminalFrame = { x: number; y: number; w: number; h: number };

/** Narrower than this a command line stops being readable. */
export const TERMINAL_MIN_WIDTH = 320;
/** Below this the output area is shorter than the head above it. */
export const TERMINAL_MIN_HEIGHT = 200;
/** The distance every other window of the shell keeps from the viewport. */
export const TERMINAL_INSET = 14;

const DEFAULT_WIDTH = 720;
const DEFAULT_HEIGHT = 420;
/** Where a window first stands: clear of the rail, over the end of the chat. */
const DEFAULT_MARGIN = 24;

const FRAME_KEY = "coddy.terminal.frame";
const MAXIMIZED_KEY = "coddy.terminal.maximized";

/** Which edges a drag moves. "se" is the bottom-right corner, "n" the top edge. */
export type TerminalEdge = "n" | "s" | "e" | "w" | "ne" | "nw" | "se" | "sw";

export const TERMINAL_EDGES: TerminalEdge[] = [
  "n",
  "s",
  "e",
  "w",
  "ne",
  "nw",
  "se",
  "sw",
];

/** The frame of a window opened for the first time in this browser. */
export function defaultTerminalFrame(vw: number, vh: number): TerminalFrame {
  const w = Math.min(DEFAULT_WIDTH, Math.max(0, vw - DEFAULT_MARGIN * 2));
  const h = Math.min(DEFAULT_HEIGHT, Math.max(0, vh - DEFAULT_MARGIN * 2));
  return clampTerminalFrame(
    { x: vw - w - DEFAULT_MARGIN, y: vh - h - DEFAULT_MARGIN, w, h },
    vw,
    vh,
  );
}

/**
 * Brings a frame back inside the viewport: no edge past an inset, no smaller
 * than the minimum, and never larger than the room there is - a 720px window
 * remembered from a monitor opens narrowed on a laptop rather than off it.
 */
export function clampTerminalFrame(
  frame: TerminalFrame,
  vw: number,
  vh: number,
): TerminalFrame {
  const roomW = Math.max(0, vw - TERMINAL_INSET * 2);
  const roomH = Math.max(0, vh - TERMINAL_INSET * 2);
  const w = Math.min(Math.max(frame.w, TERMINAL_MIN_WIDTH), roomW || frame.w);
  const h = Math.min(Math.max(frame.h, TERMINAL_MIN_HEIGHT), roomH || frame.h);
  const x = Math.min(
    Math.max(frame.x, TERMINAL_INSET),
    Math.max(TERMINAL_INSET, vw - w - TERMINAL_INSET),
  );
  const y = Math.min(
    Math.max(frame.y, TERMINAL_INSET),
    Math.max(TERMINAL_INSET, vh - h - TERMINAL_INSET),
  );
  return {
    x: Math.round(x),
    y: Math.round(y),
    w: Math.round(w),
    h: Math.round(h),
  };
}

/** The frame a pointer moved by (dx, dy) from where the drag started. */
export function moveTerminalFrame(
  start: TerminalFrame,
  dx: number,
  dy: number,
  vw: number,
  vh: number,
): TerminalFrame {
  return clampTerminalFrame(
    { ...start, x: start.x + dx, y: start.y + dy },
    vw,
    vh,
  );
}

/**
 * The frame after dragging one edge or corner. The opposite edge stays where
 * it was, so a window grown from its top-left corner keeps its bottom-right
 * one, and a drag past the minimum size stops instead of turning the window
 * inside out.
 */
export function resizeTerminalFrame(
  start: TerminalFrame,
  edge: TerminalEdge,
  dx: number,
  dy: number,
  vw: number,
  vh: number,
): TerminalFrame {
  let { x, y, w, h } = start;
  if (edge.includes("e")) w = start.w + dx;
  if (edge.includes("s")) h = start.h + dy;
  if (edge.includes("w")) {
    const right = start.x + start.w;
    x = Math.min(start.x + dx, right - TERMINAL_MIN_WIDTH);
    w = right - x;
  }
  if (edge.includes("n")) {
    const bottom = start.y + start.h;
    y = Math.min(start.y + dy, bottom - TERMINAL_MIN_HEIGHT);
    h = bottom - y;
  }
  return clampTerminalFrame({ x, y, w, h }, vw, vh);
}

/** The frame kept for this browser, or null when there is none to read. */
export function readTerminalFrame(): TerminalFrame | null {
  try {
    const raw = localStorage.getItem(FRAME_KEY);
    if (!raw) return null;
    const value = JSON.parse(raw) as Partial<TerminalFrame>;
    const nums = [value.x, value.y, value.w, value.h];
    if (!nums.every((n) => typeof n === "number" && Number.isFinite(n))) {
      return null;
    }
    return { x: value.x!, y: value.y!, w: value.w!, h: value.h! };
  } catch {
    return null;
  }
}

export function writeTerminalFrame(frame: TerminalFrame): void {
  try {
    localStorage.setItem(FRAME_KEY, JSON.stringify(frame));
  } catch {
    // A private or full storage must not stop the window from being moved.
  }
}

export function readTerminalMaximized(): boolean {
  try {
    return localStorage.getItem(MAXIMIZED_KEY) === "1";
  } catch {
    return false;
  }
}

export function writeTerminalMaximized(maximized: boolean): void {
  try {
    localStorage.setItem(MAXIMIZED_KEY, maximized ? "1" : "0");
  } catch {
    // As above: the window still opens, it just forgets how it was left.
  }
}
