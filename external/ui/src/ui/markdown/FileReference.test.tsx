import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

import { onOpenWorkspaceFile, type OpenFileRequest } from "../files/fileBus";
import { WorkspaceFilesContext } from "../files/workspaceEntries";
import { Markdown } from "./Markdown";

const scope = { sessionId: "s1", workspacePath: "/work/repo" };

let asked: string[][] = [];
let opened: OpenFileRequest[] = [];
let stopListening: () => void = () => {};

beforeEach(() => {
  asked = [];
  opened = [];
  stopListening = onOpenWorkspaceFile((request) => opened.push(request));
  const files = new Set(["internal/foo.go", "docs/a.md", "plans/p.md"]);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
      if (!String(url).endsWith("/workspace/stat"))
        return new Response("unexpected", { status: 500 });
      const { paths } = JSON.parse(String(init?.body)) as { paths: string[] };
      asked.push(paths);
      return Response.json({
        object: "coddy.workspace_stat",
        entries: paths
          .filter((path) => files.has(path) || path === "cmd/coddy")
          .map((path) => ({
            path_rel: path,
            kind: path === "cmd/coddy" ? "directory" : "file",
          })),
      });
    }),
  );
});

afterEach(() => {
  stopListening();
  cleanup();
  vi.unstubAllGlobals();
});

const ANSWER =
  "Open `internal/foo.go:12`, see /work/repo/docs/a.md and [the plan](plans/p.md), " +
  "not `fmt.Println`, `cmd/coddy` or `gone.go`.";

function renderAnswer(text = ANSWER, streaming = false) {
  return render(
    <WorkspaceFilesContext.Provider value={scope}>
      <Markdown text={text} {...(streaming ? { streaming: true } : {})} />
    </WorkspaceFilesContext.Provider>,
  );
}

test("a path an answer names opens in a tab of the Files window once the server says it is a file", async () => {
  renderAnswer();
  const code = await screen.findByRole("link", { name: "internal/foo.go:12" });
  const prose = await screen.findByRole("link", {
    name: "/work/repo/docs/a.md",
  });
  const link = await screen.findByRole("link", { name: "the plan" });

  // Asked once, all together, relative to the chat's workspace.
  expect(asked).toHaveLength(1);
  expect([...asked[0]!].sort()).toEqual(
    [
      "cmd/coddy",
      "docs/a.md",
      "fmt.Println",
      "gone.go",
      "internal/foo.go",
      "plans/p.md",
    ].sort(),
  );

  expect(code).toHaveAttribute(
    "href",
    "#/s/s1/files?path=internal%2Ffoo.go&line=12",
  );
  fireEvent.click(code);
  fireEvent.click(prose);
  fireEvent.click(link);
  expect(opened).toEqual([
    { path: "internal/foo.go", line: 12 },
    { path: "docs/a.md", line: undefined },
    { path: "plans/p.md", line: undefined },
  ]);
});

test("what is not a file of the workspace keeps its look and its copy button", async () => {
  renderAnswer();
  await screen.findByRole("link", { name: "the plan" });
  for (const text of ["fmt.Println", "cmd/coddy", "gone.go"]) {
    const code = screen.getByText(text);
    expect(code).toHaveAttribute("data-testid", "md-inline-code");
    expect(code).toHaveAttribute("role", "button");
  }
  expect(screen.queryByRole("link", { name: "gone.go" })).toBeNull();
});

test("nothing is asked while the answer is still arriving", async () => {
  const { rerender } = renderAnswer(ANSWER, true);
  await new Promise((resolve) => setTimeout(resolve, 80));
  expect(asked).toHaveLength(0);
  expect(screen.queryByRole("link", { name: "internal/foo.go:12" })).toBeNull();

  rerender(
    <WorkspaceFilesContext.Provider value={scope}>
      <Markdown text={ANSWER} />
    </WorkspaceFilesContext.Provider>,
  );
  expect(
    await screen.findByRole("link", { name: "internal/foo.go:12" }),
  ).toBeVisible();
});

test("outside a chat a path stays text and nothing is asked", async () => {
  render(<Markdown text={ANSWER} />);
  await new Promise((resolve) => setTimeout(resolve, 80));
  expect(asked).toHaveLength(0);
  expect(screen.getByText("internal/foo.go:12")).toHaveAttribute(
    "data-testid",
    "md-inline-code",
  );
  expect(screen.getByText(/\/work\/repo\/docs\/a\.md/)).not.toHaveAttribute(
    "href",
  );
  // A link the documentation writes keeps its address.
  expect(screen.getByRole("link", { name: "the plan" })).toHaveAttribute(
    "href",
    "plans/p.md",
  );
});

test("a path outside the chat's workspace is not asked about", async () => {
  renderAnswer("Compare /etc/app/config.yaml with `../other/x.go`.");
  await new Promise((resolve) => setTimeout(resolve, 80));
  await waitFor(() => expect(asked).toHaveLength(0));
  expect(screen.queryByRole("link")).toBeNull();
});
