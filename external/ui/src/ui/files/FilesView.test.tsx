import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { FilesView, forgetOpenFiles } from "./FilesView";
import { t } from "../i18n/i18n";
import type { FileEntry } from "./api";

/**
 * The files of a session open in a window of their own, the way the
 * documentation does: the workspace tree on the left, the files opened from it
 * as tabs on the right. The dock beside the chat keeps its background tasks
 * and its edits; it no longer carries a tab strip.
 */

function entry(path: string, kind: FileEntry["kind"] = "file"): FileEntry {
  return {
    name: path.split("/").pop() || path,
    path_rel: path,
    kind,
    size_bytes: 12,
    mod_time: "2026-10-05T12:00:00Z",
  };
}

const tree: Record<string, FileEntry[]> = {
  "": [
    entry("src", "directory"),
    entry("README.md"),
    entry("notes.txt"),
    entry("CLAUDE.md", "symlink"),
  ],
  src: [entry("src/main.go"), entry("src/lib", "directory")],
  "src/lib": [entry("src/lib/util.ts")],
  big: ["a", "b", "c", "d", "e"].map((n) => entry(`big/${n}.txt`)),
};
let mediaTokens = 0;
/** Overrides of the file type the node reports, by path. */
let types: Record<string, string> = {};
/** Status the node answers a text read of a path with, instead of the text. */
let textStatus: Record<string, number> = {};
/** Holds the tree reads of these folders until released. */
let holdTree: { dirs: Set<string>; release: () => void; waiting: (() => void)[] } = {
  dirs: new Set(),
  release() {
    for (const go of this.waiting.splice(0)) go();
  },
  waiting: [],
};

let contents: Record<string, string>;
let fetcher: ReturnType<typeof vi.fn>;

function query(url: string, name: string): string {
  return new URL(url, "http://x").searchParams.get(name) || "";
}

beforeEach(() => {
  forgetOpenFiles();
  mediaTokens = 0;
  types = {};
  textStatus = {};
  holdTree.dirs = new Set();
  holdTree.waiting = [];
  tree[""] = tree[""]!.filter((e) => e.path_rel !== "new.txt");
  tree.src = [entry("src/main.go"), entry("src/lib", "directory")];
  if (!tree[""]!.some((e) => e.path_rel === "big"))
    tree[""]!.push(entry("big", "directory"));
  if (!tree[""]!.some((e) => e.path_rel === "media"))
    tree[""]!.push(entry("media", "directory"));
  tree.media = [entry("media/tone.wav")];
  Element.prototype.scrollIntoView = vi.fn();
  contents = {
    "README.md": "# Readme\n\nWelcome.",
    "notes.txt": "first note\nsecond note\nthird note",
    "src/main.go": "package main\n\nfunc main() {}",
    "src/lib/util.ts": "export const util = 1;",
  };
  fetcher = vi.fn(async (input: unknown, init?: RequestInit) => {
    const url = String(input);
    if (url.includes("/workspace/tree")) {
      const dir = query(url, "path_rel");
      if (holdTree.dirs.has(dir))
        await new Promise<void>((go) => holdTree.waiting.push(go));
      const all = tree[dir];
      if (!all) return new Response("{}", { status: 404 });
      // The big folder pages two rows at a time unless a limit says otherwise.
      const limit = Number(query(url, "limit")) || (dir === "big" ? 2 : 200);
      const cursor = query(url, "cursor");
      const start = cursor ? all.findIndex((e) => e.name === cursor) + 1 : 0;
      const rows = all.slice(start, start + limit);
      const more = start + limit < all.length;
      return new Response(
        JSON.stringify({
          entries: rows,
          has_more: more,
          next_cursor: more ? rows[rows.length - 1]!.name : "",
        }),
      );
    }
    if (url.includes("/workspace/media-token")) {
      mediaTokens += 1;
      return new Response(JSON.stringify({ token: `cap${mediaTokens}` }));
    }
    if (url.includes("/workspace/raw") && init?.method === "HEAD") {
      const path = query(url, "path_rel");
      const etag = `"${path}:${contents[path]?.length ?? 0}"`;
      const asked = (init.headers as Record<string, string> | undefined)?.[
        "If-None-Match"
      ];
      if (asked === etag) return new Response(null, { status: 304 });
      return new Response(null, {
        headers: {
          ETag: etag,
          "Content-Type": types[path]
            ? types[path]!
            : path.endsWith(".wav")
            ? "audio/wav"
            : path.endsWith(".md")
              ? "text/markdown; charset=utf-8"
              : "text/plain; charset=utf-8",
          "Content-Length": String(contents[path]?.length ?? 0),
          "Last-Modified": "Mon, 05 Oct 2026 12:00:00 GMT",
        },
      });
    }
    if (url.includes("/workspace/text")) {
      const path = query(url, "path_rel");
      if (textStatus[path])
        return new Response(
          JSON.stringify({ error: { message: "file is not decodable text" } }),
          { status: textStatus[path] },
        );
      const etag = `"${path}:${contents[path]?.length ?? 0}"`;
      const asked = query(url, "etag");
      if (asked && asked !== etag)
        return new Response(
          JSON.stringify({ error: { message: "file changed; reload before reading another page" } }),
          { status: 409 },
        );
      const all = (contents[path] || "").split("\n");
      const offset = Number(query(url, "offset")) || 0;
      const lines = all.slice(offset, offset + 300);
      return new Response(
        JSON.stringify({
          path_rel: path,
          lines,
          offset,
          next_offset: offset + lines.length,
          has_more: offset + lines.length < all.length,
          etag: `"${path}:${contents[path]?.length ?? 0}"`,
        }),
      );
    }
    if (url.startsWith("/coddy/mentions")) {
      const q = query(url, "q");
      if (q === "outside") {
        // What the "@" index answers for paths that leave the workspace.
        return new Response(
          JSON.stringify({
            items: [
              { kind: "file", insert: "@../outside.txt", label: "../outside.txt" },
              { kind: "file", insert: "@/etc/outside", label: "/etc/outside" },
              { kind: "file", insert: "@~/outside", label: "~/outside" },
              { kind: "directory", insert: "@../up/", label: "../up/" },
              { kind: "file", insert: "@\\Windows\\win.ini", label: "\\Windows\\win.ini" },
              { kind: "file", insert: "@\\\\server\\share\\x", label: "\\\\server\\share\\x" },
              { kind: "file", insert: "@notes/outside.md", label: "notes/outside.md" },
            ],
          }),
        );
      }
      const items = Object.keys(contents)
        .filter((p) => p.includes(q))
        .map((p) => ({ kind: "file", insert: `@${p}`, label: p }));
      return new Response(
        JSON.stringify({
          items: [
            ...items,
            // Not a file of the workspace: the window shows none of these.
            { kind: "session", insert: "@session:x", label: "session:x" },
          ],
        }),
      );
    }
    return new Response("{}", { status: 404 });
  });
  vi.stubGlobal("fetch", fetcher);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

/** The text of one numbered line, once it is on screen: highlighting splits it into spans. */
async function line(no: number, text: string) {
  await waitFor(() =>
    expect(
      document.querySelector(`[data-file-line="${no}"] code`)?.textContent,
    ).toBe(text),
  );
}

function view(over: Partial<React.ComponentProps<typeof FilesView>> = {}) {
  return (
    <FilesView
      sessionId="s1"
      workspacePath="/work/demo"
      onClose={() => {}}
      {...over}
    />
  );
}

test("the files window shows the workspace tree beside an empty preview", async () => {
  render(view());
  const win = screen.getByRole("dialog", { name: t("files.title") });
  expect(win).toBe(screen.getByTestId("files-view"));
  // Titled the way the documentation window is, with the workspace under it.
  expect(within(win).getByText(t("files.title"))).toBeTruthy();
  expect(within(win).getByText("demo")).toBeTruthy();
  const treeEl = await screen.findByTestId("files-tree");
  await within(treeEl).findByText("README.md");
  expect(within(treeEl).getByText("src")).toBeTruthy();
  // A link is listed, marked, and not opened.
  const link = within(treeEl).getByText("CLAUDE.md").closest("button")!;
  expect(link).toBeDisabled();
  expect(link.querySelector(".files-tree-link")).toBeTruthy();
  expect(screen.getByTestId("files-empty").textContent).toContain(
    t("files.empty.title"),
  );
  expect(screen.queryByRole("tablist")).toBeNull();
});

test("a file picked in the tree opens in a tab, and a second one beside it", async () => {
  const onNavigate = vi.fn();
  render(view({ onNavigate }));
  fireEvent.click(await screen.findByText("notes.txt"));
  await screen.findByText("second note");
  const tabs = screen.getByRole("tablist", { name: t("files.openFiles") });
  expect(within(tabs).getAllByRole("tab").map((tab) => tab.textContent)).toEqual([
    "notes.txt",
  ]);
  expect(onNavigate).toHaveBeenLastCalledWith("notes.txt", 1);
  // Folders open in place.
  fireEvent.click(screen.getByText("src"));
  fireEvent.click(await screen.findByText("main.go"));
  await line(1, "package main");
  const names = within(tabs)
    .getAllByRole("tab")
    .map((tab) => [tab.textContent, tab.getAttribute("aria-selected")]);
  expect(names).toEqual([
    ["notes.txt", "false"],
    ["main.go", "true"],
  ]);
  expect(onNavigate).toHaveBeenLastCalledWith("src/main.go", 1);
  // Back to the first tab without reading the tree again.
  fireEvent.click(within(tabs).getByText("notes.txt"));
  await screen.findByText("second note");
  expect(onNavigate).toHaveBeenLastCalledWith("notes.txt", 1);
});

test("closing the tab on show shows the one beside it, and the last one the empty preview", async () => {
  render(view());
  fireEvent.click(await screen.findByText("notes.txt"));
  await screen.findByText("second note");
  fireEvent.click(screen.getByText("README.md"));
  await screen.findByText("Welcome.");
  fireEvent.click(
    screen.getByRole("button", {
      name: t("files.closeTab", { name: "README.md" }),
    }),
  );
  await screen.findByText("second note");
  fireEvent.click(
    screen.getByRole("button", {
      name: t("files.closeTab", { name: "notes.txt" }),
    }),
  );
  expect(await screen.findByTestId("files-empty")).toBeTruthy();
  expect(screen.queryByRole("tablist")).toBeNull();
});

test("the window opens on the file and the line the address names", async () => {
  render(view({ initialPath: "notes.txt", initialLine: 3 }));
  await screen.findByText("third note");
  // The window goes to the line the address names, without painting it.
  const row = document.querySelector('[data-file-line="3"]');
  expect(row).toBeTruthy();
  expect(row?.className || "").toBe("");
  // The scroll is a passive effect of the render that put the line in, which
  // a loaded runner can run after findByText has already returned.
  await waitFor(() => expect(Element.prototype.scrollIntoView).toHaveBeenCalled());
  expect(screen.getByRole("tab", { selected: true }).textContent).toBe(
    "notes.txt",
  );
});

test("the filter searches the whole workspace, not only the folders opened", async () => {
  render(view());
  await screen.findByText("README.md");
  const filter = screen.getByRole("searchbox", { name: t("files.search") });
  fireEvent.change(filter, { target: { value: "util" } });
  const hit = await screen.findByText("src/lib/util.ts");
  // The search is the session's own workspace index.
  const call = fetcher.mock.calls.find(([u]) =>
    String(u).startsWith("/coddy/mentions"),
  )!;
  expect(query(String(call[0]), "q")).toBe("util");
  expect((call[1] as RequestInit).headers).toMatchObject({
    "X-Coddy-Session-ID": "s1",
  });
  expect(screen.queryByText("session:x")).toBeNull();
  fireEvent.click(hit);
  await line(1, "export const util = 1;");
});

test("Escape in the filter clears it first, then closes the window", async () => {
  const onClose = vi.fn();
  render(view({ onClose }));
  await screen.findByText("README.md");
  const filter = screen.getByRole("searchbox", { name: t("files.search") });
  fireEvent.change(filter, { target: { value: "util" } });
  fireEvent.keyDown(filter, { key: "Escape" });
  expect((filter as HTMLInputElement).value).toBe("");
  expect(onClose).not.toHaveBeenCalled();
  fireEvent.keyDown(filter, { key: "Escape" });
  expect(onClose).toHaveBeenCalledTimes(1);
});

test("the tree folds away to give the preview the width, and the window grows to the whole screen", async () => {
  render(view());
  await screen.findByText("README.md");
  const toggle = screen.getByTestId("files-toggle-tree");
  expect(toggle.getAttribute("aria-pressed")).toBe("true");
  fireEvent.click(toggle);
  expect(toggle.getAttribute("aria-pressed")).toBe("false");
  expect(screen.queryByTestId("files-tree")).toBeNull();
  const expand = screen.getByTestId("files-expand");
  fireEvent.click(expand);
  expect(screen.getByTestId("files-view")).toHaveClass("is-expanded");
  fireEvent.click(expand);
  expect(screen.getByTestId("files-view")).not.toHaveClass("is-expanded");
});

test("the window opened again keeps the files that were open in it", async () => {
  const { unmount } = render(view());
  fireEvent.click(await screen.findByText("notes.txt"));
  await screen.findByText("second note");
  fireEvent.click(screen.getByText("README.md"));
  await screen.findByText("Welcome.");
  unmount();
  render(view());
  const tabs = await screen.findByRole("tablist");
  expect(within(tabs).getAllByRole("tab").map((tab) => tab.textContent)).toEqual([
    "notes.txt",
    "README.md",
  ]);
  // Another chat has its own.
  cleanup();
  render(view({ sessionId: "s2" }));
  expect(await screen.findByTestId("files-empty")).toBeTruthy();
});

test("hidden files are a choice of the window's menu", async () => {
  render(view());
  await screen.findByText("README.md");
  fireEvent.click(screen.getByTestId("files-more"));
  const item = screen.getByRole("menuitemcheckbox", { name: t("files.hidden") });
  expect(item.getAttribute("aria-checked")).toBe("false");
  fireEvent.click(item);
  await waitFor(() =>
    expect(
      fetcher.mock.calls.some(
        ([u]) =>
          String(u).includes("/workspace/tree") &&
          query(String(u), "include_hidden") === "1",
      ),
    ).toBe(true),
  );
});

// A phone has room for one column: the tree, or the file.
test("on a phone the window shows the file it was opened on, and a file picked in the tree puts the tree away", async () => {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: query.includes("max-width: 599px") || query.includes("max-width: 1199px"),
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }));
  render(view({ initialPath: "notes.txt", initialLine: 2 }));
  await screen.findByText("second note");
  expect(screen.queryByTestId("files-tree")).toBeNull();
  fireEvent.click(screen.getByTestId("files-toggle-tree"));
  fireEvent.click(await screen.findByText("README.md"));
  await screen.findByText("Welcome.");
  expect(screen.queryByTestId("files-tree")).toBeNull();
});

test("an open file revalidates on focus and reports a concurrent rewrite", async () => {
  render(view({ initialPath: "notes.txt" }));
  await screen.findByText("first note");
  contents["notes.txt"] = "rewritten note";
  await act(async () => {
    fireEvent.focus(window);
  });
  await screen.findByText("rewritten note");
  expect(screen.getByRole("status")).toHaveTextContent(t("files.changed"));
});

// The tab names the file; the preview is its text and nothing over it: no
// second name, no size or time, no switch between a rendering and the source.
test("a file opens straight on its source, with no head over it", async () => {
  contents["README.md"] = "# Demo\n\nSome *text* here.";
  render(view({ initialPath: "README.md" }));
  await line(1, "# Demo");
  await line(3, "Some *text* here.");
  const preview = screen.getByTestId("files-file");
  expect(preview.querySelector(".files-file-head")).toBeNull();
  expect(within(preview).queryByRole("heading")).toBeNull();
  expect(within(preview).queryByRole("spinbutton")).toBeNull();
  expect(within(preview).queryAllByRole("button")).toEqual([]);
  expect(preview.textContent).not.toMatch(/bytes|байт/);
  expect(preview.querySelector("time")).toBeNull();
});

// Markdown is text like any other: a picture or a script in it is a line to
// read, never something the window loads or runs.
test("workspace Markdown is shown as text: its pictures are not loaded and its HTML never runs", async () => {
  contents["docs/readme.md"] =
    "![remote](https://example.test/track.png)\n\n<script>window.stolen=true</script>";
  render(view({ initialPath: "docs/readme.md" }));
  await line(1, "![remote](https://example.test/track.png)");
  await line(3, "<script>window.stolen=true</script>");
  expect(document.querySelector("script")).toBeNull();
  expect(document.querySelector("img")).toBeNull();
  expect(
    fetcher.mock.calls.some(([url]) => String(url).includes("example.test")),
  ).toBe(false);
});

// Coming back to the page checks the open file; a file that did not change is
// not read again, so a sound keeps playing from the address it has.
test("a file that did not change is not read again when the page comes back, and a sound keeps its address", async () => {
  contents["media/tone.wav"] = "RIFF....WAVE";
  render(view({ initialPath: "media/tone.wav" }));
  await waitFor(() =>
    expect(document.querySelector("audio")?.getAttribute("src")).toContain(
      "access_token=cap1",
    ),
  );
  await act(async () => {
    fireEvent.focus(window);
    await new Promise((r) => setTimeout(r, 30));
  });
  expect(document.querySelector("audio")?.getAttribute("src")).toContain(
    "access_token=cap1",
  );
  expect(mediaTokens).toBe(1);

  fireEvent.click(await screen.findByText("notes.txt"));
  await screen.findByText("second note");
  const textReads = () =>
    fetcher.mock.calls.filter(([u]) => String(u).includes("/workspace/text"))
      .length;
  const before = textReads();
  await act(async () => {
    fireEvent.focus(window);
    await new Promise((r) => setTimeout(r, 30));
  });
  expect(screen.getByText("second note")).toBeTruthy();
  expect(textReads()).toBe(before);
});

test("the filter keeps to the workspace: paths that leave it are not offered", async () => {
  render(view());
  await screen.findByText("README.md");
  fireEvent.change(screen.getByRole("searchbox", { name: t("files.search") }), {
    target: { value: "outside" },
  });
  await screen.findByText("notes/outside.md");
  for (const away of [
    "../outside.txt",
    "/etc/outside",
    "~/outside",
    "../up",
    "\\Windows\\win.ini",
    "\\\\server\\share\\x",
  ])
    expect(screen.queryByText(away)).toBeNull();
});

test("a folder that is gone does not take the rest of the tree with it", async () => {
  render(view());
  fireEvent.click(await screen.findByText("src"));
  await screen.findByText("main.go");
  delete tree.src;
  tree[""]!.push(entry("new.txt"));
  await act(async () => {
    fireEvent.focus(window);
  });
  await screen.findByText("new.txt");
  expect(screen.queryByRole("alert")).toBeNull();
});

test("the rows a folder loaded with Load more stay through a refresh", async () => {
  render(view());
  fireEvent.click(await screen.findByText("big"));
  await screen.findByText("b.txt");
  fireEvent.click(screen.getByText(t("files.moreEntries")));
  await screen.findByText("d.txt");
  // Another folder opens, and the page comes back: neither reads big again
  // down to its first page.
  fireEvent.click(screen.getByText("src"));
  await screen.findByText("main.go");
  await act(async () => {
    fireEvent.focus(window);
    await new Promise((r) => setTimeout(r, 30));
  });
  await screen.findByText("d.txt");
  expect(screen.getByText("c.txt")).toBeTruthy();
});

test("the window opened again shows its file at the line it was on, and says so to the address", async () => {
  const { unmount } = render(view({ initialPath: "notes.txt", initialLine: 3 }));
  await screen.findByText("third note");
  unmount();
  const onNavigate = vi.fn();
  render(view({ onNavigate }));
  await screen.findByText("third note");
  // The window goes to the line; for now a file is only read, so nothing marks it.
  const line = document.querySelector('[data-file-line="3"]');
  await waitFor(() => expect(Element.prototype.scrollIntoView).toHaveBeenCalled());
  expect(line?.className || "").toBe("");
  expect(onNavigate).toHaveBeenCalledWith("notes.txt", 3);
});

test("a file rewritten as another kind under the same path does not keep showing the old text", async () => {
  render(view({ initialPath: "notes.txt" }));
  await screen.findByText("second note");
  types["notes.txt"] = "application/pdf";
  contents["notes.txt"] = "%PDF-1.7 not text any more";
  await act(async () => {
    fireEvent.focus(window);
  });
  await screen.findByText(t("files.pdfDownload"));
  expect(screen.queryByText("second note")).toBeNull();
});

test("a folder opened while the open ones are read again keeps its rows", async () => {
  render(view());
  fireEvent.click(await screen.findByText("src"));
  await screen.findByText("main.go");
  holdTree.dirs = new Set(["", "src"]);
  await act(async () => {
    fireEvent.focus(window);
  });
  fireEvent.click(screen.getByText("big"));
  await waitFor(() => expect(holdTree.waiting.length).toBeGreaterThan(0));
  holdTree.dirs = new Set();
  // big is not held: it loads while the refresh still waits.
  await screen.findByText("b.txt");
  await act(async () => {
    holdTree.release();
    await new Promise((r) => setTimeout(r, 20));
  });
  expect(screen.getByText("b.txt")).toBeTruthy();
});

test("a new filter query never shows the hits of the one before", async () => {
  render(view());
  await screen.findByText("README.md");
  const filter = screen.getByRole("searchbox", { name: t("files.search") });
  fireEvent.change(filter, { target: { value: "util" } });
  await screen.findByText("src/lib/util.ts");
  fireEvent.change(filter, { target: { value: "readme" } });
  expect(screen.queryByText("src/lib/util.ts")).toBeNull();
});

/** Puts the file's scroll box at `top` of `height`, as a browser would lay it out. */
function scrollBody(top: number, height = 6000, view = 600) {
  const body = document.querySelector(".files-file-body") as HTMLElement;
  Object.defineProperty(body, "scrollHeight", { configurable: true, value: height });
  Object.defineProperty(body, "clientHeight", { configurable: true, value: view });
  body.scrollTop = top;
  fireEvent.scroll(body);
}

// A long file scrolls through: the next lines are read as the reader nears
// the end of those on screen, so there are no pages to click through.
test("a long file reads on as it is scrolled, with no pages to click", async () => {
  contents["long.txt"] = Array.from({ length: 700 }, (_, i) => `line ${i + 1}`).join("\n");
  render(view({ initialPath: "long.txt" }));
  await screen.findByText("line 1");
  expect(screen.queryByText("line 301")).toBeNull();
  expect(document.querySelector(".files-pages")).toBeNull();
  scrollBody(5300);
  await screen.findByText("line 301");
  expect(screen.getByText("line 1")).toBeTruthy();
  scrollBody(11000, 11600);
  await screen.findByText("line 700");
  expect(document.querySelectorAll("[data-file-line]")).toHaveLength(700);
});

// A view still at the end once the lines went in reads on by itself: no
// second scroll event comes when the reader already stands at the bottom.
// Two reads and two renders of a growing list stand between the scroll and
// line 700, which outlasts the default second of findByText on a loaded
// runner (the full suite beside other work); with time it always arrives.
test("a view still at the end after a read reads on without another scroll", async () => {
  contents["long.txt"] = Array.from({ length: 700 }, (_, i) => `line ${i + 1}`).join("\n");
  render(view({ initialPath: "long.txt" }));
  await screen.findByText("line 1");
  scrollBody(5500);
  await screen.findByText("line 700", {}, { timeout: 10000 });
}, 20000);

// Opened in the middle (an address, a link), the file reads back up as the
// reader scrolls to the top of what is on screen.
test("a file opened in the middle reads back up as it is scrolled", async () => {
  contents["long.txt"] = Array.from({ length: 700 }, (_, i) => `line ${i + 1}`).join("\n");
  render(view({ initialPath: "long.txt", initialLine: 450 }));
  await screen.findByText("line 450");
  expect(screen.queryByText("line 1")).toBeNull();
  scrollBody(0);
  await screen.findByText("line 1");
  expect(screen.getByText("line 450")).toBeTruthy();
});

// A file rewritten while the reader scrolls on is not spliced from two
// versions: it says so and starts over at its top.
test("a file rewritten while it is read on starts over and says so", async () => {
  contents["long.txt"] = Array.from({ length: 700 }, (_, i) => `line ${i + 1}`).join("\n");
  render(view({ initialPath: "long.txt" }));
  await screen.findByText("line 1");
  contents["long.txt"] = Array.from({ length: 700 }, (_, i) => `new ${i + 1}`).join("\n");
  scrollBody(5300);
  await screen.findByRole("status");
  await screen.findByText("new 1");
  expect(screen.queryByText("line 1")).toBeNull();
});

/** Lays the tab strip out as a browser would: `content` wide inside `width`. */
function layOutTabs(content: number, width = 300) {
  const strip = document.querySelector(".files-tabs") as HTMLElement;
  Object.defineProperty(strip, "scrollWidth", { configurable: true, value: content });
  Object.defineProperty(strip, "clientWidth", { configurable: true, value: width });
  fireEvent.scroll(strip);
  return strip;
}

// Tabs that do not fit scroll sideways with no arrows: the wheel turned over
// the strip moves it, a finger swipes it, and an end that has more fades out.
test("tabs that do not fit scroll sideways by the wheel, with no arrows", async () => {
  render(view({ initialPath: "notes.txt" }));
  await screen.findByText("first note");
  const strip = layOutTabs(900);
  expect(document.querySelector(".files-tabs-bar button:not([role=tab]):not(.files-tab-close)")).toBeNull();
  expect(strip).toHaveClass("has-more-right");
  expect(strip).not.toHaveClass("has-more-left");
  fireEvent.wheel(strip, { deltaY: 120 });
  expect(strip.scrollLeft).toBe(120);
  await waitFor(() => expect(strip).toHaveClass("has-more-left"));
  strip.scrollLeft = 600;
  fireEvent.scroll(strip);
  await waitFor(() => expect(strip).not.toHaveClass("has-more-right"));
});

// The tab on show is brought into the strip's view when it is picked or opened.
test("the tab on show is scrolled into the strip", async () => {
  render(view({ initialPath: "notes.txt" }));
  await screen.findByText("first note");
  fireEvent.click(screen.getByText("README.md"));
  await screen.findByText("Welcome.");
  const strip = layOutTabs(900);
  // notes.txt lies past the strip's right edge; showing it brings it in.
  const notes = screen.getByRole("tab", { name: "notes.txt" }).parentElement as HTMLElement;
  Object.defineProperty(notes, "offsetLeft", { configurable: true, value: 760 });
  Object.defineProperty(notes, "offsetWidth", { configurable: true, value: 120 });
  fireEvent.click(screen.getByRole("tab", { name: "notes.txt" }));
  await waitFor(() => expect(strip.scrollLeft).toBeGreaterThanOrEqual(760 + 120 - 300));
});

test("Reload puts away the notice that the file changed", async () => {
  render(view({ initialPath: "notes.txt" }));
  await screen.findByText("first note");
  contents["notes.txt"] = "rewritten note";
  await act(async () => {
    fireEvent.focus(window);
  });
  await screen.findByRole("status");
  fireEvent.click(screen.getByTestId("files-more"));
  fireEvent.click(screen.getByRole("menuitem", { name: t("files.reload") }));
  await waitFor(() => expect(screen.queryByRole("status")).toBeNull());
});

test("the binary notice is for a file that is not text, not for any failed read", async () => {
  textStatus["blob.bin"] = 415;
  contents["blob.bin"] = "\u0000\u0001";
  render(view({ initialPath: "blob.bin" }));
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain(t("files.binary"));
  cleanup();
  textStatus["notes.txt"] = 500;
  render(view({ initialPath: "notes.txt" }));
  const failed = await screen.findByRole("alert");
  expect(failed.textContent).not.toContain(t("files.binary"));
});

test("the open files survive the window learning its workspace folder", async () => {
  const { unmount } = render(view({ workspacePath: "" }));
  fireEvent.click(await screen.findByText("notes.txt"));
  await screen.findByText("second note");
  unmount();
  render(view({ workspacePath: "/work/demo" }));
  const tabs = await screen.findByRole("tablist");
  expect(within(tabs).getAllByRole("tab").map((tab) => tab.textContent)).toEqual([
    "notes.txt",
  ]);
});
