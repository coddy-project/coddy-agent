import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { ChatHeader } from "./ChatHeader";
import type { BackgroundTask } from "../tasks/types";
import { OpenRailScreen } from "../nav/railEscape.fakes";

afterEach(() => cleanup());

test("edit mode shows full-width title input class", () => {
  render(<ChatHeader title="Hello" editable onTitleSave={() => {}} />);

  fireEvent.click(screen.getByRole("button", { name: /chat title/i }));

  const input = screen.getByRole("textbox");
  expect(input).toHaveClass("chat-title-input");
});

// History open beside the chat: Escape in the title leaves the title, and
// History stays for the next one.
test("Escape leaves the title and not the drawer open beside the chat", () => {
  const onTitleSave = vi.fn();
  const closeHistory = vi.fn();
  render(
    <>
      <OpenRailScreen id="history" onClose={closeHistory} />
      <ChatHeader title="Hello" editable onTitleSave={onTitleSave} />
    </>,
  );
  fireEvent.click(screen.getByRole("button", { name: /chat title/i }));
  fireEvent.keyDown(screen.getByRole("textbox"), { key: "Escape" });
  expect(screen.queryByRole("textbox")).toBeNull();
  expect(closeHistory).not.toHaveBeenCalled();
});

// The opener of the Tasks panel lives in the sticky header, so it does not scroll
// away with the transcript (docs/plans/turn-progress.md).

function task(over: Partial<BackgroundTask>): BackgroundTask {
  return {
    id: "bg_1",
    session_id: "sess",
    kind: "command",
    label: "make test",
    command: "make test",
    status: "running",
    started_at: "2026-09-18T10:00:00Z",
    timeout_seconds: 900,
    output_bytes: 0,
    output_truncated: false,
    elapsed_seconds: 5,
    overdue: false,
    running: true,
    ...over,
  };
}

test("the tasks control is in the header of a chat that never ran a task, without counts", () => {
  const onOpenTasks = vi.fn();
  render(<ChatHeader title="Hello" tasks={[]} onOpenTasks={onOpenTasks} />);
  const control = screen.getByTestId("chat-views-tasks");
  expect(control).not.toHaveClass("is-running");
  // The dot the control always had, and its word.
  expect(control.querySelector(".bgtask-dot--muted")).toBeTruthy();
  expect(control.querySelector(".chat-view-label")?.textContent).toBe("Tasks");
  expect(screen.queryByTestId("chat-views-tasks-count")).toBeNull();
  expect(control.getAttribute("aria-label")).toBe("Background tasks: none yet");
  fireEvent.click(control);
  expect(onOpenTasks).toHaveBeenCalledTimes(1);
});

test("with tasks the control says how many are running out of how many there are", () => {
  render(
    <ChatHeader
      title="Hello"
      tasks={[
        task({ id: "bg_1" }),
        task({ id: "bg_2", status: "succeeded", running: false }),
        task({ id: "bg_3", status: "failed", running: false }),
        // The memory run of a turn is a system task: neither running nor total.
        task({
          id: "bg_4",
          kind: "agent",
          agent: { name: "memory", system: true },
        }),
      ]}
      onOpenTasks={() => {}}
    />,
  );
  const control = screen.getByTestId("chat-views-tasks");
  expect(control).toHaveClass("is-running");
  expect(control.querySelector(".bgtask-dot--running")).toBeTruthy();
  expect(screen.getByTestId("chat-views-tasks-count").textContent).toBe(
    "1 / 3",
  );
  expect(control.getAttribute("aria-label")).toBe(
    "Background tasks: 1 running, 3 in total",
  );
  expect(
    control.parentElement?.querySelector(".chat-view-tip")?.textContent,
  ).toBe("Background tasks: 1 running, 3 in total");
});

test("once everything has finished the control keeps the total and drops the live mark", () => {
  render(
    <ChatHeader
      title="Hello"
      tasks={[
        task({ id: "bg_1", status: "succeeded", running: false }),
        task({ id: "bg_2", status: "failed", running: false }),
      ]}
      onOpenTasks={() => {}}
    />,
  );
  const control = screen.getByTestId("chat-views-tasks");
  expect(control).not.toHaveClass("is-running");
  expect(screen.getByTestId("chat-views-tasks-count").textContent).toBe(
    "0 / 2",
  );
  expect(control.getAttribute("aria-label")).toBe(
    "Background tasks: 0 running, 2 in total",
  );
});

test("without a way to open the panel the header has no views", () => {
  render(<ChatHeader title="Hello" tasks={[task({})]} />);
  expect(screen.queryByTestId("chat-views")).toBeNull();
});

// The views of a chat in its header - its files, its background tasks - are a
// row of buttons, the way the views of a session sit at the top of Claude's
// app: Files an icon with its short name (a phone keeps the icon alone),
// Background tasks last with the dot and the counts, the full name in a
// tooltip; no tab strip inside a panel, no menu to open first. The edits open
// from git's count in the bar over the composer, never from the header.
function viewsHeader(
  over: Partial<React.ComponentProps<typeof ChatHeader>> = {},
) {
  return (
    <ChatHeader
      title="Hello"
      tasks={[
        task({ id: "bg_1" }),
        task({ id: "bg_2", status: "succeeded", running: false }),
      ]}
      onOpenTasks={() => {}}
      onOpenFiles={() => {}}
      {...over}
    />
  );
}

test("the header shows files and background tasks as buttons in a row", () => {
  render(viewsHeader());
  const row = screen.getByRole("toolbar", { name: "Views of this chat" });
  const buttons = Array.from(row.querySelectorAll("button"));
  // Background tasks stand at the right edge; no Edits button.
  expect(buttons.map((b) => b.getAttribute("data-testid"))).toEqual([
    "chat-views-files",
    "chat-views-tasks",
  ]);
  // Files: an icon and a short name; Tasks: the dot and the counts.
  expect(buttons[0]!.querySelector("svg.chat-view-icon")).toBeTruthy();
  expect(buttons[1]!.querySelector("svg")).toBeNull();
  expect(buttons[1]!.querySelector(".bgtask-dot")).toBeTruthy();
  expect(
    buttons.map((b) => b.querySelector(".chat-view-label")?.textContent),
  ).toEqual(["Files", "Tasks"]);
  expect(screen.getByTestId("chat-views-tasks-count").textContent).toBe(
    "1 / 2",
  );
  const tips = Array.from(row.querySelectorAll('[role="tooltip"]')).map(
    (tip) => tip.textContent,
  );
  // The Files tooltip names its key.
  expect(tips[0]).toMatch(/^Workspace files \((Ctrl\+Shift\+F|⇧⌘F)\)$/);
  expect(tips[1]).toBe("Background tasks: 1 running, 2 in total");
  expect(buttons.map((b) => b.getAttribute("aria-label"))).toEqual(tips);
});

test("a button opens its view", () => {
  const onOpenTasks = vi.fn();
  const onOpenFiles = vi.fn();
  render(viewsHeader({ onOpenTasks, onOpenFiles }));
  fireEvent.click(screen.getByTestId("chat-views-files"));
  expect(onOpenFiles).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByTestId("chat-views-tasks"));
  expect(onOpenTasks).toHaveBeenCalledTimes(1);
});

test("the button of the view on show is pressed", () => {
  const { rerender } = render(viewsHeader());
  for (const id of ["tasks", "files"])
    expect(
      screen.getByTestId(`chat-views-${id}`).getAttribute("aria-pressed"),
    ).toBe("false");
  rerender(viewsHeader({ filesOpen: true }));
  expect(
    screen.getByTestId("chat-views-files").getAttribute("aria-pressed"),
  ).toBe("true");
  expect(screen.getByTestId("chat-views-files")).toHaveClass("is-active");
});

// Issue #435: while a new chat is being named, a shimmering bar stands where
// the title will be, never the first message ("/rpa-init") or "New chat".
test("a title being worked out shows a placeholder", () => {
  const { rerender } = render(
    <ChatHeader
      title="/rpa-init"
      titlePending
      editable
      onTitleSave={() => {}}
    />,
  );
  const btn = screen.getByRole("button", { name: /chat title/i });
  expect(btn).toHaveAttribute("aria-busy", "true");
  expect(screen.getByTestId("chat-title-pending")).toBeInTheDocument();
  expect(btn).toHaveTextContent("Naming the chat");
  expect(btn).not.toHaveTextContent("/rpa-init");

  rerender(
    <ChatHeader
      title="Repository onboarding"
      editable
      onTitleSave={() => {}}
    />,
  );
  expect(screen.queryByTestId("chat-title-pending")).toBeNull();
  expect(btn).not.toHaveAttribute("aria-busy");
  expect(btn).toHaveTextContent("Repository onboarding");
});
