import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { SubagentReadOnlyNotice } from "./SubagentReadOnlyNotice";

afterEach(() => cleanup());

const meta = { parentSessionId: "s_parent", name: "explore", taskId: "bg_3" };

test("names the subagent and links back to the parent chat", () => {
  const onOpenSession = vi.fn();
  render(<SubagentReadOnlyNotice meta={meta} onOpenSession={onOpenSession} />);

  expect(screen.getByTestId("subagent-readonly-notice")).toHaveTextContent(
    "Read-only transcript of subagent explore. Prompts go to the parent chat.",
  );
  const link = screen.getByTestId("subagent-readonly-parent-link");
  expect(link).toHaveAttribute("href", "#/s/s_parent");

  // A plain click opens in place; the href is what new-tab gestures use.
  const notPrevented = fireEvent.click(link);
  expect(notPrevented).toBe(false);
  expect(onOpenSession).toHaveBeenCalledWith("s_parent");
});

test("a modifier click falls through to the href", () => {
  const onOpenSession = vi.fn();
  render(<SubagentReadOnlyNotice meta={meta} onOpenSession={onOpenSession} />);

  fireEvent.click(screen.getByTestId("subagent-readonly-parent-link"), {
    ctrlKey: true,
  });
  expect(onOpenSession).not.toHaveBeenCalled();
});

test("falls back to generic copy without a name and hides the link without a parent", () => {
  render(
    <SubagentReadOnlyNotice
      meta={{ parentSessionId: "", name: "", taskId: "" }}
    />,
  );

  expect(screen.getByTestId("subagent-readonly-notice")).toHaveTextContent(
    "Read-only subagent transcript. Prompts go to the parent chat.",
  );
  expect(screen.queryByTestId("subagent-readonly-parent-link")).toBeNull();
});

test("a scheduled run names its job and links to the job's runs", () => {
  render(
    <SubagentReadOnlyNotice
      meta={{
        parentSessionId: "sess_job",
        name: "nightly",
        taskId: "bg_2",
        scheduler: { jobId: "nightly", trigger: "cron" },
      }}
    />,
  );
  expect(screen.getByTestId("subagent-readonly-notice")).toHaveTextContent(
    "Read-only transcript of a run of scheduler job nightly.",
  );
  expect(screen.queryByTestId("subagent-readonly-parent-link")).toBeNull();
  expect(screen.getByTestId("subagent-readonly-runs-link")).toHaveAttribute(
    "href",
    "#/scheduler/jobs/nightly/runs/bg_2",
  );
});

test("the session of a scheduler job says so and links to the runs list", () => {
  render(
    <SubagentReadOnlyNotice
      meta={{
        parentSessionId: "",
        name: "",
        taskId: "",
        scheduler: { jobId: "nightly", trigger: "" },
        jobSession: true,
      }}
    />,
  );
  expect(screen.getByTestId("subagent-readonly-notice")).toHaveTextContent(
    "This session belongs to scheduler job nightly and holds its runs",
  );
  expect(screen.getByTestId("subagent-readonly-runs-link")).toHaveAttribute(
    "href",
    "#/scheduler/jobs/nightly/runs",
  );
});
