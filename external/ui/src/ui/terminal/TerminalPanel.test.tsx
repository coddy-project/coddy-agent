import React from "react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { TerminalPanel } from "./TerminalPanel";
import { shellStackMaxWidthMediaQuery } from "../shellBreakpoint";

const fetcher = vi.fn();

beforeEach(() => {
  localStorage.clear();
  fetcher.mockReset();
  fetcher.mockImplementation(
    (_input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "DELETE") return new Response(null, { status: 204 });
      const body = init?.method === "POST" ? "\n❯ pwd\n/work/demo\n" : "";
      return new Response(
        JSON.stringify({
          object: "coddy.terminal",
          shell: "/bin/bash",
          output: body,
          running: true,
        }),
        { headers: { "Content-Type": "application/json" } },
      );
    },
  );
  vi.stubGlobal("fetch", fetcher);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

test("runs commands, recalls them, and restarts the chat shell", async () => {
  render(
    <TerminalPanel
      sessionId="sess_terminal"
      workspacePath="/work/demo"
      onClose={() => {}}
    />,
  );
  const input = await screen.findByRole("textbox", {
    name: "Terminal command",
  });
  expect(screen.queryByRole("button", { name: "Run" })).toBeNull();
  await waitFor(() => expect(fetcher).toHaveBeenCalled());

  fireEvent.change(input, { target: { value: "pwd" } });
  fireEvent.submit(input.closest("form")!);
  await waitFor(() =>
    expect(fetcher).toHaveBeenCalledWith(
      "/coddy/sessions/sess_terminal/terminal",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ input: "pwd" }),
      }),
    ),
  );
  expect(await screen.findByText(/\/work\/demo/)).toBeTruthy();

  fireEvent.keyDown(input, { key: "ArrowUp" });
  expect((input as HTMLInputElement).value).toBe("pwd");

  fireEvent.click(screen.getByRole("button", { name: "Restart" }));
  await waitFor(() =>
    expect(fetcher).toHaveBeenCalledWith(
      "/coddy/sessions/sess_terminal/terminal",
      expect.objectContaining({ method: "DELETE" }),
    ),
  );
});

test("keeps command history when the panel is closed and reopened", async () => {
  const first = render(
    <TerminalPanel
      sessionId="sess_terminal"
      workspacePath="/work/demo"
      onClose={() => {}}
    />,
  );
  const input = await screen.findByRole("textbox", {
    name: "Terminal command",
  });
  fireEvent.change(input, { target: { value: "go test ./..." } });
  fireEvent.submit(input.closest("form")!);
  await waitFor(() =>
    expect(
      JSON.parse(
        localStorage.getItem("coddy.terminal.history.sess_terminal") || "[]",
      ),
    ).toContain("go test ./..."),
  );

  first.unmount();
  render(
    <TerminalPanel
      sessionId="sess_terminal"
      workspacePath="/work/demo"
      onClose={() => {}}
    />,
  );
  const reopened = await screen.findByRole("textbox", {
    name: "Terminal command",
  });
  fireEvent.keyDown(reopened, { key: "ArrowUp" });
  expect((reopened as HTMLInputElement).value).toBe("go test ./...");
});

/** jsdom answers no media query by itself: the shell tier is stated per test. */
function shell(stacked: boolean) {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: stacked && query === shellStackMaxWidthMediaQuery,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  }));
  vi.stubGlobal("fetch", fetcher);
}

/**
 * jsdom has no PointerEvent, so the events React would get from a browser are
 * built by hand: a plain fireEvent.pointerDown carries neither button nor
 * coordinates, and the drag would have nothing to measure from.
 */
function dragFrom(
  target: Element,
  fromX: number,
  fromY: number,
  toX: number,
  toY: number,
) {
  fireEvent(
    target,
    new MouseEvent("pointerdown", {
      bubbles: true,
      cancelable: true,
      button: 0,
      clientX: fromX,
      clientY: fromY,
    }),
  );
  fireEvent(
    window,
    new MouseEvent("pointermove", { clientX: toX, clientY: toY }),
  );
  fireEvent(window, new MouseEvent("pointerup", {}));
}

function openTerminal() {
  return render(
    <TerminalPanel
      sessionId="sess_terminal"
      workspacePath="/work/demo"
      onClose={() => {}}
    />,
  );
}

test("on the stacked shell the terminal is the whole screen and cannot be dragged", async () => {
  shell(true);
  openTerminal();
  const panel = await screen.findByTestId("terminal-panel");
  expect(panel.className).toContain("terminal-panel--full");
  // Nothing to move or resize where there is no room to put a window.
  expect(panel.getAttribute("style")).toBeNull();
  expect(screen.queryByTestId("terminal-grip-se")).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Maximize the terminal window" }),
  ).toBeNull();
});

test("on a desktop the window is placed, dragged by its head and remembered", async () => {
  shell(false);
  openTerminal();
  const panel = await screen.findByTestId("terminal-panel");
  expect(panel.className).not.toContain("terminal-panel--full");
  const before = panel.style.left;
  expect(before).toMatch(/px$/);

  dragFrom(screen.getByTestId("terminal-head"), 500, 400, 380, 300);

  expect(panel.style.left).toBe(`${parseInt(before, 10) - 120}px`);
  const kept = JSON.parse(localStorage.getItem("coddy.terminal.frame") || "{}");
  expect(kept.x).toBe(parseInt(panel.style.left, 10));
  expect(kept.y).toBe(parseInt(panel.style.top, 10));
});

test("a grip resizes the window and leaves the opposite edge where it was", async () => {
  shell(false);
  openTerminal();
  const panel = await screen.findByTestId("terminal-panel");
  const left = parseInt(panel.style.left, 10);
  const right = left + parseInt(panel.style.width, 10);

  dragFrom(screen.getByTestId("terminal-grip-w"), 300, 400, 380, 400);

  expect(parseInt(panel.style.left, 10)).toBe(left + 80);
  expect(parseInt(panel.style.left, 10) + parseInt(panel.style.width, 10)).toBe(
    right,
  );
});

test("a desktop window goes full screen and back, and is still full screen next time", async () => {
  shell(false);
  const first = openTerminal();
  fireEvent.click(
    await screen.findByRole("button", { name: "Maximize the terminal window" }),
  );
  expect(screen.getByTestId("terminal-panel").className).toContain(
    "terminal-panel--maximized",
  );
  // Full screen is the stylesheet's frame: the component places nothing.
  expect(screen.getByTestId("terminal-panel").style.left).toBe("");

  first.unmount();
  openTerminal();
  const reopened = await screen.findByTestId("terminal-panel");
  expect(reopened.className).toContain("terminal-panel--maximized");
  fireEvent.click(
    screen.getByRole("button", { name: "Restore the terminal window" }),
  );
  expect(screen.getByTestId("terminal-panel").className).not.toContain(
    "terminal-panel--maximized",
  );
});
