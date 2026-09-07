import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { PlanDocumentSection } from "./PlanDocumentSection";

afterEach(() => cleanup());

function renderPlan(
  overrides?: Partial<Parameters<typeof PlanDocumentSection>[0]>,
  opts?: { withActions?: boolean },
) {
  // A read-only transcript (subagent child) passes no action handlers at all.
  const actions =
    opts?.withActions === false
      ? {}
      : { onDiscard: () => {}, onRunPlan: () => {} };
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
      {...actions}
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

test("without action handlers (read-only child transcript) the card has no footer and a read-only editor", async () => {
  vi.useFakeTimers();
  const fetchMock = vi.fn().mockResolvedValue({ ok: true });
  vi.stubGlobal("fetch", fetchMock);
  try {
    renderPlan(undefined, { withActions: false });
    expect(document.querySelector(".plan-document-card--readonly")).toBeTruthy();
    expect(document.querySelector(".plan-document-foot")).toBeNull();
    expect(screen.queryByRole("button", { name: /run plan/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /discard/i })).toBeNull();
    // The preview still works, and the markdown pane cannot be edited or saved.
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Hello");
    fireEvent.click(screen.getByRole("button", { name: "Toggle preview" }));
    const editor = screen.getByRole("textbox", { name: /plan body/i });
    expect(editor).toHaveAttribute("readonly");
    fireEvent.change(editor, { target: { value: "# Hello\n\nEdited" } });
    await vi.advanceTimersByTimeAsync(650);
    expect(fetchMock).not.toHaveBeenCalled();
  } finally {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  }
});

test("collapsed card shows title and one-line description", () => {
  renderPlan({ expanded: false });
  expect(screen.getByText("Demo plan")).toBeInTheDocument();
  expect(screen.getByText("Short overview for the card")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Toggle preview" })).toBeNull();
});

test("markdown edit autosaves body with transcript content for bootstrap", async () => {
  vi.useFakeTimers();
  const fetchMock = vi.fn().mockResolvedValue({ ok: true });
  vi.stubGlobal("fetch", fetchMock);
  try {
    renderPlan();
    fireEvent.click(screen.getByRole("button", { name: "Toggle preview" }));
    const editor = screen.getByRole("textbox", { name: /plan body/i });
    fireEvent.change(editor, { target: { value: "# Hello\n\nEdited" } });
    await vi.advanceTimersByTimeAsync(650);
    expect(fetchMock).toHaveBeenCalled();
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/coddy/sessions/sess_test/plans/demo-plan");
    expect(init?.method).toBe("PUT");
    const payload = JSON.parse(String(init?.body));
    expect(payload.body).toBe("# Hello\n\nEdited");
    expect(payload.content).toContain("name: Demo");
  } finally {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  }
});
