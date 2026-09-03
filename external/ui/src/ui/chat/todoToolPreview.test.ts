import { expect, test } from "vitest";

import { buildTodoToolPreview } from "./todoToolPreview";

const plan = [
  { content: "Inspect the existing tool cards", status: "completed" },
  { content: "Render the structured preview", status: "in_progress" },
  { content: "Add interaction tests", status: "pending" },
];

test("item update reads its final row from the persisted plan snapshot", () => {
  expect(
    buildTodoToolPreview({
      toolName: "coddy_todo_item_update",
      argsText: JSON.stringify({ index: 1, status: "in_progress" }),
      planSnapshot: plan,
    }),
  ).toEqual({
    variant: "item",
    header: "Updated item",
    meta: ["2 of 3"],
    entries: [plan[1]],
  });
});

test("plan replacement preserves item order and reports completed count", () => {
  expect(
    buildTodoToolPreview({
      toolName: "coddy_todo_plan_replace",
      argsText: JSON.stringify({ markdown: "- [ ] ignored here" }),
      planSnapshot: plan,
    }),
  ).toEqual({
    variant: "plan",
    header: "Todo plan",
    meta: ["1 completed", "3 items"],
    entries: plan,
  });
});

test("missing or invalid snapshot leaves the generic tool preview in place", () => {
  expect(
    buildTodoToolPreview({
      toolName: "coddy_todo_item_update",
      argsText: JSON.stringify({ index: 4, status: "completed" }),
      planSnapshot: plan,
    }),
  ).toBeNull();
  expect(
    buildTodoToolPreview({
      toolName: "coddy_todo_plan_replace",
      argsText: "{}",
      planSnapshot: [],
    }),
  ).toBeNull();
});
