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
import { EditsView } from "./EditsView";
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

function open(onClose: () => void = () => {}) {
  return render(
    <ConfirmProvider>
      <EditsView
        sessionId="s1"
        workspacePath="/home/dev/shop"
        onClose={onClose}
      />
    </ConfirmProvider>,
  );
}

/** Opens the window's ⋮ menu and returns it. */
function openMenu(): HTMLElement {
  fireEvent.click(screen.getByTestId("edits-more"));
  return screen.getByRole("menu");
}

test("lists every changed file with its counts and totals", async () => {
  open();
  const viewer = await screen.findByTestId("edits-view");
  await screen.findByTestId("dv-file-src/a.ts");
  expect(screen.getByTestId("dv-file-docs/b.md")).toBeTruthy();
  expect(within(viewer).getByTestId("edits-totals")).toHaveTextContent("+5");
  expect(within(viewer).getByTestId("edits-totals")).toHaveTextContent("−1");
});

// The edits window is framed and headed the way the Files window is: the same
// sheet beside the rail, the tree switch and the title with the folder on the
// left, the menu, the expand button and the close button on the right.
test("is framed and headed like the Files window", async () => {
  const onClose = vi.fn();
  open(onClose);
  const viewer = await screen.findByTestId("edits-view");
  expect(viewer).toHaveClass("files-dock-cluster");
  expect(viewer.parentElement).not.toBe(document.body);
  const head = viewer.querySelector(".files-header") as HTMLElement;
  expect(head).toBeTruthy();
  expect(within(head).getByRole("heading").textContent).toBe(
    t("changes.viewer.title"),
  );
  expect(head.querySelector(".files-subtitle")?.textContent).toContain("shop");
  const order = [...head.querySelectorAll("[data-testid]")].map((el) =>
    el.getAttribute("data-testid"),
  );
  expect(order.filter((id) => id !== "edits-totals")).toEqual([
    "edits-toggle-tree",
    "edits-more",
    "edits-expand",
    "edits-close",
  ]);
  // No toolbar of its own: what the old one held lives in the menu.
  expect(viewer.querySelector(".dv-toolbar")).toBeNull();
  expect(screen.queryByTestId("dv-goto")).toBeNull();
  fireEvent.click(screen.getByTestId("edits-expand"));
  expect(viewer).toHaveClass("is-expanded");
  expect(screen.getByTestId("edits-expand")).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  fireEvent.click(screen.getByTestId("edits-close"));
  expect(onClose).toHaveBeenCalledTimes(1);
});

// The menu holds how the diffs are drawn, folding them all, and discarding.
test("the menu holds side by side, collapse all and discard all", async () => {
  open();
  await screen.findByTestId("dv-file-src/a.ts");
  const menu = openMenu();
  const items = [...menu.querySelectorAll("[role^=menuitem]")].map((el) =>
    el.getAttribute("data-testid"),
  );
  expect(items).toEqual([
    "edits-split",
    "edits-toggle-all",
    "edits-discard-all",
  ]);
  expect(within(menu).getByTestId("edits-split")).toHaveAttribute(
    "aria-checked",
    "false",
  );
  expect(menu.querySelector(".files-menu-sep")).toBeTruthy();
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

test("side by side in the menu switches to the split view and back", async () => {
  open();
  await waitFor(() =>
    expect(document.body.querySelector(".dv-diff--unified")).toBeTruthy(),
  );

  fireEvent.click(within(openMenu()).getByTestId("edits-split"));
  await waitFor(() =>
    expect(document.body.querySelector(".dv-diff--split")).toBeTruthy(),
  );
  expect(document.body.querySelector(".dv-diff--unified")).toBeNull();
  // Picking an item puts the menu away.
  expect(screen.queryByRole("menu")).toBeNull();
  expect(within(openMenu()).getByTestId("edits-split")).toHaveAttribute(
    "aria-checked",
    "true",
  );

  fireEvent.click(screen.getByTestId("edits-split"));
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
    fetchMock.mock.calls
      .map((c) => String(c[0]))
      .filter((u) => u.includes("/changes/file"));
  await waitFor(() => expect(details()).toHaveLength(2));
  await waitFor(() =>
    expect(document.body.querySelector(".dv-code")).toBeTruthy(),
  );
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
  await waitFor(() =>
    expect(screen.getByTestId("edits-totals")).toHaveTextContent("+7"),
  );
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

  fireEvent.click(within(openMenu()).getByTestId("edits-toggle-all"));
  await waitFor(() =>
    expect(document.body.querySelector(".dv-file-body")).toBeNull(),
  );

  fireEvent.click(within(openMenu()).getByTestId("edits-toggle-all"));
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

// The tree beside the diffs holds the changed files and nothing else: the
// folders they sit in, each file with git's status, no other file of the
// workspace.
test("the tree lists only the changed files, in their folders", async () => {
  open();
  await screen.findByTestId("dv-file-src/a.ts");
  const tree = screen.getByTestId("edits-tree");
  expect(tree.closest(".files-sidebar")).toBeTruthy();
  const rows = [...tree.querySelectorAll(".files-tree-row")].map(
    (el) => el.textContent,
  );
  expect(rows).toEqual(["docs", "b.mdA", "src", "a.tsM"]);
  expect(
    within(tree)
      .getByTestId("edits-tree-file-docs/b.md")
      .querySelector(".edits-tree-status--added"),
  ).toBeTruthy();
  // The diffs read in the tree's order, and the file at their top is marked.
  const sections = [...document.querySelectorAll(".dv-file")].map((el) =>
    el.getAttribute("data-testid"),
  );
  expect(sections).toEqual(["dv-file-docs/b.md", "dv-file-src/a.ts"]);
  expect(within(tree).getByTestId("edits-tree-file-docs/b.md")).toHaveClass(
    "is-active",
  );
  expect(screen.getByTestId("edits-toggle-tree")).toHaveAttribute(
    "aria-pressed",
    "true",
  );
  // A folder folds its files away.
  fireEvent.click(within(tree).getByRole("treeitem", { name: /docs/ }));
  expect(within(tree).queryByTestId("edits-tree-file-docs/b.md")).toBeNull();
});

/** Lays an element out at `top` with `height`, as a browser would. */
function place(el: Element, top: number, height: number) {
  Object.defineProperty(el, "getBoundingClientRect", {
    configurable: true,
    value: () => ({
      top,
      bottom: top + height,
      left: 0,
      right: 800,
      width: 800,
      height,
      x: 0,
      y: top,
      toJSON() {},
    }),
  });
}

/** Lays a section out inside the diffs at `at()` from their content's top,
 *  moving with their scroll as it does in a browser. */
function placeIn(
  scroller: HTMLElement,
  el: Element,
  at: () => number,
  height = 200,
) {
  Object.defineProperty(el, "getBoundingClientRect", {
    configurable: true,
    value: () => {
      const top =
        scroller.getBoundingClientRect().top + at() - scroller.scrollTop;
      return {
        top,
        bottom: top + height,
        left: 0,
        right: 800,
        width: 800,
        height,
        x: 0,
        y: top,
        toJSON() {},
      };
    },
  });
}

// A pick scrolls the diffs alone, the file's section 10px under their top -
// the gap the filter keeps from the head - and nothing around them moves.
test("a file picked in the tree is scrolled to and marked", async () => {
  open();
  await screen.findByTestId("dv-file-src/a.ts");
  const scroller = screen.getByTestId("dv-scroll");
  place(scroller, 100, 500);
  placeIn(scroller, screen.getByTestId("dv-file-src/a.ts"), () => 800);
  const row = screen.getByTestId("edits-tree-file-src/a.ts");
  expect(row).not.toHaveClass("is-active");
  fireEvent.click(row);
  await waitFor(() => expect(scroller.scrollTop).toBe(800 - 10));
  expect(scrolled).toEqual([]);
  expect(row).toHaveClass("is-active");
  expect(row).toHaveAttribute("aria-selected", "true");
});

// The tree marks the file at the top of the diffs as the reader scrolls; a
// file picked stays marked while it is in sight, even where the diffs cannot
// bring it to their top (the last of a short set).
test("the mark follows the scroll, and a file picked keeps it while in sight", async () => {
  open();
  await screen.findByTestId("dv-file-src/a.ts");
  const scroller = screen.getByTestId("dv-scroll");
  place(scroller, 0, 500);
  const first = screen.getByTestId("dv-file-docs/b.md");
  const last = screen.getByTestId("dv-file-src/a.ts");
  place(first, 0, 300);
  place(last, 300, 60);
  const row = (path: string) => screen.getByTestId(`edits-tree-file-${path}`);

  fireEvent.click(row("src/a.ts"));
  fireEvent.scroll(scroller);
  expect(row("src/a.ts")).toHaveClass("is-active");

  // Out of sight, the picked file gives the mark back to the file at the top.
  place(first, -100, 300);
  place(last, 600, 60);
  fireEvent.scroll(scroller);
  await waitFor(() => expect(row("docs/b.md")).toHaveClass("is-active"));
  place(last, 10, 60);
  place(first, -300, 300);
  fireEvent.scroll(scroller);
  await waitFor(() => expect(row("src/a.ts")).toHaveClass("is-active"));
});

// A file picked while the diffs above it still load stays where the pick put
// it as they land, until the reader scrolls away.
test("a picked file stays put while the diffs above it load, until the reader scrolls", async () => {
  open();
  await screen.findByTestId("dv-file-src/a.ts");
  const scroller = screen.getByTestId("dv-scroll");
  place(scroller, 100, 500);
  let above = 800;
  placeIn(scroller, screen.getByTestId("dv-file-src/a.ts"), () => above);
  fireEvent.click(screen.getByTestId("edits-tree-file-src/a.ts"));
  await waitFor(() => expect(scroller.scrollTop).toBe(790));
  // The diffs above grow as their patches land (git reports the files again).
  let edits = 1;
  const report = () => {
    edits += 1;
    fetchMock.mockImplementation(async (input: unknown) =>
      jsonResponse(
        String(input).includes("/changes/file")
          ? { patch: PATCH }
          : {
              ...SESSION,
              files: [
                SESSION.files[0]!,
                { ...SESSION.files[1]!, additions: 4 + edits },
              ],
            },
      ),
    );
    emitChangesSettled("s1");
  };
  above = 1300;
  report();
  await waitFor(() => expect(scroller.scrollTop).toBe(1290));
  // The reader scrolls: the pin lets go, and what lands next moves nothing.
  scroller.scrollTop = 200;
  fireEvent.scroll(scroller);
  above = 1600;
  report();
  await new Promise((r) => setTimeout(r, 100));
  expect(scroller.scrollTop).toBe(200);
});

// With many files the tree scrolls too: the row the diffs' scroll marks is
// brought into the tree's view.
test("the row the scroll marks is kept in the tree's view", async () => {
  open();
  await screen.findByTestId("dv-file-src/a.ts");
  const tree = screen.getByTestId("edits-tree");
  place(tree, 0, 200);
  const scroller = screen.getByTestId("dv-scroll");
  place(scroller, 0, 500);
  place(screen.getByTestId("dv-file-docs/b.md"), -400, 300);
  place(screen.getByTestId("dv-file-src/a.ts"), 0, 300);
  place(screen.getByTestId("edits-tree-file-src/a.ts"), 400, 28);
  fireEvent.scroll(scroller);
  await waitFor(() =>
    expect(screen.getByTestId("edits-tree-file-src/a.ts")).toHaveClass(
      "is-active",
    ),
  );
  expect(tree.scrollTop).toBe(400 + 28 - 200 + 4);
});

test("the filter narrows the tree, and the tree switch puts it away", async () => {
  open();
  await screen.findByTestId("dv-file-src/a.ts");
  fireEvent.change(screen.getByTestId("edits-tree-filter"), {
    target: { value: "b.md" },
  });
  await waitFor(() =>
    expect(screen.queryByTestId("edits-tree-file-src/a.ts")).toBeNull(),
  );
  expect(screen.getByTestId("edits-tree-file-docs/b.md")).toBeTruthy();
  fireEvent.change(screen.getByTestId("edits-tree-filter"), {
    target: { value: "nothing" },
  });
  await screen.findByText(t("changes.viewer.noMatches"));
  fireEvent.click(screen.getByTestId("edits-toggle-tree"));
  expect(screen.queryByTestId("edits-tree")).toBeNull();
  expect(
    screen.getByTestId("edits-view").querySelector(".files-layout"),
  ).not.toHaveClass("has-tree");
});

// One Escape, one step: the menu goes first, the window with the next one.
test("Escape puts the menu away before the window", async () => {
  const onClose = vi.fn();
  open(onClose);
  await screen.findByTestId("dv-file-src/a.ts");
  openMenu();
  fireEvent.keyDown(document.body, { key: "Escape" });
  expect(screen.queryByRole("menu")).toBeNull();
  expect(onClose).not.toHaveBeenCalled();
  fireEvent.keyDown(document.body, { key: "Escape" });
  expect(onClose).toHaveBeenCalledTimes(1);
});

// A file's head: the fold chevron and the name on the left, then copy and
// discard, always in sight, and git's counts at the right end; the name folds
// the diff, so there is no second chevron on the right.
test("a file's head ends with its counts, after copy and discard, with one chevron", async () => {
  open();
  const section = await screen.findByTestId("dv-file-src/a.ts");
  const head = section.querySelector(".dv-file-head") as HTMLElement;
  const parts = [...head.children].map((el) => el.className.split(" ")[0]);
  expect(parts).toEqual(["dv-file-title", "dv-file-actions", "dv-file-stat"]);
  const actions = [...head.querySelectorAll(".dv-file-actions button")].map(
    (b) => b.getAttribute("data-testid"),
  );
  expect(actions).toEqual(["dv-copy-src/a.ts", "dv-discard-src/a.ts"]);
  expect(head.querySelectorAll(".coddy-chevron")).toHaveLength(1);
  // No status dot before the name: the tree says the status, the name is enough.
  expect(head.querySelector(".dv-file-badge")).toBeNull();
  expect(head.querySelector(".dv-file-stat")?.textContent).toBe("+1−1");
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
  expect(within(openMenu()).queryByTestId("edits-discard-all")).toBeNull();
});

test("discarding a file asks first, then puts it back through the server", async () => {
  open();
  await screen.findByTestId("dv-file-src/a.ts");
  fireEvent.click(screen.getByTestId("dv-discard-src/a.ts"));
  const dialog = await confirmDialog();
  expect(dialog.textContent || "").toContain("a.ts");
  fireEvent.click(
    within(dialog).getByRole("button", { name: t("changes.discardYes") }),
  );
  await waitFor(() => {
    const post = fetchMock.mock.calls.find((c) =>
      String(c[0]).endsWith("/changes/revert"),
    );
    expect(post).toBeTruthy();
    expect(JSON.parse(String((post![1] as RequestInit).body))).toEqual({
      paths: ["src/a.ts"],
    });
  });
});

test("discarding everything asks first, and a refusal touches nothing", async () => {
  open();
  await screen.findByTestId("dv-file-src/a.ts");
  fireEvent.click(within(openMenu()).getByTestId("edits-discard-all"));
  let dialog = await confirmDialog();
  fireEvent.click(
    within(dialog).getByRole("button", { name: t("common.cancel") }),
  );
  await waitFor(() =>
    expect(document.body.querySelector(".confirm-dialog")).toBeNull(),
  );
  expect(
    fetchMock.mock.calls.some((c) => String(c[0]).endsWith("/changes/revert")),
  ).toBe(false);

  fireEvent.click(within(openMenu()).getByTestId("edits-discard-all"));
  dialog = await confirmDialog();
  fireEvent.click(
    within(dialog).getByRole("button", { name: t("changes.discardYes") }),
  );
  await waitFor(() => {
    const post = fetchMock.mock.calls.find((c) =>
      String(c[0]).endsWith("/changes/revert"),
    );
    expect(JSON.parse(String((post![1] as RequestInit).body))).toEqual({
      all: true,
    });
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
  open(onClose);
  await screen.findByTestId("dv-file-src/a.ts");
  fireEvent.click(within(openMenu()).getByTestId("edits-discard-all"));
  await confirmDialog();
  fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" });
  await waitFor(() =>
    expect(document.body.querySelector(".confirm-dialog")).toBeNull(),
  );
  expect(onClose).not.toHaveBeenCalled();
  fireEvent.keyDown(document.body, { key: "Escape" });
  expect(onClose).toHaveBeenCalledTimes(1);
});

test("a first read that fails says why instead of an empty window", async () => {
  fetchMock.mockImplementation(
    async () =>
      ({
        ok: false,
        status: 503,
        json: async () => ({ error: { message: "server restarting" } }),
      }) as unknown as Response,
  );
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
    return jsonResponse({
      ...SESSION,
      files: [SESSION.files[0]!],
      totals: { files: 1, additions: 1, deletions: 1 },
    });
  });
  open();
  await waitFor(() => expect(document.body.textContent).toContain("new"));
  body = "rewritten";
  emitChangesSettled("s1");
  await waitFor(() => expect(document.body.textContent).toContain("rewritten"));
});

test("new files git could not list can still be discarded", async () => {
  fetchMock.mockImplementation(async () =>
    jsonResponse({
      sessionId: "s1",
      vcs: "git",
      files: [],
      totals: { files: 0, additions: 0, deletions: 0 },
      skipped: 3,
    }),
  );
  open();
  await screen.findByTestId("dv-skipped");
  expect(within(openMenu()).getByTestId("edits-discard-all")).toBeTruthy();
});
