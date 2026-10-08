import React from "react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";

import { Composer } from "./Composer";

/**
 * jsdom lays nothing out, so the field is given the metrics a browser would:
 * the stylesheet's type and padding, a 76px floor (two rows and the CSS
 * minimum of the docked field) and a text as tall as the test says.
 */
let contentPx = 76;
let style: HTMLStyleElement;
const spies: { mockRestore: () => void }[] = [];

beforeEach(() => {
  contentPx = 76;
  room = 500;
  style = document.createElement("style");
  style.textContent =
    "textarea#composer { box-sizing: border-box; border: 0; line-height: 22.5px; padding: 9px 44px 10px 16px; }";
  document.head.appendChild(style);
  spies.push(
    vi
      .spyOn(HTMLTextAreaElement.prototype, "scrollHeight", "get")
      .mockImplementation(function (this: HTMLTextAreaElement) {
        return Math.max(contentPx, fieldHeight(this));
      }),
  );
  spies.push(
    vi
      .spyOn(HTMLTextAreaElement.prototype, "getBoundingClientRect")
      .mockImplementation(function (this: HTMLTextAreaElement) {
        const h = fieldHeight(this);
        return {
          x: 0,
          y: 0,
          top: 0,
          left: 0,
          right: 600,
          bottom: h,
          width: 600,
          height: h,
          toJSON: () => ({}),
        } as DOMRect;
      }),
  );
});

afterEach(() => {
  cleanup();
  style.remove();
  for (const spy of spies.splice(0)) spy.mockRestore();
});

function fieldHeight(el: HTMLTextAreaElement): number {
  const set = parseFloat(el.style.height);
  return Number.isFinite(set) ? set : 76;
}

function docked(
  value: string,
  o: { onSend?: (text: string) => void; sessionId?: string } = {},
) {
  return (
    <Composer
      value={value}
      isEmpty={false}
      sessionId={o.sessionId ?? "sess_a"}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={o.onSend ?? (() => {})}
      expandRoomPx={() => room}
    />
  );
}

/** The room above the docked block a test gives the composer. */
let room = 500;

function field(): HTMLTextAreaElement {
  return document.getElementById("composer") as HTMLTextAreaElement;
}

test("the composer field grows with its text up to a ceiling and scrolls past it", () => {
  const { rerender } = render(docked("one line"));
  expect(field().style.height).toBe("76px");

  contentPx = 154;
  rerender(docked("five lines\n2\n3\n4\n5"));
  expect(field().style.height).toBe("154px");

  // Eight lines of 22.5px and the padding: the text scrolls past them.
  contentPx = 1026;
  rerender(docked("a long prompt\n".repeat(40)));
  expect(field().style.height).toBe("199px");

  contentPx = 41.5;
  rerender(docked(""));
  expect(field().style.height).toBe("76px");
});

test("the expand control gives the field the chat under its header, and a send folds it back", () => {
  const onSend = vi.fn();
  contentPx = 100;
  render(docked("a draft\nof\nthree lines", { onSend }));
  expect(field().style.height).toBe("100px");

  const expand = screen.getByTestId("composer-expand");
  expect(expand).toHaveAccessibleName("Expand the message field");
  expect(expand).toHaveAttribute("aria-pressed", "false");
  fireEvent.click(expand);
  // 500px of room above the docked block at the field's floor.
  expect(field().style.height).toBe("576px");
  expect(expand).toHaveAccessibleName("Collapse the message field");
  expect(expand).toHaveAttribute("aria-pressed", "true");

  // Escape in the field folds it back, and so does the control.
  fireEvent.keyDown(field(), { key: "Escape" });
  expect(field().style.height).toBe("100px");
  fireEvent.click(expand);
  expect(field().style.height).toBe("576px");
  fireEvent.click(expand);
  expect(field().style.height).toBe("100px");

  // Sending folds it.
  fireEvent.click(expand);
  fireEvent.keyDown(field(), { key: "Enter" });
  expect(onSend).toHaveBeenCalledWith("a draft\nof\nthree lines");
  expect(field().style.height).toBe("100px");
});

test("another chat opens with the field folded", () => {
  contentPx = 100;
  const { rerender } = render(docked("draft"));
  fireEvent.click(screen.getByTestId("composer-expand"));
  expect(field().style.height).toBe("576px");
  rerender(docked("draft", { sessionId: "sess_b" }));
  expect(field().style.height).toBe("100px");
});

test("the start screen grows with its text and has no expand control", () => {
  contentPx = 154;
  render(
    <Composer
      value={"a\nb\nc\nd\ne"}
      isEmpty={true}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
    />,
  );
  expect(screen.queryByTestId("composer-expand")).toBeNull();
  expect(field().style.height).toBe("154px");
});

test("an expanded field gives way to what grows in the docked block beside it", () => {
  // The docked block's observer: a usage banner rising into it changes its
  // height without a draft change or a resize of the window.
  const blockObservers = new Map<Element, (h: number) => void>();
  vi.stubGlobal(
    "ResizeObserver",
    class {
      private cb: (
        entries: { contentRect: { height: number; width: number } }[],
      ) => void;
      constructor(
        cb: (
          entries: { contentRect: { height: number; width: number } }[],
        ) => void,
      ) {
        this.cb = cb;
      }
      observe(el: Element) {
        blockObservers.set(el, (h) =>
          this.cb([{ contentRect: { height: h, width: 600 } }]),
        );
      }
      disconnect() {}
    },
  );
  try {
    contentPx = 100;
    render(docked("draft"));
    fireEvent.click(screen.getByTestId("composer-expand"));
    expect(field().style.height).toBe("576px");
    const block = document.querySelector(".composer-wrap")!;
    const resize = blockObservers.get(block)!;
    act(() => resize(700));
    // A 60px banner: 60px less room above the block.
    room = 440;
    act(() => resize(760));
    expect(field().style.height).toBe("516px");
  } finally {
    vi.unstubAllGlobals();
  }
});

test("queueing the prompt while a turn runs folds the field too", () => {
  const onQueue = vi.fn();
  contentPx = 100;
  render(
    <Composer
      value="steer it this way"
      isEmpty={false}
      sessionId="sess_a"
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
      generating
      onStop={() => {}}
      queuedMessages={[]}
      onQueue={onQueue}
      queueMode="steer"
      onCancelQueued={() => {}}
      expandRoomPx={() => room}
    />,
  );
  fireEvent.click(screen.getByTestId("composer-expand"));
  expect(field().style.height).toBe("576px");
  fireEvent.click(screen.getByRole("button", { name: "Queue this message" }));
  expect(onQueue).toHaveBeenCalledWith("steer it this way", "steer", []);
  expect(field().style.height).toBe("100px");
});

test("a field grown by itself gives way to what grows in the docked block", () => {
  const blockObservers = new Map<Element, (h: number) => void>();
  vi.stubGlobal(
    "ResizeObserver",
    class {
      private cb: (
        entries: { contentRect: { height: number; width: number } }[],
      ) => void;
      constructor(
        cb: (
          entries: { contentRect: { height: number; width: number } }[],
        ) => void,
      ) {
        this.cb = cb;
      }
      observe(el: Element) {
        blockObservers.set(el, (h) =>
          this.cb([{ contentRect: { height: h, width: 600 } }]),
        );
      }
      disconnect() {}
    },
  );
  try {
    // A phone held sideways: 150px of room above the block, a draft of
    // 156px - grown by itself, short of that room.
    room = 150;
    contentPx = 156;
    render(docked("five\nlines\nof\na\ndraft"));
    expect(field().style.height).toBe("156px");
    const resize = blockObservers.get(
      document.querySelector(".composer-wrap")!,
    )!;
    act(() => resize(300));
    // A 100px banner rises into the block: 50px of room are left.
    room = 50;
    act(() => resize(400));
    expect(field().style.height).toBe("126px");
  } finally {
    vi.unstubAllGlobals();
  }
});
