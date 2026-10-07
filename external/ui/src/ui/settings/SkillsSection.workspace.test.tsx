import { afterEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { SkillsSection } from "./SkillsSection";
import type { JsonSchema } from "./SchemaForm";

// The installed list resolves ${CWD} in skills.dirs against the chat's
// workspace, like the Subagents tab: a folder picked before a session exists
// lists its own project skills, and another folder lists its own.

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const schema = {
  type: "object",
  title: "Skills",
  properties: { dirs: { type: "array", items: { type: "string" } } },
} as unknown as JsonSchema;

const BY_FOLDER: Record<string, string[]> = {
  "/projects/data": ["dat-report"],
  "/projects/other": [],
};

function stubFetch() {
  const fetchMock = vi.fn(async (input: string, _init?: RequestInit) => {
    const url = new URL(String(input), "http://x");
    if (url.pathname === "/coddy/skills") {
      const names = BY_FOLDER[url.searchParams.get("cwd") ?? ""] ?? [];
      return {
        ok: true,
        json: async () => ({
          items: names.map((name) => ({
            name,
            description: `${name} skill`,
            file_path: `${url.searchParams.get("cwd")}/.coddy/skills/${name}/SKILL.md`,
            enabled: true,
          })),
        }),
      };
    }
    return { ok: true, json: async () => ({ items: [] }) };
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

test("the installed list follows the workspace of the chat", async () => {
  stubFetch();
  const { rerender } = render(
    <SkillsSection
      schema={schema}
      value={{}}
      onChange={() => {}}
      workspacePath="/projects/data"
    />,
  );
  await waitFor(() => expect(screen.getByText("dat-report")).toBeTruthy());

  rerender(
    <SkillsSection
      schema={schema}
      value={{}}
      onChange={() => {}}
      workspacePath="/projects/other"
    />,
  );
  await waitFor(() => expect(screen.queryByText("dat-report")).toBeNull());
});

test("a list asked for a folder left since does not paint over the current one", async () => {
  let releaseData: () => void = () => {};
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string) => {
      const url = new URL(String(input), "http://x");
      if (url.pathname !== "/coddy/skills") {
        return { ok: true, json: async () => ({ items: [] }) };
      }
      const cwd = url.searchParams.get("cwd") ?? "";
      if (cwd === "/projects/data") {
        await new Promise<void>((resolve) => {
          releaseData = resolve;
        });
      }
      const names = BY_FOLDER[cwd] ?? [];
      return {
        ok: true,
        json: async () => ({
          items: names.map((name) => ({
            name,
            description: `${name} skill`,
            file_path: `${cwd}/.coddy/skills/${name}/SKILL.md`,
            enabled: true,
          })),
        }),
      };
    }),
  );
  const { rerender } = render(
    <SkillsSection
      schema={schema}
      value={{}}
      onChange={() => {}}
      workspacePath="/projects/data"
    />,
  );
  rerender(
    <SkillsSection
      schema={schema}
      value={{}}
      onChange={() => {}}
      workspacePath="/projects/other"
    />,
  );
  await new Promise((resolve) => setTimeout(resolve, 20));
  releaseData();
  await new Promise((resolve) => setTimeout(resolve, 20));
  expect(screen.queryByText("dat-report")).toBeNull();
});

test("deleting a project skill asks in the workspace the list was read for", async () => {
  const fetchMock = stubFetch();
  render(
    <SkillsSection
      schema={schema}
      value={{}}
      onChange={() => {}}
      workspacePath="/projects/data"
    />,
  );
  await waitFor(() => expect(screen.getByText("dat-report")).toBeTruthy());
  fireEvent.click(screen.getByRole("button", { name: /dat-report/i }));
  await waitFor(() =>
    expect(
      fetchMock.mock.calls.some(
        ([input, init]) =>
          (init as RequestInit | undefined)?.method === "DELETE" &&
          String(input) === "/coddy/skills/dat-report?cwd=%2Fprojects%2Fdata",
      ),
    ).toBe(true),
  );
});

// The install search and the update check follow the viewed session, like
// the marketplaces list they come from: a project marketplace approved in
// that workspace is offered, one held there is neither checked nor updated.
test("the install search and the update check go to the viewed session", async () => {
  const seen: Array<{ url: string; session: string | null }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      seen.push({
        url: String(input),
        session: new Headers(init?.headers).get("X-Coddy-Session-ID"),
      });
      return { ok: true, json: async () => ({ items: [], entries: [] }) };
    }),
  );
  render(
    <SkillsSection
      schema={schema}
      value={{}}
      onChange={() => {}}
      workspacePath="/projects/data"
      activeSessionId="sess_data"
    />,
  );
  fireEvent.focus(await screen.findByTestId("skills-install-input"));
  await waitFor(() =>
    expect(seen.find((c) => c.url === "/coddy/skills/available")?.session).toBe(
      "sess_data",
    ),
  );
  fireEvent.click(screen.getByTestId("skills-sync-all"));
  await waitFor(() =>
    expect(seen.find((c) => c.url === "/coddy/skills/updates")?.session).toBe(
      "sess_data",
    ),
  );
});
