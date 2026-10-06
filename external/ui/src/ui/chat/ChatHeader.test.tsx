import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { ChatHeader } from "./ChatHeader";
import type { BackgroundTask } from "../tasks/types";
import { OpenRailScreen } from "../nav/railEscape.fakes";
import { useRightDockEscape } from "../components/useRightDock";

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
  const control = screen.getByTestId("chat-header-tasks");
  expect(control).not.toHaveClass("is-running");
  expect(control.querySelector(".chat-header-tasks-label")?.textContent).toBe(
    "Tasks",
  );
  expect(screen.queryByTestId("chat-header-tasks-counts")).toBeNull();
  expect(control.getAttribute("aria-label")).toBe("Background tasks: none yet");
  fireEvent.click(control);
  expect(onOpenTasks).not.toHaveBeenCalled();
  fireEvent.click(screen.getByTestId("chat-views-tasks"));
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
  const control = screen.getByTestId("chat-header-tasks");
  expect(control).toHaveClass("is-running");
  expect(screen.getByTestId("chat-header-tasks-counts").textContent).toBe(
    "1 / 3",
  );
  expect(control.getAttribute("aria-label")).toBe(
    "Background tasks: 1 running, 3 in total",
  );
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
  const control = screen.getByTestId("chat-header-tasks");
  expect(control).not.toHaveClass("is-running");
  expect(screen.getByTestId("chat-header-tasks-counts").textContent).toBe(
    "0 / 2",
  );
});

test("the control opens a menu and says whether a view it offers is on show", () => {
  const { rerender } = render(
    <ChatHeader title="Hello" tasks={[task({})]} onOpenTasks={() => {}} />,
  );
  const control = screen.getByTestId("chat-header-tasks");
  expect(control.getAttribute("aria-haspopup")).toBe("menu");
  expect(control.getAttribute("aria-expanded")).toBe("false");
  expect(control).not.toHaveClass("is-active");
  fireEvent.click(control);
  expect(control.getAttribute("aria-expanded")).toBe("true");
  rerender(
    <ChatHeader
      title="Hello"
      tasks={[task({})]}
      onOpenTasks={() => {}}
      tasksOpen={true}
    />,
  );
  expect(screen.getByTestId("chat-header-tasks")).toHaveClass("is-active");
});

test("without a way to open the panel the header has no tasks control", () => {
  render(<ChatHeader title="Hello" tasks={[task({})]} />);
  expect(screen.queryByTestId("chat-header-tasks")).toBeNull();
});

// The views beside a chat - its background tasks, its edits, its files - are
// picked from one menu under the header control, the way the views of a
// session are picked in Claude's app, not from a tab strip inside the panel.
function viewsHeader(
  over: Partial<React.ComponentProps<typeof ChatHeader>> = {},
  withEdits = true,
) {
  return (
    <ChatHeader
      title="Hello"
      tasks={[
        task({ id: "bg_1" }),
        task({ id: "bg_2", status: "succeeded", running: false }),
      ]}
      onOpenTasks={() => {}}
      {...(withEdits ? { onOpenEdits: () => {} } : {})}
      onOpenFiles={() => {}}
      {...over}
    />
  );
}

test("the views menu offers background tasks, edits and files", () => {
  render(viewsHeader());
  fireEvent.click(screen.getByTestId("chat-header-tasks"));
  const menu = screen.getByRole("menu");
  expect(menu).toBe(screen.getByTestId("chat-views-menu"));
  const items = screen.getAllByRole("menuitemcheckbox");
  expect(items.map((item) => item.getAttribute("data-testid"))).toEqual([
    "chat-views-tasks",
    "chat-views-edits",
    "chat-views-files",
  ]);
  expect(items[0]!.textContent).toContain("Background tasks");
  expect(items[0]!.textContent).toContain("1 / 2");
  expect(items[1]!.textContent).toContain("Edits");
  expect(items[2]!.textContent).toContain("Files");
  // The window has a key of its own, and the menu teaches it.
  expect(
    screen.getByTestId("chat-views-files-shortcut").textContent,
  ).toMatch(/^(Ctrl\+Shift\+F|⇧⌘F)$/);
  // The menu takes the focus, so the keyboard can walk it.
  expect(document.activeElement).toBe(items[0]);
});

test("picking a view opens it and puts the menu away", () => {
  const onOpenEdits = vi.fn();
  const onOpenFiles = vi.fn();
  render(viewsHeader({ onOpenEdits, onOpenFiles }));
  const control = screen.getByTestId("chat-header-tasks");
  fireEvent.click(control);
  fireEvent.click(screen.getByTestId("chat-views-edits"));
  expect(onOpenEdits).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole("menu")).toBeNull();
  expect(document.activeElement).toBe(control);
  fireEvent.click(control);
  fireEvent.click(screen.getByTestId("chat-views-files"));
  expect(onOpenFiles).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole("menu")).toBeNull();
});

test("the view on show is checked in the menu", () => {
  render(viewsHeader({ editsOpen: true }));
  fireEvent.click(screen.getByTestId("chat-header-tasks"));
  expect(
    screen.getByTestId("chat-views-tasks").getAttribute("aria-checked"),
  ).toBe("false");
  expect(
    screen.getByTestId("chat-views-edits").getAttribute("aria-checked"),
  ).toBe("true");
});

test("the arrow keys walk the menu and wrap around", () => {
  render(viewsHeader());
  fireEvent.click(screen.getByTestId("chat-header-tasks"));
  const [tasks, edits, files] = screen.getAllByRole("menuitemcheckbox");
  fireEvent.keyDown(tasks!, { key: "ArrowDown" });
  expect(document.activeElement).toBe(edits);
  fireEvent.keyDown(edits!, { key: "ArrowDown" });
  expect(document.activeElement).toBe(files);
  fireEvent.keyDown(files!, { key: "ArrowDown" });
  expect(document.activeElement).toBe(tasks);
  fireEvent.keyDown(tasks!, { key: "ArrowUp" });
  expect(document.activeElement).toBe(files);
  fireEvent.keyDown(files!, { key: "Home" });
  expect(document.activeElement).toBe(tasks);
  fireEvent.keyDown(tasks!, { key: "End" });
  expect(document.activeElement).toBe(files);
});

// Escape undoes one step: the menu goes, the view beside the chat stays.
test("Escape puts the menu away and leaves the view beside the chat open", () => {
  const closeView = vi.fn();
  function Stand() {
    useRightDockEscape(true, closeView);
    return viewsHeader({ tasksOpen: true });
  }
  render(<Stand />);
  const control = screen.getByTestId("chat-header-tasks");
  fireEvent.click(control);
  fireEvent.keyDown(document.activeElement!, { key: "Escape" });
  expect(screen.queryByRole("menu")).toBeNull();
  expect(closeView).not.toHaveBeenCalled();
  expect(document.activeElement).toBe(control);
});

test("a chat whose edits are switched off is offered its tasks and its files", () => {
  render(viewsHeader({}, false));
  fireEvent.click(screen.getByTestId("chat-header-tasks"));
  expect(screen.queryByTestId("chat-views-edits")).toBeNull();
  expect(screen.getByTestId("chat-views-tasks")).toBeTruthy();
  expect(screen.getByTestId("chat-views-files")).toBeTruthy();
});

// A view opened some other way - its key, a link - puts the menu away, so the
// next Escape is the view's.
test("a view opened from elsewhere puts the menu away", () => {
  const { rerender } = render(viewsHeader());
  fireEvent.click(screen.getByTestId("chat-header-tasks"));
  expect(screen.getByRole("menu")).toBeTruthy();
  rerender(viewsHeader({ filesOpen: true }));
  expect(screen.queryByRole("menu")).toBeNull();
});
