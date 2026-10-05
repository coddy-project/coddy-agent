import React from "react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { DiffViewerModal } from "./DiffViewerModal";
import type { SessionChanges } from "./types";
import { t } from "../i18n/i18n";

const PATCH = [
  "--- a/src/a.ts",
  "+++ b/src/a.ts",
  "@@ -1,3 +1,3 @@",
  " keep",
  "-old",
  "+new",
  " tail",
].join("\n");

const SESSION: SessionChanges = {
  sessionId: "s1",
  scope: "session",
  files: [
    {
      path: "src/a.ts",
      status: "modified",
      additions: 1,
      deletions: 1,
      binary: false,
      truncated: false,
    },
    {
      path: "docs/b.md",
      status: "added",
      additions: 4,
      deletions: 0,
      binary: false,
      truncated: false,
    },
  ],
  totals: { files: 2, additions: 5, deletions: 1 },
};

const TURN: SessionChanges = {
  sessionId: "s1",
  scope: "turn",
  files: [SESSION.files[0]!],
  totals: { files: 1, additions: 1, deletions: 1 },
};

const UNCOMMITTED: SessionChanges = {
  sessionId: "s1",
  scope: "uncommitted",
  files: [SESSION.files[0]!],
  totals: { files: 1, additions: 1, deletions: 1 },
  untracked: 3,
  vcsAvailable: true,
};

function jsonResponse(body: unknown) {
  return {
    ok: true,
    status: 200,
    json: async () => body,
  } as unknown as Response;
}

let fetchMock: ReturnType<typeof vi.fn>;
let scrolled: string[];

beforeEach(() => {
  document.cookie = "coddy_diff_view=; Path=/; Max-Age=0";
  scrolled = [];
  // jsdom implements neither, and both are how the window navigates and copies.
  Element.prototype.scrollIntoView = function scrollIntoView(this: Element) {
    scrolled.push(this.getAttribute("data-testid") || "");
  };
  Object.assign(navigator, {
    clipboard: { writeText: vi.fn(async () => undefined) },
  });

  fetchMock = vi.fn(async (input: unknown) => {
    const url = String(input);
    if (url.includes("/changes/file")) {
      return jsonResponse({ patch: PATCH });
    }
    if (url.includes("scope=turn")) {
      return jsonResponse(TURN);
    }
    if (url.includes("scope=uncommitted")) {
      return jsonResponse(UNCOMMITTED);
    }
    return jsonResponse(SESSION);
  });
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function open() {
  return render(<DiffViewerModal open sessionId="s1" onClose={() => {}} />);
}

test("lists every changed file with its counts and totals", async () => {
  open();
  const viewer = await screen.findByTestId("diff-viewer");
  await screen.findByTestId("dv-file-src/a.ts");
  expect(screen.getByTestId("dv-file-docs/b.md")).toBeTruthy();
  expect(within(viewer).getByTestId("dv-totals")).toHaveTextContent("+5");
  expect(within(viewer).getByTestId("dv-totals")).toHaveTextContent("−1");
});

test("warns when the per-file response truncates a patch", async () => {
  fetchMock.mockImplementation(async (input: unknown) =>
    jsonResponse(
      String(input).includes("/changes/file")
        ? { patch: PATCH, truncated: true }
        : SESSION,
    ),
  );
  open();
  await screen.findAllByText(t("changes.truncated"));
});

test("draws the unified view by default and never writes filler line counts", async () => {
  open();
  await waitFor(() =>
    expect(document.body.querySelector(".dv-diff--unified")).toBeTruthy(),
  );
  expect(document.body.querySelector(".dv-diff--split")).toBeNull();
  // The reference UI prints "N unmodified lines" between hunks; this must not.
  expect(document.body.textContent || "").not.toMatch(/unmodified/i);
});

test("the view toggle switches to the split view and back", async () => {
  open();
  await waitFor(() =>
    expect(document.body.querySelector(".dv-diff--unified")).toBeTruthy(),
  );

  fireEvent.click(screen.getByTestId("dv-toggle-view"));
  await waitFor(() =>
    expect(document.body.querySelector(".dv-diff--split")).toBeTruthy(),
  );
  expect(document.body.querySelector(".dv-diff--unified")).toBeNull();

  fireEvent.click(screen.getByTestId("dv-toggle-view"));
  await waitFor(() =>
    expect(document.body.querySelector(".dv-diff--unified")).toBeTruthy(),
  );
});

test("switching the scope refetches and updates the totals", async () => {
  open();
  await screen.findByTestId("dv-file-docs/b.md");

  fireEvent.change(screen.getByTestId("dv-scope"), {
    target: { value: "turn" },
  });

  await waitFor(() => {
    expect(
      fetchMock.mock.calls.some((c) => String(c[0]).includes("scope=turn")),
    ).toBe(true);
  });
  await waitFor(() =>
    expect(screen.queryByTestId("dv-file-docs/b.md")).toBeNull(),
  );
  expect(screen.getByTestId("dv-totals")).toHaveTextContent("+1");
});

test("the per-file patch is fetched in the scope the list was read in", async () => {
  open();
  await screen.findByTestId("dv-file-src/a.ts");
  fireEvent.change(screen.getByTestId("dv-scope"), {
    target: { value: "uncommitted" },
  });

  await waitFor(() => {
    const detail = fetchMock.mock.calls
      .map((c) => String(c[0]))
      .filter((u) => u.includes("/changes/file"));
    expect(detail.some((u) => u.includes("scope=uncommitted"))).toBe(true);
  });
});

test("collapse all hides every diff body and expand all brings them back", async () => {
  open();
  await waitFor(() =>
    expect(document.body.querySelector(".dv-file-body")).toBeTruthy(),
  );

  fireEvent.click(screen.getByTestId("dv-toggle-all"));
  await waitFor(() =>
    expect(document.body.querySelector(".dv-file-body")).toBeNull(),
  );

  fireEvent.click(screen.getByTestId("dv-toggle-all"));
  await waitFor(() =>
    expect(document.body.querySelector(".dv-file-body")).toBeTruthy(),
  );
});

test("a file header collapses only its own diff", async () => {
  open();
  await waitFor(() =>
    expect(document.body.querySelectorAll(".dv-file-body")).toHaveLength(2),
  );
  fireEvent.click(screen.getByTestId("dv-file-toggle-src/a.ts"));
  await waitFor(() =>
    expect(document.body.querySelectorAll(".dv-file-body")).toHaveLength(1),
  );
});

test("go to file filters the list and scrolls to the pick", async () => {
  open();
  await screen.findByTestId("dv-file-src/a.ts");

  fireEvent.click(screen.getByTestId("dv-goto"));
  const menu = await screen.findByTestId("dv-goto-menu");
  expect(within(menu).getByTestId("dv-goto-row-docs/b.md")).toBeTruthy();

  fireEvent.change(screen.getByTestId("dv-goto-input"), {
    target: { value: "b.md" },
  });
  await waitFor(() =>
    expect(screen.queryByTestId("dv-goto-row-src/a.ts")).toBeNull(),
  );

  fireEvent.click(screen.getByTestId("dv-goto-row-docs/b.md"));
  expect(scrolled).toContain("dv-file-docs/b.md");
  // Picking a file closes the menu, so the list is out of the way of the diff.
  expect(screen.queryByTestId("dv-goto-menu")).toBeNull();
});

test("the file tree opens on demand and scrolls to a picked file", async () => {
  open();
  await screen.findByTestId("dv-file-src/a.ts");
  expect(screen.queryByTestId("dv-tree")).toBeNull();

  fireEvent.click(screen.getByTestId("dv-toggle-tree"));
  await screen.findByTestId("dv-tree");

  fireEvent.click(screen.getByTestId("dv-tree-file-docs/b.md"));
  expect(scrolled).toContain("dv-file-docs/b.md");
});

test("the header copies the file path", async () => {
  open();
  await screen.findByTestId("dv-file-src/a.ts");
  fireEvent.click(screen.getByTestId("dv-copy-src/a.ts"));
  expect(navigator.clipboard.writeText).toHaveBeenCalledWith("src/a.ts");
});

test("the uncommitted scope says how many untracked files it skipped", async () => {
  open();
  await screen.findByTestId("dv-file-src/a.ts");
  fireEvent.change(screen.getByTestId("dv-scope"), {
    target: { value: "uncommitted" },
  });
  const banner = await screen.findByTestId("dv-untracked");
  expect(banner.textContent || "").toContain("3");
});

test("an unversioned workspace explains itself instead of showing an empty diff", async () => {
  fetchMock.mockImplementation(async (input: unknown) => {
    const url = String(input);
    if (url.includes("scope=uncommitted")) {
      return jsonResponse({
        sessionId: "s1",
        scope: "uncommitted",
        files: [],
        totals: { files: 0, additions: 0, deletions: 0 },
        untracked: 0,
        vcsAvailable: false,
      });
    }
    if (url.includes("/changes/file")) {
      return jsonResponse({ patch: PATCH });
    }
    return jsonResponse(SESSION);
  });

  open();
  await screen.findByTestId("dv-file-src/a.ts");
  fireEvent.change(screen.getByTestId("dv-scope"), {
    target: { value: "uncommitted" },
  });
  await screen.findByTestId("dv-no-vcs");
});

test("colours the code when the file has a known language", async () => {
  const tsPatch = [
    "--- a/src/a.ts",
    "+++ b/src/a.ts",
    "@@ -1,2 +1,2 @@",
    "-const before = 1;",
    "+const after = 2;",
  ].join("\n");
  fetchMock.mockImplementation(async (input: unknown) => {
    const url = String(input);
    if (url.includes("/changes/file")) {
      return jsonResponse({ patch: tsPatch });
    }
    return jsonResponse({
      sessionId: "s1",
      scope: "session",
      files: [SESSION.files[0]!],
      totals: { files: 1, additions: 1, deletions: 1 },
    });
  });

  open();
  await screen.findByTestId("dv-file-src/a.ts");
  await waitFor(() => {
    expect(document.body.querySelector(".hljs-keyword")).toBeTruthy();
  });
  // Colouring must not disturb the text: the line still reads as written.
  const line = document.body.querySelector(".dv-code");
  expect(line?.textContent).toBe("const before = 1;");
});

// A file the highlighter has no grammar for still renders, just uncoloured.
test("leaves an unknown file type as plain text", async () => {
  fetchMock.mockImplementation(async (input: unknown) => {
    const url = String(input);
    if (url.includes("/changes/file")) {
      return jsonResponse({
        patch: [
          "--- a/notes.txt",
          "+++ b/notes.txt",
          "@@ -1 +1 @@",
          "-a",
          "+b",
        ].join("\n"),
      });
    }
    return jsonResponse({
      sessionId: "s1",
      scope: "session",
      files: [{ ...SESSION.files[0]!, path: "notes.txt" }],
      totals: { files: 1, additions: 1, deletions: 1 },
    });
  });

  open();
  await screen.findByTestId("dv-file-notes.txt");
  await waitFor(() =>
    expect(document.body.querySelector(".dv-code")).toBeTruthy(),
  );
  expect(document.body.querySelector(".hljs-keyword")).toBeNull();
});

test("the all-files scope offers untracked files the git scope leaves out", async () => {
  fetchMock.mockImplementation(async (input: unknown) => {
    const url = String(input);
    if (url.includes("/changes/file")) {
      return jsonResponse({ patch: PATCH });
    }
    if (url.includes("scope=all")) {
      return jsonResponse({
        sessionId: "s1",
        scope: "all",
        files: [
          SESSION.files[0]!,
          { ...SESSION.files[1]!, path: "brand-new.txt", status: "added" },
        ],
        totals: { files: 2, additions: 5, deletions: 1 },
        untracked: 0,
        vcsAvailable: true,
      });
    }
    return jsonResponse(SESSION);
  });

  open();
  await screen.findByTestId("dv-file-src/a.ts");
  fireEvent.change(screen.getByTestId("dv-scope"), {
    target: { value: "all" },
  });

  await screen.findByTestId("dv-file-brand-new.txt");
  // Nothing was skipped, so the scope makes no excuses for itself.
  expect(screen.queryByTestId("dv-untracked")).toBeNull();
});

// Past the cap the scope says how much it left out, and says it differently
// from the tracked-only scope: there the omission is the rule, here a limit.
test("the all-files scope reports files it had to leave out", async () => {
  fetchMock.mockImplementation(async (input: unknown) => {
    const url = String(input);
    if (url.includes("/changes/file")) {
      return jsonResponse({ patch: PATCH });
    }
    if (url.includes("scope=all")) {
      return jsonResponse({
        sessionId: "s1",
        scope: "all",
        files: [SESSION.files[0]!],
        totals: { files: 1, additions: 1, deletions: 1 },
        untracked: 12,
        vcsAvailable: true,
      });
    }
    return jsonResponse(SESSION);
  });

  open();
  await screen.findByTestId("dv-file-src/a.ts");
  fireEvent.change(screen.getByTestId("dv-scope"), {
    target: { value: "all" },
  });

  const banner = await screen.findByTestId("dv-untracked");
  expect(banner.textContent || "").toContain("12");
  expect(banner.textContent || "").not.toContain("Only tracked changes");
});
