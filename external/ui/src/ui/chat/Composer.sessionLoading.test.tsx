import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { Composer } from "./Composer";

afterEach(() => cleanup());

// While the opened session loads, its settings included, the selectors still
// name another session's, and a prompt would carry them: Send waits, and
// nothing else of the composer does.
function renderComposer(opts: {
  sessionLoading: boolean;
  generating?: boolean;
  onSend?: (text: string) => void;
  onQueue?: (text: string, mode: "steer" | "after_turn") => void;
}) {
  return render(
    <Composer
      value="a question"
      isEmpty={false}
      sessionId="sess_1"
      mode="plan"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={opts.onSend ?? (() => {})}
      {...(opts.sessionLoading ? { sessionLoading: true } : {})}
      {...(opts.generating
        ? {
            generating: true,
            onStop: () => {},
            queuedMessages: [],
            onQueue: opts.onQueue ?? (() => {}),
            queueMode: "steer" as const,
          }
        : {})}
    />,
  );
}

test("Send is disabled while the opened session loads", () => {
  renderComposer({ sessionLoading: true });

  expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();
});

// Enter waits like the button: the draft stays in the field, untouched.
test("Enter does not send while the opened session loads", () => {
  const onSend = vi.fn();
  renderComposer({ sessionLoading: true, onSend });

  fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter" });
  expect(onSend).not.toHaveBeenCalled();
});

test("the draft is sent as usual once the session has loaded", () => {
  const onSend = vi.fn();
  renderComposer({ sessionLoading: false, onSend });

  expect(screen.getByRole("button", { name: "Send" })).toBeEnabled();
  fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter" });
  expect(onSend).toHaveBeenCalledWith("a question");
});

test("a message for a running turn still joins the queue, which carries no settings", () => {
  const onQueue = vi.fn();
  renderComposer({ sessionLoading: true, generating: true, onQueue });

  fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter" });
  expect(onQueue).toHaveBeenCalledWith("a question", "steer", []);
});
