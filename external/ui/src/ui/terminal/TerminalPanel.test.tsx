import React from "react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  act,
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

test("walking the history gives the draft back and never wipes it", async () => {
  shell(false);
  openTerminal();
  const input = await screen.findByRole("textbox", {
    name: "Terminal command",
  });

  // With nothing recorded the arrows belong to the field.
  fireEvent.change(input, { target: { value: "kubectl get pods --watch" } });
  fireEvent.keyDown(input, { key: "ArrowDown" });
  fireEvent.keyDown(input, { key: "ArrowUp" });
  expect((input as HTMLInputElement).value).toBe("kubectl get pods --watch");

  // One command recorded: Up recalls it, Down gives the draft back.
  fireEvent.change(input, { target: { value: "pwd" } });
  fireEvent.submit(input.closest("form")!);
  await waitFor(() => expect((input as HTMLInputElement).value).toBe(""));
  fireEvent.change(input, { target: { value: "half a command" } });
  fireEvent.keyDown(input, { key: "ArrowUp" });
  expect((input as HTMLInputElement).value).toBe("pwd");
  fireEvent.keyDown(input, { key: "ArrowDown" });
  expect((input as HTMLInputElement).value).toBe("half a command");
});

test("a command the server refused stays in the field and is not recorded", async () => {
  shell(false);
  fetcher.mockImplementation((_i: RequestInfo | URL, init?: RequestInit) => {
    if (init?.method === "POST") {
      return new Response('{"error":{"message":"a command is one line"}}', {
        status: 409,
      });
    }
    return new Response(
      JSON.stringify({
        object: "coddy.terminal",
        shell: "/bin/bash",
        output: "",
        running: true,
      }),
      { headers: { "Content-Type": "application/json" } },
    );
  });
  openTerminal();
  const input = await screen.findByRole("textbox", {
    name: "Terminal command",
  });

  fireEvent.change(input, { target: { value: "rm -rf important" } });
  fireEvent.submit(input.closest("form")!);

  expect(await screen.findByRole("alert")).toBeTruthy();
  // The command is still where the person can look at it and try again, and a
  // command that never ran is not in the history Up walks.
  expect((input as HTMLInputElement).value).toBe("rm -rf important");
  expect(
    localStorage.getItem("coddy.terminal.history.sess_terminal"),
  ).toBeNull();
});

test("an answer older than the one on screen is dropped", async () => {
  shell(false);
  let hold: ((value: Response) => void) | null = null;
  fetcher.mockImplementation((_i: RequestInfo | URL, init?: RequestInit) => {
    const body = (output: string) =>
      new Response(
        JSON.stringify({
          object: "coddy.terminal",
          shell: "/bin/bash",
          output,
          running: true,
        }),
        { headers: { "Content-Type": "application/json" } },
      );
    if (init?.method === "POST") return body("the new shell\n");
    // The first read is held until after the command has answered.
    if (!hold) return new Promise<Response>((r) => (hold = r));
    return body("stale\n");
  });
  openTerminal();
  const input = await screen.findByRole("textbox", {
    name: "Terminal command",
  });
  fireEvent.change(input, { target: { value: "pwd" } });
  fireEvent.submit(input.closest("form")!);
  expect(await screen.findByText(/the new shell/)).toBeTruthy();

  await act(async () => {
    hold?.(
      new Response(
        JSON.stringify({
          object: "coddy.terminal",
          shell: "/bin/bash",
          output: "stale\n",
          running: true,
        }),
        { headers: { "Content-Type": "application/json" } },
      ),
    );
    await Promise.resolve();
  });
  expect(screen.queryByText(/stale/)).toBeNull();
  expect(screen.getByText(/the new shell/)).toBeTruthy();
});

test("a drag ignores a second pointer and ends when the window is taken away", async () => {
  shell(false);
  const view = openTerminal();
  const panel = await screen.findByTestId("terminal-panel");
  const left = parseInt(panel.style.left, 10);

  const down = new MouseEvent("pointerdown", {
    bubbles: true,
    cancelable: true,
    button: 0,
    clientX: 500,
    clientY: 400,
  });
  Object.defineProperty(down, "pointerId", { value: 1 });
  fireEvent(screen.getByTestId("terminal-head"), down);

  // Another pointer's move belongs to that pointer, not to this drag.
  const other = new MouseEvent("pointermove", { clientX: 100, clientY: 100 });
  Object.defineProperty(other, "pointerId", { value: 99 });
  fireEvent(window, other);
  expect(parseInt(panel.style.left, 10)).toBe(left);

  const mine = new MouseEvent("pointermove", { clientX: 450, clientY: 400 });
  Object.defineProperty(mine, "pointerId", { value: 1 });
  fireEvent(window, mine);
  expect(parseInt(panel.style.left, 10)).toBe(left - 50);

  // Unmounting mid-drag leaves nothing listening on the document.
  view.unmount();
  expect(() =>
    fireEvent(
      window,
      new MouseEvent("pointermove", { clientX: 10, clientY: 10 }),
    ),
  ).not.toThrow();
});
