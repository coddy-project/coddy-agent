import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { CameraStore, SETTLE_MS } from "./cameraStore";
import { boardFingerprint, CANVAS_ADDRESS_REFRESH_MS } from "./useCanvasBoard";
import type { CanvasBoard } from "./api";

/**
 * The camera of a board lives outside React: a pan or a zoom writes the world
 * layer's transform and the scale the title bars read once per animation
 * frame, and only what shows the zoom hears of it.
 */

let frames: FrameRequestCallback[] = [];

beforeEach(() => {
  frames = [];
  vi.spyOn(window, "requestAnimationFrame").mockImplementation((cb) => {
    frames.push(cb);
    return frames.length;
  });
  vi.spyOn(window, "cancelAnimationFrame").mockImplementation(() => {});
});

afterEach(() => {
  vi.useRealTimers();
  vi.mocked(window.requestAnimationFrame).mockRestore();
  vi.mocked(window.cancelAnimationFrame).mockRestore();
});

function flush() {
  const due = frames;
  frames = [];
  for (const cb of due) cb(0);
}

test("moves are written once per frame, the last one winning", () => {
  const store = new CameraStore();
  const world = document.createElement("div");
  const board = document.createElement("div");
  store.attach(world, board);
  const heard = vi.fn();
  store.subscribe(heard);
  store.move({ x: 10, y: 0, scale: 1 });
  store.move({ x: 20, y: 5, scale: 2 });
  expect(frames).toHaveLength(1);
  expect(world.style.transform).toBe("translate(0px, 0px) scale(1)");
  flush();
  expect(world.style.transform).toBe("translate(20px, 5px) scale(2)");
  expect(board.style.getPropertyValue("--canvas-scale")).toBe("2");
  expect(board.style.getPropertyValue("--canvas-inv")).toBe("0.5");
  expect(heard).toHaveBeenCalledTimes(1);
  // The same camera again is no frame at all.
  store.move({ x: 20, y: 5, scale: 2 });
  expect(frames).toHaveLength(0);
});

test("the level reads the fit glyph until the person moves the board", () => {
  const store = new CameraStore();
  expect(store.fitted()).toBe(true);
  expect(store.percent()).toBeNull();
  store.fitTo({ x: 0, y: 0, scale: 0.42 });
  expect(store.percent()).toBeNull();
  store.move((c) => ({ ...c, scale: 0.5 }));
  expect(store.fitted()).toBe(false);
  expect(store.percent()).toBe(50);
});

test("a moving board is one layer until it rests", () => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  const store = new CameraStore();
  const board = document.createElement("div");
  store.attach(document.createElement("div"), board);
  store.move({ x: 1, y: 0, scale: 1 });
  expect(board.classList.contains("is-moving")).toBe(true);
  vi.advanceTimersByTime(SETTLE_MS - 1);
  store.move({ x: 2, y: 0, scale: 1 });
  vi.advanceTimersByTime(SETTLE_MS - 1);
  expect(board.classList.contains("is-moving")).toBe(true);
  vi.advanceTimersByTime(2);
  expect(board.classList.contains("is-moving")).toBe(false);
  // A fit is not a move of the person's: the board does not turn into a layer.
  store.fitTo({ x: 0, y: 0, scale: 1 });
  expect(board.classList.contains("is-moving")).toBe(false);
});

test("a board read again differs only when something on it changed", () => {
  const b = (url: string, x = 0): CanvasBoard => ({
    session_id: "s",
    file: "a.canvas",
    current: true,
    revision: 1,
    stamp: "f",
    frames: [
      {
        id: "n",
        path: "p/index.html",
        root: "p",
        width: 1280,
        height: 800,
        x,
        y: 0,
        gen: 1,
        stamp: "s",
        url,
        expires_at: url,
      },
    ],
    items: [],
    edges: [],
  });
  const now = 5 * CANVAS_ADDRESS_REFRESH_MS;
  expect(boardFingerprint(b("/a"), now)).toBe(boardFingerprint(b("/b"), now));
  expect(boardFingerprint(b("/a"), now)).not.toBe(
    boardFingerprint(b("/a", 10), now),
  );
  // Well before an address nears its end, the board takes fresh ones.
  expect(boardFingerprint(b("/a"), now)).not.toBe(
    boardFingerprint(b("/a"), now + CANVAS_ADDRESS_REFRESH_MS),
  );
});
