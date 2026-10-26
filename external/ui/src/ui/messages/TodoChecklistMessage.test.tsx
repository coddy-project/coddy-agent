import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, test } from "vitest";
import { initLocale } from "../i18n/i18n";
import { TodoChecklistMessage } from "./TodoChecklistMessage";

const ENTRIES = [
  { content: "Read the failing test", status: "completed" },
  { content: "Fix the parser", status: "in_progress" },
  { content: "Run the whole suite", status: "pending" },
];

beforeEach(() => initLocale("en"));
afterEach(cleanup);

const head = () => screen.getByRole("button", { name: /Todo list/ });
const folded = () =>
  Array.from(
    screen
      .getByTestId("todo-checklist")
      .querySelectorAll(
        ".todo-checklist-row.is-folded .todo-checklist-content",
      ),
  ).map((row) => row.textContent);
const unfolded = () =>
  Array.from(
    screen
      .getByTestId("todo-checklist")
      .querySelectorAll(
        ".todo-checklist-row:not(.is-folded) .todo-checklist-content",
      ),
  ).map((row) => row.textContent);

test("the item in progress is named on its row as the current step", () => {
  render(
    <TodoChecklistMessage sessionId="s1" entries={ENTRIES} shown={true} />,
  );
  const list = screen.getByRole("list", { name: "Todo list, 1 of 3 done" });
  const rows = within(list).getAllByRole("listitem");
  expect(rows.map((row) => row.getAttribute("aria-label"))).toEqual([
    "Completed: Read the failing test",
    "In progress: Fix the parser",
    "Pending: Run the whole suite",
  ]);
  // The one the agent is on is the current step of the list; its mark spins
  // whether a turn runs or not (todoChecklistCss.test.ts).
  expect(rows.map((row) => row.getAttribute("aria-current"))).toEqual([
    null,
    "step",
    null,
  ]);
  expect(rows[1]).toHaveClass("todo-checklist-row--in_progress");
});

test("every item carries the status mark the todo rows use", () => {
  render(
    <TodoChecklistMessage
      sessionId="s1"
      entries={[
        ...ENTRIES,
        { content: "Try the old build", status: "failed" },
        { content: "Bisect", status: "cancelled" },
      ]}
      shown={true}
    />,
  );
  const marks = Array.from(
    screen
      .getByTestId("todo-checklist")
      .querySelectorAll(".todo-tool-preview-mark"),
  );
  expect(marks.map((mark) => mark.className)).toEqual([
    "todo-tool-preview-mark todo-tool-preview-mark--completed",
    "todo-tool-preview-mark todo-tool-preview-mark--in_progress",
    "todo-tool-preview-mark todo-tool-preview-mark--pending",
    "todo-tool-preview-mark todo-tool-preview-mark--failed",
    "todo-tool-preview-mark todo-tool-preview-mark--cancelled",
  ]);
  expect(marks.map((mark) => mark.textContent)).toEqual([
    "✓",
    "",
    "",
    "×",
    "−",
  ]);
  for (const mark of marks) expect(mark).toHaveAttribute("aria-hidden", "true");
});

test("the list speaks Russian in the Russian interface", () => {
  initLocale("ru");
  render(
    <TodoChecklistMessage sessionId="s1" entries={ENTRIES} shown={true} />,
  );
  const list = screen.getByRole("list", {
    name: "Список задач, выполнено 1 из 3",
  });
  expect(within(list).getAllByRole("listitem")[1]).toHaveAttribute(
    "aria-label",
    "Выполняется: Fix the parser",
  );
  expect(
    screen.getByRole("button", { name: "Список задач 1/3" }),
  ).toBeInTheDocument();
});

test("a click on the head folds the list to the item in progress, and the choice is remembered", () => {
  const { unmount } = render(
    <TodoChecklistMessage sessionId="s1" entries={ENTRIES} shown={true} />,
  );
  // Open by default: the head names the list and how far it got.
  expect(head()).toHaveAccessibleName("Todo list 1/3");
  expect(head()).toHaveAttribute("aria-expanded", "true");
  expect(folded()).toEqual([]);

  fireEvent.click(head());
  expect(head()).toHaveAttribute("aria-expanded", "false");
  // Folded, the item the agent is on stays in view, still spinning.
  expect(unfolded()).toEqual(["Fix the parser"]);
  expect(folded()).toEqual(["Read the failing test", "Run the whole suite"]);
  for (const row of screen
    .getByTestId("todo-checklist")
    .querySelectorAll(".todo-checklist-row.is-folded"))
    expect(row).toHaveAttribute("aria-hidden", "true");
  expect(
    screen
      .getByTestId("todo-checklist")
      .querySelector(".todo-checklist-row--in_progress"),
  ).toHaveTextContent("Fix the parser");

  // Another chat, or the same one after a reload, opens folded.
  unmount();
  render(
    <TodoChecklistMessage sessionId="s1" entries={ENTRIES} shown={true} />,
  );
  expect(head()).toHaveAttribute("aria-expanded", "false");
  expect(unfolded()).toEqual(["Fix the parser"]);

  fireEvent.click(head());
  expect(head()).toHaveAttribute("aria-expanded", "true");
  expect(folded()).toEqual([]);
});

test("folded with nothing in progress, only the head stays", () => {
  render(
    <TodoChecklistMessage
      sessionId="s1"
      entries={[
        { content: "Read the failing test", status: "completed" },
        { content: "Fix the parser", status: "pending" },
        { content: "Run the whole suite", status: "pending" },
      ]}
      shown={true}
    />,
  );
  fireEvent.click(head());
  expect(head()).toHaveAccessibleName("Todo list 1/3");
  expect(unfolded()).toEqual([]);
});

test("a status changes the mark in place and pops its glyph in", () => {
  const { rerender } = render(
    <TodoChecklistMessage sessionId="s1" entries={ENTRIES} shown={true} />,
  );
  const markOf = (text: string) =>
    within(screen.getByTestId("todo-checklist"))
      .getByText(text)
      .closest("li")!
      .querySelector(".todo-tool-preview-mark")!;
  const before = markOf("Fix the parser");
  expect(before.querySelector(".todo-checklist-glyph")).toBeNull();

  rerender(
    <TodoChecklistMessage
      sessionId="s1"
      entries={[
        ENTRIES[0]!,
        { content: "Fix the parser", status: "completed" },
        { content: "Run the whole suite", status: "in_progress" },
      ]}
      shown={true}
    />,
  );
  // The same mark takes its new look, so its colours and shape can move.
  const after = markOf("Fix the parser");
  expect(after).toBe(before);
  expect(after).toHaveClass("todo-tool-preview-mark--completed");
  expect(after.querySelector(".todo-checklist-glyph")).toHaveTextContent("✓");
});

test("a list that stops showing fades out before it leaves, and stays when shown again", async () => {
  const { rerender } = render(
    <TodoChecklistMessage sessionId="s1" entries={ENTRIES} shown={true} />,
  );
  const row = screen.getByTestId("todo-checklist");
  expect(row).not.toHaveClass("is-leaving");

  rerender(
    <TodoChecklistMessage sessionId="s1" entries={undefined} shown={false} />,
  );
  // Still there for its exit, with the list it had, out of the reader's way.
  expect(screen.getByTestId("todo-checklist")).toBe(row);
  expect(row).toHaveClass("is-leaving");
  expect(row).toHaveAttribute("aria-hidden", "true");
  expect(row).toHaveTextContent("Fix the parser");

  // Shown again before the exit ends: it simply stays.
  rerender(
    <TodoChecklistMessage sessionId="s1" entries={ENTRIES} shown={true} />,
  );
  expect(screen.getByTestId("todo-checklist")).not.toHaveClass("is-leaving");

  rerender(
    <TodoChecklistMessage sessionId="s1" entries={ENTRIES} shown={false} />,
  );
  await waitFor(() =>
    expect(screen.queryByTestId("todo-checklist")).toBeNull(),
  );
});

test("a list never shown has no exit to play", () => {
  render(
    <TodoChecklistMessage sessionId="s1" entries={ENTRIES} shown={false} />,
  );
  expect(screen.queryByTestId("todo-checklist")).toBeNull();
});

test("another chat never plays the exit of the list it left", () => {
  const { rerender } = render(
    <TodoChecklistMessage sessionId="s1" entries={ENTRIES} shown={true} />,
  );
  expect(screen.getByTestId("todo-checklist")).toBeInTheDocument();
  // The transcript stays mounted when the reader switches chats.
  rerender(
    <TodoChecklistMessage sessionId="s2" entries={undefined} shown={false} />,
  );
  expect(screen.queryByTestId("todo-checklist")).toBeNull();
});

test("an item added above keeps the rows below it, marks and all", () => {
  const { rerender } = render(
    <TodoChecklistMessage sessionId="s1" entries={ENTRIES} shown={true} />,
  );
  const rowOf = (text: string) =>
    within(screen.getByTestId("todo-checklist")).getByText(text).closest("li");
  const before = rowOf("Fix the parser");
  rerender(
    <TodoChecklistMessage
      sessionId="s1"
      entries={[
        { content: "Reproduce the bug", status: "completed" },
        ...ENTRIES,
      ]}
      shown={true}
    />,
  );
  // The row moved down a place and is still the same row, so its mark can
  // change in place and it does not come in again.
  expect(rowOf("Fix the parser")).toBe(before);
});
