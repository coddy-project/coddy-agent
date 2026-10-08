import React, { useState } from "react";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { Composer } from "./Composer";
import { WorkspaceBar } from "./WorkspaceBar";
import { ConfirmProvider } from "../components/useConfirm";
import type { GoalActions } from "./GoalPopover";
import { parseSessionGoal, type SessionGoal } from "./goal";

afterEach(() => cleanup());

function goalOf(over: Record<string, unknown> = {}): SessionGoal {
  return parseSessionGoal({
    id: "goal_1",
    objective: "Make every test of the parser pass",
    status: "active",
    continuations: 2,
    maxContinuations: 10,
    checks: 3,
    activeMs: 65_000,
    tokensUsed: 12_345,
    tokenBudget: 200_000,
    lastCheck: {
      verdict: "met",
      reason: "all tests pass",
      remaining: ["update the changelog"],
      verified: true,
    },
    checklist: [
      { text: "parser tests", status: "met", evidence: "go test ./parser ok" },
      { text: "lexer tests", status: "not_met" },
      { text: "docs", status: "unverified" },
    ],
    ...over,
  })!;
}

function actionsMock() {
  return {
    pause: vi.fn(async () => true),
    clear: vi.fn(async () => true),
    sendPrompt: vi.fn(),
  } satisfies GoalActions;
}

function Harness(props: {
  goal: SessionGoal | null;
  actions?: GoalActions;
  initial?: string;
  generating?: boolean;
  onSend?: (text: string) => void;
}) {
  const [value, setValue] = useState(props.initial ?? "");
  return (
    <ConfirmProvider>
      <Composer
        value={value}
        isEmpty={false}
        sessionId="sess_1"
        mode="agent"
        modes={["agent", "plan"]}
        onModeChange={() => {}}
        onChange={setValue}
        onSend={props.onSend ?? (() => {})}
        goal={props.goal}
        {...(props.actions ? { goalActions: props.actions } : {})}
        {...(props.generating ? { generating: true, onStop: () => {} } : {})}
        // The plate a running chat hands in, with git's count on it.
        cardTop={(goalMark) => (
          <WorkspaceBar
            context={{
              path: "/work/parser",
              name: "parser",
              is_git_repo: true,
              is_worktree: false,
              branch: "main",
            }}
            workingCopy={{
              loaded: true,
              error: "",
              changes: {
                sessionId: "sess_1",
                vcs: "git",
                files: [],
                totals: { files: 2, additions: 4, deletions: 2 },
              },
            }}
            onOpenEdits={() => {}}
            goal={goalMark}
          />
        )}
      />
    </ConfirmProvider>
  );
}

const mark = () => screen.getByTestId("composer-goal");
const popover = () => screen.getByTestId("goal-popover");

test.each([
  ["active", "goal-tone-active", "active"],
  ["paused", "goal-tone-muted", "paused"],
  ["blocked", "goal-tone-alert", "needs you"],
  ["complete", "goal-tone-done", "complete"],
  ["limited", "goal-tone-muted", "out of budget"],
])(
  "the mark of a %s goal is the target in its tone, its status in the name and the tip",
  (status, tone, word) => {
    render(<Harness goal={goalOf({ status })} actions={actionsMock()} />);
    expect(mark()).toHaveClass("composer-goal", tone);
    expect(mark().dataset.goalStatus).toBe(status);
    // An icon: no words on the bar.
    expect(mark().textContent).toBe("");
    expect(mark()).toHaveAttribute("aria-label", `Goal: ${word}`);
    const tip = screen.getByTestId("composer-goal-tip");
    expect(tip).toHaveTextContent(`Goal: ${word}`);
    expect(tip).toHaveTextContent("Make every test of the parser pass");
  },
);

test("the goal mark stands on the plate over the card, left of git's count", () => {
  render(<Harness goal={goalOf()} actions={actionsMock()} />);
  const host = mark().parentElement!;
  expect(host).toHaveClass("composer-goal-tip-host");
  expect(host.parentElement).toBe(screen.getByTestId("workspace-bar"));
  expect(host.nextElementSibling).toBe(
    screen.getByTestId("workspace-bar-edits"),
  );
  // Nowhere on the bar under the field.
  expect(mark().closest(".composer-bar-actions")).toBeNull();
  expect(mark().closest(".composer-tabs")).toBeNull();
});

test("the goal's actions are square icons, named and with a tip", () => {
  render(<Harness goal={goalOf()} actions={actionsMock()} />);
  fireEvent.click(mark());
  for (const [id, name] of [
    ["goal-pause", "Pause"],
    ["goal-edit", "Edit"],
    ["goal-clear", "Clear"],
  ] as const) {
    const button = within(popover()).getByTestId(id);
    expect(button).toHaveClass("goal-icon-btn");
    expect(button.textContent).toBe("");
    expect(button.querySelector("svg")).not.toBeNull();
    expect(button).toHaveAttribute("aria-label", name);
    expect(button).toHaveAttribute("title", name);
  }
  expect(within(popover()).getByTestId("goal-clear")).toHaveClass(
    "goal-icon-btn--danger",
  );
  cleanup();
  render(
    <Harness goal={goalOf({ status: "paused" })} actions={actionsMock()} />,
  );
  fireEvent.click(mark());
  const resume = within(popover()).getByTestId("goal-resume");
  expect(resume).toHaveClass("goal-icon-btn", "goal-icon-btn--primary");
  expect(resume.textContent).toBe("");
  expect(resume).toHaveAttribute("aria-label", "Resume");
});

test("no goal, no mark; no actions, no mark either", () => {
  const { unmount } = render(<Harness goal={null} actions={actionsMock()} />);
  expect(screen.queryByTestId("composer-goal")).toBeNull();
  unmount();
  render(<Harness goal={goalOf()} />);
  expect(screen.queryByTestId("composer-goal")).toBeNull();
});

test("the mark opens the popover with everything the supervisor knows", () => {
  render(
    <Harness
      goal={goalOf({ status: "blocked", statusReason: "Which branch?" })}
      actions={actionsMock()}
    />,
  );
  fireEvent.click(mark());
  expect(mark()).toHaveAttribute("aria-expanded", "true");
  const p = popover();
  expect(within(p).getByTestId("goal-objective")).toHaveTextContent(
    "Make every test of the parser pass",
  );
  expect(within(p).getByTestId("goal-popover-status")).toHaveTextContent(
    "needs you",
  );
  expect(within(p).getByTestId("goal-status-reason")).toHaveTextContent(
    "Which branch?",
  );
  const check = within(p).getByTestId("goal-last-check");
  expect(check).toHaveTextContent("Met");
  expect(within(check).getByTestId("goal-verified")).toHaveTextContent(
    "verified",
  );
  expect(check).toHaveTextContent("all tests pass");
  expect(check).toHaveTextContent("update the changelog");
  const list = within(p).getByTestId("goal-checklist");
  expect(list.querySelectorAll("li")).toHaveLength(3);
  // Evidence folds out on a tap: a disclosure, not a hover.
  expect(within(list).getByText("go test ./parser ok")).toBeInTheDocument();
  expect(list.querySelectorAll("details")).toHaveLength(1);
  const numbers = within(p).getByTestId("goal-numbers");
  expect(numbers).toHaveTextContent("2 of 10");
  expect(numbers).toHaveTextContent("1m 05s");
  expect(numbers).toHaveTextContent("12.3k of 200k");
  // Blocked: resume, never pause.
  expect(within(p).queryByTestId("goal-pause")).toBeNull();
  expect(within(p).getByTestId("goal-resume")).toBeEnabled();
});

test("Pause asks the server to pause; it is offered only while the goal is active", async () => {
  const actions = actionsMock();
  render(<Harness goal={goalOf()} actions={actions} />);
  fireEvent.click(mark());
  expect(within(popover()).queryByTestId("goal-resume")).toBeNull();
  await act(async () => {
    fireEvent.click(within(popover()).getByTestId("goal-pause"));
  });
  expect(actions.pause).toHaveBeenCalledTimes(1);
  expect(actions.sendPrompt).not.toHaveBeenCalled();
});

test("a refused pause says so in the popover", async () => {
  const actions = actionsMock();
  actions.pause.mockResolvedValueOnce(false);
  render(<Harness goal={goalOf()} actions={actions} />);
  fireEvent.click(mark());
  await act(async () => {
    fireEvent.click(within(popover()).getByTestId("goal-pause"));
  });
  expect(within(popover()).getByTestId("goal-error")).toHaveTextContent(
    "Could not pause the goal",
  );
});

test("Resume sends /goal resume through the composer's send path and closes", () => {
  const actions = actionsMock();
  render(<Harness goal={goalOf({ status: "paused" })} actions={actions} />);
  fireEvent.click(mark());
  fireEvent.click(within(popover()).getByTestId("goal-resume"));
  expect(actions.sendPrompt).toHaveBeenCalledWith("/goal resume");
  expect(screen.queryByTestId("goal-popover")).toBeNull();
});

test("while a turn runs, Resume waits and the popover says why", () => {
  const actions = actionsMock();
  render(
    <Harness
      goal={goalOf({ status: "limited" })}
      actions={actions}
      generating={true}
    />,
  );
  fireEvent.click(mark());
  expect(within(popover()).getByTestId("goal-resume")).toBeDisabled();
  expect(within(popover()).getByTestId("goal-busy-note")).toBeInTheDocument();
});

test("Edit replaces the goal by sending /goal with the new objective", () => {
  const actions = actionsMock();
  render(<Harness goal={goalOf()} actions={actions} />);
  fireEvent.click(mark());
  fireEvent.click(within(popover()).getByTestId("goal-edit"));
  const field = within(popover()).getByTestId(
    "goal-objective-input",
  ) as HTMLTextAreaElement;
  expect(field.value).toBe("Make every test of the parser pass");
  fireEvent.change(field, {
    target: { value: "Make every test pass, the lexer's too" },
  });
  fireEvent.click(within(popover()).getByTestId("goal-submit"));
  expect(actions.sendPrompt).toHaveBeenCalledWith(
    "/goal Make every test pass, the lexer's too",
  );
  expect(screen.queryByTestId("goal-popover")).toBeNull();
});

test("Escape leaves the edit first, then the popover", () => {
  render(<Harness goal={goalOf()} actions={actionsMock()} />);
  fireEvent.click(mark());
  fireEvent.click(within(popover()).getByTestId("goal-edit"));
  fireEvent.keyDown(document, { key: "Escape" });
  expect(within(popover()).queryByTestId("goal-form")).toBeNull();
  expect(within(popover()).getByTestId("goal-objective")).toBeInTheDocument();
  fireEvent.keyDown(document, { key: "Escape" });
  expect(screen.queryByTestId("goal-popover")).toBeNull();
});

test("an objective over 4000 characters cannot be sent", () => {
  const actions = actionsMock();
  render(<Harness goal={goalOf()} actions={actions} />);
  fireEvent.click(mark());
  fireEvent.click(within(popover()).getByTestId("goal-edit"));
  fireEvent.change(within(popover()).getByTestId("goal-objective-input"), {
    target: { value: "x".repeat(4001) },
  });
  expect(within(popover()).getByTestId("goal-submit")).toBeDisabled();
  expect(
    within(popover()).getByTestId("goal-objective-count"),
  ).toHaveTextContent("4001 / 4000");
  expect(within(popover()).getByRole("alert")).toHaveTextContent(
    "longer than 4000 characters",
  );
});

test("Clear asks first and clears only on yes", async () => {
  const actions = actionsMock();
  render(<Harness goal={goalOf()} actions={actions} />);
  fireEvent.click(mark());
  fireEvent.click(within(popover()).getByTestId("goal-clear"));
  const dialog = await screen.findByTestId("app-confirm-dialog");
  expect(dialog).toHaveTextContent("Clear the goal?");
  // The popover stays under the dialog while it asks.
  expect(screen.getByTestId("goal-popover")).toBeInTheDocument();
  await act(async () => {
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
  });
  expect(actions.clear).not.toHaveBeenCalled();

  fireEvent.click(within(popover()).getByTestId("goal-clear"));
  const again = await screen.findByTestId("app-confirm-dialog");
  await act(async () => {
    fireEvent.click(within(again).getByRole("button", { name: "Clear" }));
  });
  await waitFor(() => expect(actions.clear).toHaveBeenCalledTimes(1));
  await waitFor(() => expect(screen.queryByTestId("goal-popover")).toBeNull());
});

test("a bare /goal opens the popover and sends nothing", () => {
  const onSend = vi.fn();
  const actions = actionsMock();
  render(
    <Harness
      goal={goalOf()}
      actions={actions}
      initial="/goal"
      onSend={onSend}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  expect(onSend).not.toHaveBeenCalled();
  expect(popover()).toBeInTheDocument();
  expect(
    (screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement)
      .value,
  ).toBe("");
});

test("a bare /goal without a goal opens the form that sets one", () => {
  const onSend = vi.fn();
  const actions = actionsMock();
  render(
    <Harness goal={null} actions={actions} initial="/goal " onSend={onSend} />,
  );
  fireEvent.keyDown(screen.getByRole("textbox", { name: "Message" }), {
    key: "Enter",
  });
  expect(onSend).not.toHaveBeenCalled();
  const p = popover();
  expect(p).toHaveTextContent("No goal is set");
  expect(within(p).getByTestId("goal-submit")).toBeDisabled();
  fireEvent.change(within(p).getByTestId("goal-objective-input"), {
    target: { value: "  Ship the release  " },
  });
  fireEvent.click(within(p).getByTestId("goal-submit"));
  expect(actions.sendPrompt).toHaveBeenCalledWith("/goal Ship the release");
  expect(onSend).not.toHaveBeenCalled();
});

test("/goal with anything after it is the server's and is sent as typed", () => {
  const onSend = vi.fn();
  render(
    <Harness
      goal={goalOf()}
      actions={actionsMock()}
      initial="/goal pause"
      onSend={onSend}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  expect(onSend).toHaveBeenCalledWith("/goal pause");
  expect(screen.queryByTestId("goal-popover")).toBeNull();
});

test("the card names the checker and, beside it, the level the check runs at", () => {
  for (const [over, model, level] of [
    [
      { checkModel: "hub/think", checkReasoning: "medium" },
      "hub/think",
      "medium",
    ],
    [
      { checkModel: "hub/think", checkReasoning: "default" },
      "hub/think",
      "default",
    ],
    [{ checkModel: "hub/plain" }, "hub/plain", null],
    [
      {
        model: "hub/think",
        reasoning: "high",
        checkModel: "hub/think",
        checkReasoning: "high",
      },
      "hub/think",
      "high",
    ],
  ] as const) {
    render(<Harness goal={goalOf(over)} actions={actionsMock()} />);
    fireEvent.click(mark());
    expect(within(popover()).getByTestId("goal-checker")).toHaveTextContent(
      new RegExp(`^${model}$`),
    );
    const reasoning = within(popover()).queryByTestId("goal-reasoning");
    if (level === null) {
      // A model without reasoning levels has no Reasoning section.
      expect(reasoning).toBeNull();
    } else {
      expect(reasoning).toHaveTextContent(new RegExp(`^${level}$`));
    }
    // Both are plain cells of the numbers' grid, the level right after the
    // model, last.
    const checkerCell =
      within(popover()).getByTestId("goal-checker").parentElement!;
    expect(checkerCell.className).toBe("");
    expect(checkerCell.parentElement).toHaveClass("goal-numbers");
    if (level !== null) {
      expect(checkerCell.nextElementSibling).toBe(reasoning!.parentElement);
      expect(checkerCell.parentElement!.lastElementChild).toBe(
        reasoning!.parentElement,
      );
    }
    cleanup();
  }
});

test("Edit keeps the model and the level that check the goal", () => {
  const actions = actionsMock();
  render(
    <Harness
      goal={goalOf({ model: "hub/qwen3-coder", reasoning: "high" })}
      actions={actions}
    />,
  );
  fireEvent.click(mark());
  expect(within(popover()).getByTestId("goal-checker")).toHaveTextContent(
    "hub/qwen3-coder",
  );
  expect(within(popover()).getByTestId("goal-reasoning")).toHaveTextContent(
    "high",
  );
  fireEvent.click(within(popover()).getByTestId("goal-edit"));
  fireEvent.change(within(popover()).getByTestId("goal-objective-input"), {
    target: { value: "Ship the lexer too" },
  });
  fireEvent.click(within(popover()).getByTestId("goal-submit"));
  expect(actions.sendPrompt).toHaveBeenCalledWith(
    "/goal --model hub/qwen3-coder --reasoning high Ship the lexer too",
  );
});
