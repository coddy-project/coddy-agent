import { expect, test } from "vitest";
import {
  TodoPlanOrder,
  nextTodoChecklistTurn,
  sameTodoPlan,
  todoChecklistFocus,
  todoChecklistProgress,
  todoChecklistShown,
  todoPlanOpen,
} from "./todoChecklist";
import type { TodoPlanEntry } from "./todoToolPreview";

const entry = (status: string, content = status): TodoPlanEntry => ({
  content,
  status,
});

test("a checklist with work left shows, whether a turn runs or not", () => {
  const open = [entry("completed"), entry("in_progress"), entry("pending")];
  expect(todoChecklistShown(open, false, false)).toBe(true);
  expect(todoChecklistShown(open, true, true)).toBe(true);
  expect(todoChecklistShown([entry("pending")], false, false)).toBe(true);
});

test("a finished checklist shows only while the turn that closed it runs", () => {
  // The turn that ticked the last item is still writing its answer: the list
  // stays with every mark ticked. Once it is over, nothing is left to follow,
  // and a later turn that starts with the list already closed shows nothing.
  for (const done of [
    [entry("completed"), entry("completed")],
    [entry("completed"), entry("failed"), entry("cancelled")],
  ]) {
    expect(todoChecklistShown(done, true, true)).toBe(true);
    expect(todoChecklistShown(done, true, false)).toBe(false);
    expect(todoChecklistShown(done, false, true)).toBe(false);
  }
});

test("no checklist shows nothing", () => {
  expect(todoChecklistShown(undefined, true, true)).toBe(false);
  expect(todoChecklistShown([], true, true)).toBe(false);
  expect(todoChecklistShown([], false, false)).toBe(false);
});

test("the turn on screen remembers an open list from its start to its end", () => {
  const idle = {
    sessionId: "s1",
    generating: false,
    turnKey: 3,
    sawOpen: false,
  };
  // A turn starting on a finished list has not seen it open.
  const closedStart = nextTodoChecklistTurn(idle, "s1", true, 4, false);
  expect(closedStart).toEqual({
    sessionId: "s1",
    generating: true,
    turnKey: 4,
    sawOpen: false,
  });
  // The turn writes a new list: from then on it has seen work left...
  const opened = nextTodoChecklistTurn(closedStart, "s1", true, 4, true);
  expect(opened.sawOpen).toBe(true);
  // ...and keeps that once the list closes, until the turn ends.
  const closed = nextTodoChecklistTurn(opened, "s1", true, 4, false);
  expect(closed).toBe(opened);
  expect(nextTodoChecklistTurn(closed, "s1", false, 4, false)).toEqual({
    sessionId: "s1",
    generating: false,
    turnKey: 4,
    sawOpen: false,
  });
  // A turn starting on an open list has seen it at once.
  expect(nextTodoChecklistTurn(idle, "s1", true, 4, true).sawOpen).toBe(true);
  // Another session starts over from its own list.
  expect(nextTodoChecklistTurn(opened, "s2", true, 4, false)).toEqual({
    sessionId: "s2",
    generating: true,
    turnKey: 4,
    sawOpen: false,
  });
  // Nothing moved: the same state comes back.
  expect(nextTodoChecklistTurn(idle, "s1", false, 3, true)).toBe(idle);
});

test("a queued prompt the same admitted turn runs starts a new turn for the list", () => {
  // The answer closed the list; the server reads the next queued message
  // without ending the admitted turn, so generating never drops.
  const closing = {
    sessionId: "s1",
    generating: true,
    turnKey: 4,
    sawOpen: true,
  };
  const followUp = nextTodoChecklistTurn(closing, "s1", true, 5, false);
  expect(followUp.sawOpen).toBe(false);
  const done = [entry("completed"), entry("completed")];
  expect(todoChecklistShown(done, true, followUp.sawOpen)).toBe(false);
});

test("a list has work left while an item is pending or in progress", () => {
  expect(todoPlanOpen([entry("completed"), entry("pending")])).toBe(true);
  expect(todoPlanOpen([entry("in_progress")])).toBe(true);
  expect(
    todoPlanOpen([entry("completed"), entry("failed"), entry("cancelled")]),
  ).toBe(false);
  expect(todoPlanOpen(undefined)).toBe(false);
});

test("the progress counts the completed items out of every item", () => {
  expect(
    todoChecklistProgress([
      entry("completed"),
      entry("in_progress"),
      entry("failed"),
      entry("pending"),
    ]),
  ).toEqual({ done: 1, total: 4 });
  expect(todoChecklistProgress([])).toEqual({ done: 0, total: 0 });
});

test("two lists are the same only with the same items and statuses in order", () => {
  const a = [entry("completed", "Read"), entry("in_progress", "Fix")];
  expect(sameTodoPlan(a, a)).toBe(true);
  expect(
    sameTodoPlan(
      a,
      a.map((e) => ({ ...e })),
    ),
  ).toBe(true);
  expect(sameTodoPlan(a, [a[1]!, a[0]!])).toBe(false);
  expect(sameTodoPlan(a, [a[0]!, entry("completed", "Fix")])).toBe(false);
  expect(sameTodoPlan(a, a.slice(0, 1))).toBe(false);
  expect(sameTodoPlan(undefined, [])).toBe(false);
  expect(sameTodoPlan(undefined, undefined)).toBe(true);
});

test("a read issued before the last plan frame moves nothing back, a later one does", () => {
  const order = new TodoPlanOrder();
  // Nothing streamed yet: every read sets the list.
  expect(order.acceptsRead("s1", 100)).toBe(true);
  order.noteFrame("s1", 200);
  expect(order.acceptsRead("s1", 150)).toBe(false);
  expect(order.acceptsRead("s1", 200)).toBe(true);
  expect(order.acceptsRead("s1", 250)).toBe(true);
  // Another session's frame says nothing about this one.
  expect(order.acceptsRead("s2", 150)).toBe(true);
});

test("a folded list keeps the items in progress, else the next pending one", () => {
  expect([
    ...todoChecklistFocus([
      entry("completed"),
      entry("in_progress", "a"),
      entry("pending"),
      entry("in_progress", "b"),
    ]),
  ]).toEqual([1, 3]);
  expect([
    ...todoChecklistFocus([
      entry("completed"),
      entry("failed"),
      entry("pending", "next"),
      entry("pending", "later"),
    ]),
  ]).toEqual([2]);
  expect([
    ...todoChecklistFocus([entry("completed"), entry("cancelled")]),
  ]).toEqual([]);
});
