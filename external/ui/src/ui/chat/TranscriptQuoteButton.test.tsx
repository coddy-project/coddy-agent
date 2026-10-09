import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";

import { I18nProvider } from "../i18n/I18nProvider";
import { TranscriptQuoteButton } from "./TranscriptQuoteButton";

afterEach(() => {
  cleanup();
  document.getSelection()?.removeAllRanges();
});

/** An answer with its text selected, and the button offering it. */
async function offered(
  onQuote: (text: string) => void,
  watch?: () => (Element | null)[],
) {
  const restore = Object.getOwnPropertyDescriptor(
    Range.prototype,
    "getBoundingClientRect",
  );
  Object.defineProperty(Range.prototype, "getBoundingClientRect", {
    configurable: true,
    value: () =>
      ({
        top: 300,
        bottom: 320,
        left: 100,
        right: 300,
        width: 200,
        height: 20,
      }) as DOMRect,
  });
  const root = document.createElement("div");
  root.className = "messages-inner";
  root.innerHTML = "<p>The stream must flush after every frame.</p>";
  document.body.appendChild(root);
  render(
    <I18nProvider>
      <TranscriptQuoteButton
        root={() => root}
        topLimit={() => 0}
        bottomLimit={() => 800}
        touch
        onQuote={onQuote}
        {...(watch ? { watch } : {})}
      />
    </I18nProvider>,
  );
  const range = document.createRange();
  range.selectNodeContents(root.querySelector("p")!);
  document.getSelection()!.addRange(range);
  act(() => {
    document.dispatchEvent(new Event("selectionchange"));
  });
  const button = await screen.findByRole("button", { name: "Quote" });
  return {
    button,
    done: () => {
      root.remove();
      if (restore)
        Object.defineProperty(
          Range.prototype,
          "getBoundingClientRect",
          restore,
        );
      else
        delete (Range.prototype as { getBoundingClientRect?: unknown })
          .getBoundingClientRect;
    },
  };
}

function pointerDown(target: HTMLElement, pointerType: string): Event {
  const ev = new MouseEvent("pointerdown", {
    bubbles: true,
    cancelable: true,
    button: 0,
  });
  Object.defineProperty(ev, "pointerId", { value: 1 });
  Object.defineProperty(ev, "pointerType", { value: pointerType });
  act(() => {
    target.dispatchEvent(ev);
  });
  return ev;
}

// WebKit sends no click after a touch whose pointerdown was cancelled: a tap
// on Quote did nothing on an iPhone while a mouse in every engine worked.
test("a finger's press is left alone so the tap still clicks; a mouse's keeps the selection", async () => {
  const onQuote = vi.fn();
  const { button, done } = await offered(onQuote);
  try {
    expect(pointerDown(button, "touch").defaultPrevented).toBe(false);
    expect(pointerDown(button, "mouse").defaultPrevented).toBe(true);
    act(() => {
      button.click();
    });
    expect(onQuote).toHaveBeenCalledWith(
      "The stream must flush after every frame.",
    );
  } finally {
    done();
  }
});

// The docked block shrinking (a long draft cleared) or the transcript growing
// moves the selection without a scroll event: the button followed none of it
// and stood where the selection had been.
test("a change of size of what the button keeps clear of places it again", async () => {
  const observed = new Map<Element, () => void>();
  vi.stubGlobal(
    "ResizeObserver",
    class {
      private cb: () => void;
      constructor(cb: () => void) {
        this.cb = cb;
      }
      observe(el: Element) {
        observed.set(el, this.cb);
      }
      disconnect() {}
    },
  );
  const dock = document.createElement("div");
  try {
    const { button, done } = await offered(
      () => {},
      () => [dock],
    );
    try {
      expect(button.style.top).toBe("330px");
      // The docked block is what the button watches while it is out. The
      // observer is set up by an effect after the button shows, which the
      // button's own appearance does not wait for: about one run in ten
      // asked before it had run.
      await waitFor(() => expect(observed.has(dock)).toBe(true));
      Object.defineProperty(Range.prototype, "getBoundingClientRect", {
        configurable: true,
        value: () =>
          ({
            top: 400,
            bottom: 420,
            left: 100,
            right: 300,
            width: 200,
            height: 20,
          }) as DOMRect,
      });
      await act(async () => {
        observed.get(dock)!();
        await new Promise((r) => requestAnimationFrame(() => r(null)));
      });
      expect(screen.getByRole("button", { name: "Quote" }).style.top).toBe(
        "430px",
      );
    } finally {
      done();
    }
  } finally {
    vi.unstubAllGlobals();
  }
});
