import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { initLocale } from "../i18n/i18n";
import type { JsonSchema } from "./SchemaForm";
import { SubagentsSection } from "./SubagentsSection";
import {
  noteSettingsConfigSaved,
  resetSettingsConfigForTests,
} from "./settingsConfigStore";

beforeEach(() => {
  initLocale("en");
});

afterEach(() => {
  cleanup();
  initLocale("en");
  vi.unstubAllGlobals();
  resetSettingsConfigForTests();
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

test("a row folds its bounds open; only a project definition under ask has a shield", async () => {
  stubFetch();
  renderSection("/work/repo");
  const catalog = await screen.findByTestId("subagents-catalog");
  await screen.findByTestId("subagents-list");
  // The (i) of the legend explains the list, each row's chevron folds its
  // bounds open, and the project file the policy holds carries the MCP
  // shield. Nothing else acts on a definition.
  const buttons = [...catalog.querySelectorAll("button:not(.field-hint)")];
  expect(buttons).toHaveLength(3);
  const toggles = buttons.filter((b) => b.className === "subagents-toggle");
  expect(toggles).toHaveLength(2);
  for (const b of toggles) expect(b).toHaveAttribute("aria-expanded");
  const shield = screen.getByTestId("subagent-trust-reviewer");
  expect(buttons).toContain(shield);
  expect(shield.querySelector("svg")).not.toBeNull();
  expect(screen.queryByTestId("subagent-trust-general")).toBeNull();
});

test("a definition awaiting approval says so and how", async () => {
  stubFetch();
  renderSection("/work/repo");
  const badge = await screen.findByTestId("subagent-pending-reviewer");
  expect(badge).toHaveTextContent("needs approval");
  expect(badge).toHaveAttribute(
    "title",
    "Spawning it is refused until it is approved for this workspace: the shield, or coddy agents trust reviewer",
  );
  expect(screen.queryByTestId("subagent-pending-general")).toBeNull();
  const shield = screen.getByTestId("subagent-trust-reviewer");
  expect(shield).toHaveClass("settings-btn-approve");
  expect(shield).not.toHaveClass("is-trusted");
  expect(shield).toHaveAttribute(
    "title",
    "Approve spawning reviewer in this workspace",
  );
  expect(shield).toHaveAttribute("aria-label", "Approve subagent reviewer");
});

type Call = { url: string; method: string; body: unknown };

// Answers the catalog GET with the listing current at the time, and records
// every request; a trust or untrust POST answers with the given response and
// moves the listing to the given next state.
function stubTrustFlow(opts: {
  after: unknown;
  trustResponse?: { ok: boolean; status: number; body: unknown };
}) {
  const calls: Call[] = [];
  let listing: unknown = listResponse;
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: string, init?: RequestInit) => {
      const method = init?.method ?? "GET";
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      calls.push({ url: String(url), method, body });
      if (method === "POST") {
        const res = opts.trustResponse ?? { ok: true, status: 200, body: {} };
        if (res.ok) listing = opts.after;
        return Promise.resolve({
          ok: res.ok,
          status: res.status,
          json: async () => res.body,
        });
      }
      return Promise.resolve({ ok: true, json: async () => listing });
    }),
  );
  return calls;
}

const approvedListing = {
  ...listResponse,
  items: listResponse.items.map((item) =>
    item.name === "reviewer"
      ? { ...item, trust: "trusted", trusted: true, needs_approval: false }
      : item,
  ),
};

test("the shield approves a project definition for the workspace, bound to the file it showed", async () => {
  const calls = stubTrustFlow({ after: approvedListing });
  renderSection("/work/repo");
  fireEvent.click(await screen.findByTestId("subagent-trust-reviewer"));

  await waitFor(() =>
    expect(screen.getByTestId("subagent-trust-reviewer")).toHaveClass(
      "is-trusted",
    ),
  );
  const post = calls.find((c) => c.method === "POST");
  expect(post?.url).toBe("/coddy/subagents/reviewer/trust");
  // The digest of the file the row showed: a file the checkout rewrote since
  // is refused rather than approved unseen.
  expect(post?.body).toEqual({ cwd: "/work/repo", digest: "9f2ca1b3d4e5f607" });
  // The list is read again after the change.
  expect(calls.filter((c) => c.method === "GET")).toHaveLength(2);
  expect(screen.queryByTestId("subagent-pending-reviewer")).toBeNull();
  expect(screen.getByTestId("subagent-trust-reviewer")).toHaveAttribute(
    "title",
    "Approved for this workspace, click to withdraw",
  );
});

test("the shield of an approved definition withdraws the approval", async () => {
  const calls = stubTrustFlow({ after: listResponse });
  // Start from the approved listing: the first GET answers with it.
  vi.mocked(fetch).mockImplementationOnce(() =>
    Promise.resolve({
      ok: true,
      json: async () => approvedListing,
    } as Response),
  );
  renderSection("/work/repo");
  const shield = await screen.findByTestId("subagent-trust-reviewer");
  await waitFor(() => expect(shield).toHaveClass("is-trusted"));
  expect(shield).toHaveAttribute(
    "aria-label",
    "Withdraw the approval of subagent reviewer",
  );
  fireEvent.click(shield);

  await screen.findByTestId("subagent-pending-reviewer");
  const post = calls.find((c) => c.method === "POST");
  expect(post?.url).toBe("/coddy/subagents/reviewer/untrust");
  expect(post?.body).toEqual({ cwd: "/work/repo" });
});

test("a refused approval says why and leaves the definition held", async () => {
  stubTrustFlow({
    after: approvedListing,
    trustResponse: {
      ok: false,
      status: 409,
      body: {
        error: {
          message:
            "subagent reviewer changed since it was listed; review it again",
        },
      },
    },
  });
  renderSection("/work/repo");
  fireEvent.click(await screen.findByTestId("subagent-trust-reviewer"));

  await screen.findByText(
    "subagent reviewer changed since it was listed; review it again",
  );
  expect(screen.getByTestId("subagent-pending-reviewer")).toBeInTheDocument();
  expect(screen.getByTestId("subagent-trust-reviewer")).not.toHaveClass(
    "is-trusted",
  );
});

test("no shield where approving means nothing: allow, deny, a built-in or a file of yours", async () => {
  const mine = {
    name: "mine",
    description: "One of my own.",
    scope: "user",
    path: "/home/me/.coddy/agents/mine.md",
    digest: "0011223344556677",
    builtin: false,
    hidden: false,
    trust: "trusted",
    trusted: true,
    needs_approval: false,
  };
  for (const policy of ["allow", "deny"]) {
    cleanup();
    stubFetch({
      ...listResponse,
      policy,
      items: [
        ...listResponse.items.map((item) =>
          item.name === "reviewer"
            ? {
                ...item,
                trust: policy === "allow" ? "trusted" : "denied",
                trusted: policy === "allow",
                needs_approval: false,
              }
            : item,
        ),
        mine,
      ],
    });
    renderSection("/work/repo");
    await screen.findByTestId("subagent-row-mine");
    expect(screen.queryByTestId("subagent-trust-reviewer")).toBeNull();
    expect(screen.queryByTestId("subagent-trust-mine")).toBeNull();
    expect(screen.queryByTestId("subagent-trust-general")).toBeNull();
  }
  // Under ask a file of yours or a built-in still has none.
  cleanup();
  stubFetch({ ...listResponse, items: [...listResponse.items, mine] });
  renderSection("/work/repo");
  await screen.findByTestId("subagent-row-mine");
  expect(screen.getByTestId("subagent-trust-reviewer")).toBeInTheDocument();
  expect(screen.queryByTestId("subagent-trust-mine")).toBeNull();
  expect(screen.queryByTestId("subagent-trust-general")).toBeNull();
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

// A save of the settings may change subagents.project_trust: the catalog is
// read again, so the shields and badges follow the saved policy.
test("a save of the settings reads the catalog again", async () => {
  const urls = stubFetch();
  renderSection("/work/repo");
  await screen.findByTestId("subagents-list");
  expect(urls).toHaveLength(1);
  act(() => {
    noteSettingsConfigSaved({ subagents: { project_trust: "allow" } });
  });
  await waitFor(() => expect(urls).toHaveLength(2));
});
