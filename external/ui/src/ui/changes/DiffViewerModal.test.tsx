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
import { ConfirmProvider } from "../components/useConfirm";
import { emitChangesSettled } from "./sessionChangesBus";

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
  vcs: "git",
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
    return jsonResponse(SESSION);
  });
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

/** The shared confirmation dialog, over the edits window (itself a dialog). */
async function confirmDialog(): Promise<HTMLElement> {
  let found: HTMLElement | null = null;
  await waitFor(() => {
    found = document.body.querySelector<HTMLElement>(".confirm-dialog");
    expect(found).toBeTruthy();
  });
  return found!;
}

function open() {
  return render(
    <ConfirmProvider>
      <DiffViewerModal open sessionId="s1" onClose={() => {}} />
    </ConfirmProvider>,
  );
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

test("every new report of git reads the patches again, the old ones staying on screen meanwhile", async () => {
  open();
  await waitFor(() =>
    expect(document.body.querySelectorAll(".dv-file-body")).toHaveLength(2),
  );
  const details = () =>
    fetchMock.mock.calls.map((c) => String(c[0])).filter((u) => u.includes("/changes/file"));
  await waitFor(() => expect(details()).toHaveLength(2));
  await waitFor(() => expect(document.body.querySelector(".dv-code")).toBeTruthy());
  let release: () => void = () => {};
  const held = new Promise<void>((r) => (release = r));
  fetchMock.mockImplementation(async (input: unknown) => {
    const url = String(input);
    if (url.includes("/changes/file")) {
      await held;
      return jsonResponse({ patch: PATCH });
    }
    return jsonResponse({
      ...SESSION,
      files: [{ ...SESSION.files[0]!, additions: 3 }, SESSION.files[1]!],
      totals: { files: 2, additions: 7, deletions: 1 },
    });
  });
  emitChangesSettled("s1");
  await waitFor(() => expect(screen.getByTestId("dv-totals")).toHaveTextContent("+7"));
  await waitFor(() => expect(details()).toHaveLength(4));
  // While the new patches are on their way the old ones are still drawn.
  expect(document.body.querySelector(".dv-code")).toBeTruthy();
  release();
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

test("says how many new files it left out", async () => {
  fetchMock.mockImplementation(async (input: unknown) => {
    const url = String(input);
    if (url.includes("/changes/file")) {
      return jsonResponse({ patch: PATCH });
    }
    return jsonResponse({ ...SESSION, skipped: 12 });
  });
  open();
  const banner = await screen.findByTestId("dv-skipped");
  expect(banner.textContent || "").toContain("12");
});

test("a folder in no repository explains itself instead of showing an empty diff", async () => {
  fetchMock.mockImplementation(async () =>
    jsonResponse({
      sessionId: "s1",
      vcs: "",
      files: [],
      totals: { files: 0, additions: 0, deletions: 0 },
      skipped: 0,
    }),
  );
  open();
  await screen.findByTestId("dv-no-vcs");
  expect(screen.queryByTestId("dv-discard-all")).toBeNull();
});

test("discarding a file asks first, then puts it back through the server", async () => {
  open();
  await screen.findByTestId("dv-file-src/a.ts");
  fireEvent.click(screen.getByTestId("dv-discard-src/a.ts"));
  const dialog = await confirmDialog();
  expect(dialog.textContent || "").toContain("a.ts");
  fireEvent.click(within(dialog).getByRole("button", { name: t("changes.discardYes") }));
  await waitFor(() => {
    const post = fetchMock.mock.calls.find((c) => String(c[0]).endsWith("/changes/revert"));
    expect(post).toBeTruthy();
    expect(JSON.parse(String((post![1] as RequestInit).body))).toEqual({ paths: ["src/a.ts"] });
  });
});

test("discarding everything asks first, and a refusal touches nothing", async () => {
  open();
  await screen.findByTestId("dv-file-src/a.ts");
  fireEvent.click(screen.getByTestId("dv-discard-all"));
  let dialog = await confirmDialog();
  fireEvent.click(within(dialog).getByRole("button", { name: t("common.cancel") }));
  await waitFor(() => expect(document.body.querySelector(".confirm-dialog")).toBeNull());
  expect(fetchMock.mock.calls.some((c) => String(c[0]).endsWith("/changes/revert"))).toBe(false);

  fireEvent.click(screen.getByTestId("dv-discard-all"));
  dialog = await confirmDialog();
  fireEvent.click(within(dialog).getByRole("button", { name: t("changes.discardYes") }));
  await waitFor(() => {
    const post = fetchMock.mock.calls.find((c) => String(c[0]).endsWith("/changes/revert"));
    expect(JSON.parse(String((post![1] as RequestInit).body))).toEqual({ all: true });
  });
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
      vcs: "git",
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
      vcs: "git",
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

// The window answers only an Escape nobody nearer claimed: the confirmation
// dialog over it takes its own, and closing both at once lost the question.
test("Escape on the discard question closes the question, not the window", async () => {
  const onClose = vi.fn();
  render(
    <ConfirmProvider>
      <DiffViewerModal open sessionId="s1" onClose={onClose} />
    </ConfirmProvider>,
  );
  await screen.findByTestId("dv-file-src/a.ts");
  fireEvent.click(screen.getByTestId("dv-discard-all"));
  await confirmDialog();
  fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" });
  await waitFor(() => expect(document.body.querySelector(".confirm-dialog")).toBeNull());
  expect(onClose).not.toHaveBeenCalled();
  fireEvent.keyDown(document.body, { key: "Escape" });
  expect(onClose).toHaveBeenCalledTimes(1);
});

test("a first read that fails says why instead of an empty window", async () => {
  fetchMock.mockImplementation(async () => ({
    ok: false,
    status: 503,
    json: async () => ({ error: { message: "server restarting" } }),
  }) as unknown as Response);
  open();
  const note = await screen.findByTestId("dv-error");
  expect(note.textContent).toContain("server restarting");
});

test("a file edited again with the same counts shows its new diff", async () => {
  let body = "new";
  fetchMock.mockImplementation(async (input: unknown) => {
    const url = String(input);
    if (url.includes("/changes/file")) {
      return jsonResponse({ patch: PATCH.replace("+new", "+" + body) });
    }
    return jsonResponse({ ...SESSION, files: [SESSION.files[0]!], totals: { files: 1, additions: 1, deletions: 1 } });
  });
  open();
  await waitFor(() => expect(document.body.textContent).toContain("new"));
  body = "rewritten";
  emitChangesSettled("s1");
  await waitFor(() => expect(document.body.textContent).toContain("rewritten"));
});

test("new files git could not list can still be discarded", async () => {
  fetchMock.mockImplementation(async () =>
    jsonResponse({ sessionId: "s1", vcs: "git", files: [], totals: { files: 0, additions: 0, deletions: 0 }, skipped: 3 }),
  );
  open();
  await screen.findByTestId("dv-skipped");
  expect(screen.getByTestId("dv-discard-all")).toBeTruthy();
});
