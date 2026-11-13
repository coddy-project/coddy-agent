import { beforeEach, expect, test } from "vitest";
import {
  clampTerminalFrame,
  defaultTerminalFrame,
  moveTerminalFrame,
  readTerminalFrame,
  readTerminalMaximized,
  resizeTerminalFrame,
  TERMINAL_INSET,
  TERMINAL_MIN_HEIGHT,
  TERMINAL_MIN_WIDTH,
  writeTerminalFrame,
  writeTerminalMaximized,
} from "./terminalFrame";

beforeEach(() => localStorage.clear());

test("a new window opens whole, over the end of the chat", () => {
  const frame = defaultTerminalFrame(1280, 800);
  expect(frame.w).toBeLessThanOrEqual(1280 - TERMINAL_INSET * 2);
  expect(frame.h).toBeLessThanOrEqual(800 - TERMINAL_INSET * 2);
  expect(frame.x + frame.w).toBeLessThanOrEqual(1280 - TERMINAL_INSET);
  expect(frame.y + frame.h).toBeLessThanOrEqual(800 - TERMINAL_INSET);
  // Towards the bottom right corner, clear of the rail on the left.
  expect(1280 - (frame.x + frame.w)).toBeLessThan(frame.x);
  expect(800 - (frame.y + frame.h)).toBeLessThan(frame.y);
});

test("a window remembered from a larger screen opens inside a smaller one", () => {
  const frame = clampTerminalFrame(
    { x: 1800, y: 1200, w: 900, h: 700 },
    1024,
    640,
  );
  expect(frame.x).toBeGreaterThanOrEqual(TERMINAL_INSET);
  expect(frame.y).toBeGreaterThanOrEqual(TERMINAL_INSET);
  expect(frame.x + frame.w).toBeLessThanOrEqual(1024 - TERMINAL_INSET);
  expect(frame.y + frame.h).toBeLessThanOrEqual(640 - TERMINAL_INSET);
});

test("a window is never smaller than a command line it can be read on", () => {
  const frame = clampTerminalFrame({ x: 40, y: 40, w: 10, h: 10 }, 1280, 800);
  expect(frame.w).toBe(TERMINAL_MIN_WIDTH);
  expect(frame.h).toBe(TERMINAL_MIN_HEIGHT);
});

test("a drag past an edge stops at it instead of leaving the screen", () => {
  const start = { x: 200, y: 200, w: 400, h: 300 };
  const far = moveTerminalFrame(start, 5000, 5000, 1280, 800);
  expect(far.x + far.w).toBe(1280 - TERMINAL_INSET);
  expect(far.y + far.h).toBe(800 - TERMINAL_INSET);
  const near = moveTerminalFrame(start, -5000, -5000, 1280, 800);
  expect(near.x).toBe(TERMINAL_INSET);
  expect(near.y).toBe(TERMINAL_INSET);
  expect(near.w).toBe(start.w);
  expect(near.h).toBe(start.h);
});

test("resizing one edge leaves the opposite one where it was", () => {
  const start = { x: 300, y: 200, w: 400, h: 300 };
  const grown = resizeTerminalFrame(start, "nw", -100, -50, 1280, 800);
  expect(grown.x).toBe(200);
  expect(grown.y).toBe(150);
  expect(grown.x + grown.w).toBe(start.x + start.w);
  expect(grown.y + grown.h).toBe(start.y + start.h);

  const widened = resizeTerminalFrame(start, "se", 120, 90, 1280, 800);
  expect(widened.x).toBe(start.x);
  expect(widened.y).toBe(start.y);
  expect(widened.w).toBe(start.w + 120);
  expect(widened.h).toBe(start.h + 90);
});

test("a resize dragged past the minimum stops rather than turning inside out", () => {
  const start = { x: 300, y: 200, w: 400, h: 300 };
  const squashed = resizeTerminalFrame(start, "nw", 900, 900, 1280, 800);
  expect(squashed.w).toBe(TERMINAL_MIN_WIDTH);
  expect(squashed.h).toBe(TERMINAL_MIN_HEIGHT);
  expect(squashed.x + squashed.w).toBe(start.x + start.w);
  expect(squashed.y + squashed.h).toBe(start.y + start.h);
});

test("the frame and the maximized state survive a reload, and bad storage does not", () => {
  writeTerminalFrame({ x: 10, y: 20, w: 500, h: 400 });
  expect(readTerminalFrame()).toEqual({ x: 10, y: 20, w: 500, h: 400 });
  writeTerminalMaximized(true);
  expect(readTerminalMaximized()).toBe(true);
  writeTerminalMaximized(false);
  expect(readTerminalMaximized()).toBe(false);

  localStorage.setItem("coddy.terminal.frame", "{not json");
  expect(readTerminalFrame()).toBeNull();
  localStorage.setItem("coddy.terminal.frame", JSON.stringify({ x: 1, y: 2 }));
  expect(readTerminalFrame()).toBeNull();
});
