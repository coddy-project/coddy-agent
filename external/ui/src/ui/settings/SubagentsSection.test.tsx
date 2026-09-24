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
  // The workspace the list answers for is the session's own; the tab does
  // not print it.
  expect(screen.getByTestId("subagents-catalog")).not.toHaveTextContent(
    "Workspace",
  );
  // The generated form for the config section stays on the tab.
  expect(screen.getByText("Project definitions")).toBeInTheDocument();
});

test("the list only reads: a row folds, nothing acts on a definition", async () => {
  stubFetch();
  renderSection("/work/repo");
  const catalog = await screen.findByTestId("subagents-catalog");
  await screen.findByTestId("subagents-list");
  // The (i) of the legend explains the list and each row's chevron folds its
  // bounds open; there is no other control.
  const buttons = [...catalog.querySelectorAll("button:not(.field-hint)")];
  expect(buttons).toHaveLength(2);
  for (const b of buttons) {
    expect(b.className).toBe("subagents-toggle");
    expect(b).toHaveAttribute("aria-expanded");
  }
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

// The name is the fold: the app's chevron in front of it, no line of its own
// under the description and no browser disclosure triangle.
test("the chevron beside the name folds the declared bounds open", async () => {
  stubFetch();
  renderSection("/work/repo");
  const declared = await screen.findByTestId("subagent-declared-reviewer");
  const row = screen.getByTestId("subagent-row-reviewer");
  expect(row.querySelector("details, summary")).toBeNull();
  expect(row).not.toHaveTextContent("Declared bounds");

  const toggle = screen.getByTestId("subagent-toggle-reviewer");
  expect(toggle).toHaveTextContent("reviewer");
  expect(toggle.querySelector(".coddy-chevron")).not.toBeNull();
  expect(toggle).toHaveAttribute("aria-expanded", "false");
  expect(toggle).toHaveAttribute("title", "Show declared bounds");
  expect(toggle.getAttribute("aria-controls")).toBe(declared.id);
  expect(declared).not.toBeVisible();

  fireEvent.click(toggle);
  expect(toggle).toHaveAttribute("aria-expanded", "true");
  expect(toggle).toHaveAttribute("title", "Hide declared bounds");
  expect(declared).toBeVisible();
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
  expect(screen.getByTestId("subagent-toggle-reviewer")).toHaveAttribute(
    "title",
    "Показать заявленные ограничения",
  );
});

// The tab is two blocks: the section's settings in their own fieldset, then
// the definitions, on the drawer's 12px rhythm.
test("the subagent settings sit in their own fieldset above the definitions", async () => {
  stubFetch();
  renderSection("/work/repo");
  await screen.findByTestId("subagents-list");

  const settings = screen.getByTestId("settings-group-subagents");
  expect(settings.querySelector("legend")?.textContent).toBe(
    "Subagent settings",
  );
  // What the block is about sits behind the (i) of its legend.
  const hint = settings.querySelector("legend .field-hint");
  expect(hint).not.toBeNull();
  expect(hint).toHaveAttribute("aria-label", "About Subagent settings");
  expect(settings.textContent).toContain("Project definitions");
  const catalog = screen.getByTestId("subagents-catalog");
  expect(
    settings.compareDocumentPosition(catalog) &
      Node.DOCUMENT_POSITION_FOLLOWING,
  ).toBeTruthy();

  const { readFileSync } = await import("node:fs");
  const { dirname, join } = await import("node:path");
  const { fileURLToPath } = await import("node:url");
  const css = readFileSync(
    join(dirname(fileURLToPath(import.meta.url)), "../../styles.css"),
    "utf8",
  );
  const rule = /^\.settings-subagents-section\s*\{([^}]*)\}/m.exec(css);
  expect(rule?.[1]).toMatch(/gap:\s*12px/);
});
