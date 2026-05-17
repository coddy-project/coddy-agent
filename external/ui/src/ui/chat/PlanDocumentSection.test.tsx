import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";

import { PlanDocumentSection } from "./PlanDocumentSection";

afterEach(() => cleanup());

function renderPlan(
  overrides?: Partial<Parameters<typeof PlanDocumentSection>[0]>,
) {
  return render(
    <PlanDocumentSection
      sessionId="sess_test"
      slug="demo-plan"
      name="Demo plan"
      overview="Short overview for the card"
      content="---\nname: Demo\n---\n# Hello\n\nSteps"
      body={`# Hello

Steps`}
      path="/tmp/sess_test/plans/demo-plan.plan.md"
      expanded
      onExpandedChange={() => {}}
      onDiscard={() => {}}
      onRunPlan={() => {}}
      {...overrides}
    />,
  );
}

test("preview is default and title shows absolute path in tooltip", () => {
  renderPlan();
  expect(screen.queryByRole("textbox")).toBeNull();
  expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Hello");
  const title = screen.getByText("Demo plan");
  expect(title).toHaveAttribute(
    "title",
    "/tmp/sess_test/plans/demo-plan.plan.md",
  );
  expect(
    screen.getByRole("button", { name: "Toggle preview" }),
  ).toHaveAttribute("aria-pressed", "true");
});

test("eye toggle switches preview and markdown in one pane", () => {
  renderPlan();
  const pane = document.querySelector(".plan-document-pane");
  expect(pane).toBeTruthy();
  const toggle = screen.getByRole("button", { name: "Toggle preview" });
  fireEvent.click(toggle);
  expect(toggle).toHaveAttribute("aria-pressed", "false");
  expect(screen.getByRole("textbox", { name: /plan body/i })).toBeInTheDocument();
  expect(screen.getByTestId("plan_editor_gutter")).toBeInTheDocument();
  fireEvent.click(toggle);
  expect(toggle).toHaveAttribute("aria-pressed", "true");
  expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Hello");
});

test("discarded plan disables run and keeps card visible", () => {
  renderPlan({ discarded: true });
  expect(screen.getByRole("button", { name: /run plan/i })).toBeDisabled();
  expect(screen.getByRole("button", { name: /discard/i })).toBeDisabled();
});

test("collapsed card shows title and one-line description", () => {
  renderPlan({ expanded: false });
  expect(screen.getByText("Demo plan")).toBeInTheDocument();
  expect(screen.getByText("Short overview for the card")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Toggle preview" })).toBeNull();
});
