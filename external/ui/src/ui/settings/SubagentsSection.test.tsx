import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
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
      path: "(embedded)",
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

function stubFetch(body: unknown = listResponse) {
  const urls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: string) => {
      urls.push(String(url));
      return Promise.resolve({ ok: true, json: async () => body });
    }),
  );
  return urls;
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

test("lists every definition of the session workspace with its scope, description and file", async () => {
  const urls = stubFetch();
  renderSection("/work/repo");
  await screen.findByTestId("subagents-list");

  expect(urls[0]).toBe("/coddy/subagents?cwd=%2Fwork%2Frepo");
  const general = screen.getByTestId("subagent-row-general");
  expect(general).toHaveTextContent("built in");
  expect(general).toHaveTextContent("General-purpose worker.");
  const reviewer = screen.getByTestId("subagent-row-reviewer");
  expect(reviewer).toHaveTextContent("from the project");
  expect(reviewer).toHaveTextContent("Reviews a diff for correctness.");
  expect(screen.getByTestId("subagent-file-reviewer")).toHaveTextContent(
    "/work/repo/.coddy/agents/reviewer.md",
  );
  expect(screen.getByTestId("subagents-workspace")).toHaveTextContent(
    "/work/repo",
  );
  // The generated form for the config section stays on the tab.
  expect(screen.getByText("Project definitions")).toBeInTheDocument();
});

test("the list only reads: no definition carries a control", async () => {
  stubFetch();
  renderSection("/work/repo");
  const catalog = await screen.findByTestId("subagents-catalog");
  await screen.findByTestId("subagents-list");
  expect(catalog.querySelectorAll("button")).toHaveLength(0);
});

test("a definition awaiting approval says so and how, with nothing to click", async () => {
  stubFetch();
  renderSection("/work/repo");
  const badge = await screen.findByTestId("subagent-pending-reviewer");
  expect(badge).toHaveTextContent("needs approval");
  expect(badge).toHaveAttribute(
    "title",
    "Spawning it is refused until it is approved for this workspace: coddy agents trust reviewer",
  );
  expect(screen.queryByTestId("subagent-pending-general")).toBeNull();
});

test("the declared bounds sit behind a disclosure on every row", async () => {
  stubFetch();
  renderSection("/work/repo");
  const declared = await screen.findByTestId("subagent-declared-reviewer");
  expect(declared.tagName).toBe("DETAILS");
  expect(declared).toHaveTextContent("Declared bounds");
  expect(declared).toHaveTextContent("read, grep");
  expect(declared).toHaveTextContent("10m");
  expect(declared).toHaveTextContent("4 KiB");
  // A built-in that declares nothing reads as inheriting.
  expect(screen.getByTestId("subagent-declared-general")).toHaveTextContent(
    "everything the spawning session can call",
  );
});

test("without a session workspace the server is left to answer for its own", async () => {
  const urls = stubFetch();
  renderSection(undefined);
  await waitFor(() => expect(urls.length).toBe(1));
  expect(urls[0]).toBe("/coddy/subagents");
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
  expect(
    await screen.findByTestId("subagent-pending-reviewer"),
  ).toHaveTextContent("нужно одобрение");
  expect(screen.getByTestId("subagent-row-reviewer")).toHaveTextContent(
    "из проекта",
  );
  expect(screen.getByTestId("subagent-declared-reviewer")).toHaveTextContent(
    "Заявленные ограничения",
  );
});
