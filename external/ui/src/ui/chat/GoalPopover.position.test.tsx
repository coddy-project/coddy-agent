import { cleanup, render, screen } from "@testing-library/react";
import { useRef } from "react";
import { afterEach, expect, test, vi } from "vitest";

import { ConfirmProvider } from "../components/useConfirm";
import { GoalPopover } from "./GoalPopover";
import { parseSessionGoal } from "./goal";

/**
 * On a desktop the goal menu opens over the goal mark on the plate and keeps
 * to the composer card: its right edge is the card's, so it does not reach
 * past the card toward the window's edge (DESIGN.md, Session goal).
 */

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function rect(left: number, top: number, right: number, bottom: number) {
  return {
    left,
    top,
    right,
    bottom,
    width: right - left,
    height: bottom - top,
    x: left,
    y: top,
    toJSON: () => ({}),
  } as DOMRect;
}

function Stand() {
  const mark = useRef<HTMLButtonElement | null>(null);
  const card = useRef<HTMLDivElement | null>(null);
  return (
    <ConfirmProvider>
      <div ref={card} data-testid="card">
        <button ref={mark} data-testid="mark" />
      </div>
      <GoalPopover
        open
        onClose={() => {}}
        goal={parseSessionGoal({ objective: "Ship it", status: "active" })}
        useSheet={false}
        anchorRef={mark}
        alignRef={card}
        generating={false}
        actions={{
          pause: async () => true,
          clear: async () => true,
          sendPrompt: () => {},
        }}
      />
    </ConfirmProvider>
  );
}

test("the menu's right edge is the composer card's, above the mark", () => {
  vi.spyOn(window, "innerWidth", "get").mockReturnValue(1280);
  vi.spyOn(window, "innerHeight", "get").mockReturnValue(800);
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(
    function (this: HTMLElement) {
      if (this.dataset.testid === "mark") return rect(1051, 611, 1073, 633);
      if (this.dataset.testid === "card") return rect(222, 605, 1142, 785);
      return rect(0, 0, 0, 0);
    },
  );
  render(<Stand />);
  const panel = screen.getByTestId("goal-popover");
  expect(panel.style.width).toBe("400px");
  expect(panel.style.left).toBe(`${1142 - 400}px`);
  expect(panel.style.bottom).toBe(`${800 - 611 + 8}px`);
});
