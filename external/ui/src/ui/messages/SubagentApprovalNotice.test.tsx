import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { initLocale } from "../i18n/i18n";
import { SubagentApprovalNotice } from "./SubagentApprovalNotice";

beforeEach(() => {
  initLocale("en");
  window.location.hash = "";
});

afterEach(() => {
  cleanup();
  initLocale("en");
  vi.unstubAllGlobals();
});

function catalog(needsApproval: boolean) {
  return {
    object: "coddy.subagent_list",
    workspace: "/work/repo",
    policy: "ask",
    items: [
      {
        name: "reviewer",
        description: "Reviews a diff.",
        scope: "project",
        path: "/work/repo/.coddy/agents/reviewer.md",
        builtin: false,
        hidden: false,
        trust: needsApproval ? "needs_approval" : "trusted",
        trusted: !needsApproval,
        needs_approval: needsApproval,
      },
    ],
  };
}

type Call = { url: string; method: string; body?: string };

function stubFetch(responses: Array<{ ok: boolean; body: unknown }>) {
  const calls: Call[] = [];
  let index = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      calls.push({
        url: String(url),
        method: init?.method ?? "GET",
        ...(typeof init?.body === "string" ? { body: init.body } : {}),
      });
      const res = responses[Math.min(index++, responses.length - 1)]!;
      return Promise.resolve({
        ok: res.ok,
        status: res.ok ? 200 : 404,
        json: async () => res.body,
      });
    }),
  );
  return calls;
}

test("offers the approval when the catalog says the definition is waiting", async () => {
  const calls = stubFetch([{ ok: true, body: catalog(true) }]);
  render(
    <SubagentApprovalNotice agentName="reviewer" workspacePath="/work/repo" />,
  );

  const notice = await screen.findByTestId("subagent-approval-reviewer");
  expect(notice).toHaveTextContent('The subagent "reviewer" comes from a file');
  expect(notice).toHaveTextContent("/work/repo/.coddy/agents/reviewer.md");
  expect(
    screen.getByTestId("subagent-approval-approve-reviewer"),
  ).toBeInTheDocument();
  expect(calls[0]?.url).toBe("/coddy/subagents?cwd=%2Fwork%2Frepo");

  fireEvent.click(screen.getByTestId("subagent-approval-settings-reviewer"));
  expect(window.location.hash).toBe("#/settings/subagents");
});

test("renders nothing when the spawn failed for some other reason", async () => {
  const calls = stubFetch([{ ok: true, body: catalog(false) }]);
  render(
    <SubagentApprovalNotice agentName="reviewer" workspacePath="/work/repo" />,
  );
  await waitFor(() => expect(calls.length).toBe(1));
  expect(screen.queryByTestId("subagent-approval-reviewer")).toBeNull();
});

test("renders nothing for a name the catalog does not list", async () => {
  const calls = stubFetch([{ ok: true, body: catalog(true) }]);
  render(<SubagentApprovalNotice agentName="ghost" />);
  await waitFor(() => expect(calls.length).toBe(1));
  expect(screen.queryByTestId("subagent-approval-ghost")).toBeNull();
});

test("renders nothing when the catalog cannot be reached", async () => {
  const fetchMock = vi.fn().mockRejectedValue(new Error("offline"));
  vi.stubGlobal("fetch", fetchMock);
  render(
    <SubagentApprovalNotice agentName="reviewer" workspacePath="/work/repo" />,
  );
  await waitFor(() => expect(fetchMock).toHaveBeenCalled());
  expect(screen.queryByTestId("subagent-approval-reviewer")).toBeNull();
});

test("approving posts the workspace and never retries the spawn", async () => {
  const calls = stubFetch([
    { ok: true, body: catalog(true) },
    { ok: true, body: { object: "coddy.subagent", item: {} } },
  ]);
  render(
    <SubagentApprovalNotice agentName="reviewer" workspacePath="/work/repo" />,
  );
  fireEvent.click(
    await screen.findByTestId("subagent-approval-approve-reviewer"),
  );

  await waitFor(() => expect(calls.length).toBe(2));
  expect(calls[1]).toMatchObject({
    url: "/coddy/subagents/reviewer/trust",
    method: "POST",
    body: JSON.stringify({ cwd: "/work/repo" }),
  });
  // The confirmation asks the user to request the run again; nothing is
  // started on their behalf.
  await waitFor(() =>
    expect(screen.getByTestId("subagent-approval-reviewer")).toHaveTextContent(
      "Ask again to run it",
    ),
  );
  expect(screen.queryByTestId("subagent-approval-approve-reviewer")).toBeNull();
  expect(calls.filter((c) => c.method === "POST").length).toBe(1);
});

test("a refused approval keeps the button and says why", async () => {
  stubFetch([
    { ok: true, body: catalog(true) },
    {
      ok: false,
      body: { error: { message: 'subagent "reviewer" not found' } },
    },
  ]);
  render(
    <SubagentApprovalNotice agentName="reviewer" workspacePath="/work/repo" />,
  );
  fireEvent.click(
    await screen.findByTestId("subagent-approval-approve-reviewer"),
  );
  await waitFor(() =>
    expect(screen.getByTestId("subagent-approval-reviewer")).toHaveTextContent(
      'Could not record the approval: subagent "reviewer" not found',
    ),
  );
  expect(
    screen.getByTestId("subagent-approval-approve-reviewer"),
  ).not.toBeDisabled();
});
