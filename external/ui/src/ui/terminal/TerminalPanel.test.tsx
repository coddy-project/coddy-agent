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
