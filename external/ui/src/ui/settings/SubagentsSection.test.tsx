import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { initLocale } from "../i18n/i18n";
import type { JsonSchema } from "./SchemaForm";
import { SubagentsSection } from "./SubagentsSection";

beforeEach(() => {
  initLocale("en");
});

afterEach(() => {
  cleanup();
  initLocale("en");
  vi.unstubAllGlobals();
});

const schema: JsonSchema = {
  type: "object",
  properties: {
    project_trust: {
      type: "string",
      title: "Project definitions",
      enum: ["ask", "allow", "deny"],
    },
  },
} as JsonSchema;

const listResponse = {
  object: "coddy.subagent_list",
  workspace: "/work/repo",
  policy: "ask",
  items: [
    {
      name: "general",
      description: "General-purpose worker.",
      scope: "builtin",
      builtin: true,
      hidden: false,
      trust: "trusted",
      trusted: true,
      needs_approval: false,
    },
    {
      name: "reviewer",
      description: "Reviews a diff for correctness.",
      scope: "project",
      path: "/work/repo/.coddy/agents/reviewer.md",
      digest: "9f2ca1b3d4e5f607",
      builtin: false,
      hidden: false,
      trust: "needs_approval",
      trusted: false,
      needs_approval: true,
      tools: ["read", "grep"],
      permission_mode: "ask",
      timeout_seconds: 600,
      role_bytes: 4210,
    },
  ],
};

type Call = { url: string; method: string; body?: string };

function stubFetch(responses?: unknown[]) {
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
      const body =
        responses && index < responses.length
          ? responses[index++]
          : listResponse;
      return Promise.resolve({ ok: true, json: async () => body });
    }),
  );
  return calls;
}

function renderSection(workspacePath?: string) {
  return render(
    <SubagentsSection
      schema={schema}
      value={{ project_trust: "ask" }}
      onChange={() => {}}
      workspacePath={workspacePath}
    />,
  );
}

test("lists every definition and offers the shield only on the project one", async () => {
  stubFetch();
  renderSection("/work/repo");
  await screen.findByTestId("subagents-list");

  expect(screen.getByTestId("subagent-row-general")).toBeInTheDocument();
  expect(screen.getByTestId("subagent-row-reviewer")).toBeInTheDocument();
  // A built-in needs no approval, so it carries no control at all.
  expect(screen.queryByTestId("subagent-trust-general")).toBeNull();
  expect(screen.getByTestId("subagent-trust-reviewer")).toBeInTheDocument();
  expect(screen.getByTestId("subagents-pending-hint")).toHaveTextContent(
    "1 definition is waiting for your approval.",
  );
  expect(screen.getByTestId("subagents-workspace")).toHaveTextContent(
    "/work/repo",
  );
  // The generated form for the config section stays on the tab.
  expect(screen.getByText("Project definitions")).toBeInTheDocument();
});

test("an unapproved definition shows its bounds and withholds its description", async () => {
  stubFetch();
  renderSection("/work/repo");
  const note = await screen.findByTestId("subagent-trust-note-reviewer");

  expect(note).toHaveTextContent("/work/repo/.coddy/agents/reviewer.md");
  expect(note).toHaveTextContent("read, grep");
  expect(note).toHaveTextContent("10m");
  expect(note).toHaveTextContent("4 KiB");
  expect(note).toHaveTextContent("9f2ca1b3d4e5");
  // The file's own description is the place a hostile checkout would put
  // instructions; it stays out until the receipt exists.
  const row = screen.getByTestId("subagent-row-reviewer");
  expect(row).not.toHaveTextContent("Reviews a diff for correctness");
  expect(row).toHaveTextContent(
    "Description withheld until you approve this file.",
  );
  // An approved definition shows its description as plain text.
  expect(screen.getByTestId("subagent-row-general")).toHaveTextContent(
    "General-purpose worker.",
  );
});

test("approving posts the workspace and reloads the catalog", async () => {
  const approved = {
    ...listResponse,
    items: listResponse.items.map((i) =>
      i.name === "reviewer"
        ? { ...i, trust: "trusted", trusted: true, needs_approval: false }
        : i,
    ),
  };
  // list, trust, list again
  const calls = stubFetch([
    listResponse,
    { object: "coddy.subagent", item: {} },
    approved,
  ]);
  renderSection("/work/repo");
  fireEvent.click(await screen.findByTestId("subagent-trust-reviewer"));

  await waitFor(() => expect(calls.length).toBe(3));
  expect(calls[0]?.url).toBe("/coddy/subagents?cwd=%2Fwork%2Frepo");
  expect(calls[1]).toMatchObject({
    url: "/coddy/subagents/reviewer/trust",
    method: "POST",
    body: JSON.stringify({ cwd: "/work/repo" }),
  });
  // The refreshed row drops the approval notice and keeps a withdraw shield.
  await waitFor(() =>
    expect(screen.queryByTestId("subagent-trust-note-reviewer")).toBeNull(),
  );
  expect(screen.getByTestId("subagent-trust-reviewer")).toHaveAttribute(
    "aria-label",
    "Withdraw approval of subagent reviewer",
  );
  expect(screen.queryByTestId("subagents-pending-hint")).toBeNull();
});

test("no shield is offered when the policy leaves no decision to make", async () => {
  stubFetch([{ ...listResponse, policy: "allow" }]);
  renderSection("/work/repo");
  await screen.findByTestId("subagents-list");
  expect(screen.queryByTestId("subagent-trust-reviewer")).toBeNull();
});

test("without a session workspace the server is left to answer for its own", async () => {
  const calls = stubFetch();
  renderSection(undefined);
  await waitFor(() => expect(calls.length).toBe(1));
  expect(calls[0]?.url).toBe("/coddy/subagents");
});

test("a refused approval says why and keeps the row as it was", async () => {
  const calls: Call[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      calls.push({ url: String(url), method: init?.method ?? "GET" });
      if (init?.method === "POST") {
        return Promise.resolve({
          ok: false,
          status: 404,
          json: async () => ({
            error: { message: 'subagent "reviewer" not found' },
          }),
        });
      }
      return Promise.resolve({ ok: true, json: async () => listResponse });
    }),
  );
  renderSection("/work/repo");
  fireEvent.click(await screen.findByTestId("subagent-trust-reviewer"));
  await screen.findByText(
    'Could not change the approval of reviewer: subagent "reviewer" not found',
  );
  expect(
    screen.getByTestId("subagent-trust-note-reviewer"),
  ).toBeInTheDocument();
  // No reload after a failure: the list on screen is still the true state.
  expect(calls.filter((c) => c.method === "GET").length).toBe(1);
});

test("a failed catalog load says so instead of rendering an empty list", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation(() =>
      Promise.resolve({
        ok: false,
        status: 400,
        json: async () => ({
          error: { message: "cwd must be an absolute path" },
        }),
      }),
    ),
  );
  renderSection("relative");
  await screen.findByText("Could not load the subagent catalog.");
  await waitFor(() =>
    expect(screen.getByTestId("subagents-empty")).toHaveTextContent(
      "No subagent definitions are visible from this workspace.",
    ),
  );
});

test("the catalog reads in Russian", async () => {
  initLocale("ru");
  stubFetch();
  renderSection("/work/repo");
  expect(await screen.findByTestId("subagents-pending-hint")).toHaveTextContent(
    "1 определение ждёт вашего одобрения.",
  );
  expect(screen.getByTestId("subagent-pending-reviewer")).toHaveTextContent(
    "нужно одобрение",
  );
});
