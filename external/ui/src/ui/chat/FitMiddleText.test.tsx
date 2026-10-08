import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

import { FitMiddleText } from "./FitMiddleText";

/**
 * A branch name on the plate is shown whole while it fits and cut in the
 * middle only when it does not: as much of it as the room allows, both ends
 * kept (DESIGN.md, Bar over the composer).
 *
 * jsdom has no layout, so the host plays a flex item that shrinks to the room
 * it is given: its content is 7px a character, its width the content's or the
 * room, whichever is smaller.
 */

const CHAR = 7;
let room = 1000;
let observed: ResizeObserverCallback[] = [];

beforeEach(() => {
  room = 1000;
  observed = [];
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(cb: ResizeObserverCallback) {
        observed.push(cb);
      }
      observe() {}
      disconnect() {}
    },
  );
  const content = (el: HTMLElement) => (el.textContent ?? "").length * CHAR;
  vi.spyOn(HTMLElement.prototype, "scrollWidth", "get").mockImplementation(
    function (this: HTMLElement) {
      return content(this);
    },
  );
  vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockImplementation(
    function (this: HTMLElement) {
      return Math.min(content(this), room);
    },
  );
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const BRANCH = "fix/skill-install-without-git";

function Host() {
  return (
    <span data-testid="host">
      <FitMiddleText text={BRANCH} />
    </span>
  );
}

test("a name that fits is shown whole, however long", () => {
  render(<Host />);
  expect(screen.getByTestId("host").textContent).toBe(BRANCH);
});

test("a name that does not fit keeps as much of both ends as the room holds", () => {
  room = 20 * CHAR;
  render(<Host />);
  const shown = screen.getByTestId("host").textContent ?? "";
  expect(shown).toContain("…");
  expect(shown.length).toBe(20);
  expect(shown.startsWith("fix/")).toBe(true);
  expect(shown.endsWith("without-git")).toBe(true);
});

test("the name grows back whole when the room comes back", () => {
  room = 12 * CHAR;
  render(<Host />);
  expect(screen.getByTestId("host").textContent?.length).toBe(12);
  room = 1000;
  act(() => {
    for (const cb of observed) cb([], {} as ResizeObserver);
  });
  expect(screen.getByTestId("host").textContent).toBe(BRANCH);
});
