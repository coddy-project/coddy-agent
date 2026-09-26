import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { Composer } from "./Composer";

afterEach(() => cleanup());

// While a turn runs, the composer is not a dead end: a draft with text in it is
// a follow-up for the queue, an empty one still leaves the Stop control.
function renderGenerating(opts: {
  value: string;
  queued?: { id: string; text: string }[];
  onQueue?: (text: string, mode: "steer" | "after_turn", files?: File[]) => void;
  onStop?: () => void;
  onCancelQueued?: (id: string) => void;
}) {
  return render(
    <Composer
      value={opts.value}
      isEmpty={false}
      mode="agent"
      modes={["agent", "plan"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
      generating={true}
      onStop={opts.onStop ?? (() => {})}
      queuedMessages={opts.queued ?? []}
      onQueue={opts.onQueue ?? (() => {})}
      queueMode="steer"
      onCancelQueued={opts.onCancelQueued ?? (() => {})}
    />,
  );
}

test("the primary control queues the draft while a turn runs", () => {
  const onQueue = vi.fn();
  const onStop = vi.fn();
  renderGenerating({ value: "check the Windows path too", onQueue, onStop });

  const btn = screen.getByRole("button", { name: "Queue this message" });
  expect(btn).toHaveAttribute("data-queue", "true");
  fireEvent.click(btn);

  expect(onQueue).toHaveBeenCalledWith("check the Windows path too", "steer", []);
  expect(onStop).not.toHaveBeenCalled();
});

test("an empty draft leaves the control as Stop", () => {
  const onQueue = vi.fn();
  const onStop = vi.fn();
  renderGenerating({ value: "   ", onQueue, onStop });

  const btn = screen.getByRole("button", { name: "Stop generation" });
  expect(btn).not.toHaveAttribute("data-queue");
  fireEvent.click(btn);

  expect(onStop).toHaveBeenCalledTimes(1);
  expect(onQueue).not.toHaveBeenCalled();
});

test("Enter queues the draft instead of being swallowed", () => {
  const onQueue = vi.fn();
  renderGenerating({ value: "one more thing", onQueue });

  fireEvent.keyDown(screen.getByRole("textbox"), { key: "Enter" });

  expect(onQueue).toHaveBeenCalledWith("one more thing", "steer", []);
});

test("first queued message asks for the Enter preference once", () => {
  const onQueue = vi.fn();
  const onQueueModeChange = vi.fn();
  render(<Composer value="check this" isEmpty={false} mode="agent" modes={["agent"]} generating={true} onModeChange={() => {}} onChange={() => {}} onSend={() => {}} onQueue={onQueue} onQueueModeChange={onQueueModeChange} />);
  fireEvent.keyDown(screen.getByRole("textbox", { name: "Message" }), { key: "Enter" });
  expect(onQueue).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "After this turn" }));
  expect(onQueueModeChange).toHaveBeenCalledWith("after_turn");
  expect(onQueue).toHaveBeenCalledWith("check this", "after_turn", []);
});

test("queued messages are listed in order, each with its own remove control", () => {
  const onCancelQueued = vi.fn();
  renderGenerating({
    value: "",
    queued: [
      { id: "q_1", text: "first correction" },
      { id: "q_2", text: "second correction" },
    ],
    onCancelQueued,
  });

  const rows = screen.getAllByTestId("composer-queue-item");
  expect(rows).toHaveLength(2);
  expect(rows[0]).toHaveTextContent("first correction");
  expect(rows[1]).toHaveTextContent("second correction");

  fireEvent.click(screen.getByTestId("composer-queue-remove-q_2"));
  expect(onCancelQueued).toHaveBeenCalledWith("q_2");
});

test("nothing queued renders no list at all", () => {
  renderGenerating({ value: "", queued: [] });
  expect(screen.queryByTestId("composer-queue")).toBeNull();
});

test("the placeholder says a draft joins the running turn", () => {
  renderGenerating({ value: "" });
  expect(screen.getByRole("textbox")).toHaveAttribute(
    "placeholder",
    "Add a follow-up for the running turn",
  );
});

test("a first message sent with Tab still goes the other way once Enter's mode is chosen", () => {
  const onQueue = vi.fn();
  const onQueueModeChange = vi.fn();
  render(<Composer value="review the answer" isEmpty={false} mode="agent" modes={["agent"]} generating={true} onModeChange={() => {}} onChange={() => {}} onSend={() => {}} onQueue={onQueue} onQueueModeChange={onQueueModeChange} />);
  fireEvent.keyDown(screen.getByRole("textbox", { name: "Message" }), { key: "Tab" });
  expect(onQueue).not.toHaveBeenCalled();
  expect(screen.getByTestId("composer-queue-choice")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Steer now" }));
  expect(onQueueModeChange).toHaveBeenCalledWith("steer");
  expect(onQueue).toHaveBeenCalledWith("review the answer", "after_turn", []);
  expect(screen.queryByTestId("composer-queue-choice")).not.toBeInTheDocument();
});

test("a queued message with images shows a paperclip and their count, not the images", () => {
  render(
    <Composer
      value=""
      isEmpty={true}
      mode="agent"
      modes={["agent"]}
      generating={true}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
      onQueue={() => {}}
      queueMode="steer"
      queuedMessages={[{ id: "q1", text: "compare", mode: "after_turn", imageParts: [{ name: "a.png", mimeType: "image/png", sizeBytes: 3 }, { name: "b.png" }] }]}
    />,
  );
  const files = screen.getByTestId("composer-queue-files-q1");
  expect(files).toHaveTextContent("2");
  expect(files).toHaveAttribute("aria-label", "2 images attached");
  expect(files.querySelector("svg")).not.toBeNull();
  expect(screen.getByTestId("composer-queue-mode-q1")).toHaveTextContent("After turn");
});
