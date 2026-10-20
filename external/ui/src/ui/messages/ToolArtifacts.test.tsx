import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";

import { pageWithoutClipboardApi, selectedOnce } from "../clipboardTestKit";
import { setEnv } from "../env/remoteEnv";
import { parseToolArtifacts } from "../chat/toolArtifacts";
import { onOpenWorkspaceFile, type OpenFileRequest } from "../files/fileBus";
import { WorkspaceFilesContext } from "../files/workspaceEntries";
import { ToolCallMessage } from "./ToolCallMessage";
import {
  ArtifactCard,
  placeArtifactMenu,
  placeArtifactSubmenu,
} from "./ToolArtifactCards";
import { shellStackMaxWidthMediaQuery } from "../shellBreakpoint";

const artifact = {
  id: "artifact-1",
  name: "release-notes.pdf",
  sha256: "a".repeat(64),
  size: 2048,
  url: "/coddy/sessions/s1/artifacts/artifact-1",
};
const unavailableArtifact = (({ url: _url, ...rest }) => rest)(artifact);

// The card reads the width and the input device (a phone folds its menu), and
// restoreAllMocks below would take the setup's matchMedia stub away with it.
function stubMatchMedia(matches: (query: string) => boolean) {
  vi.stubGlobal(
    "matchMedia",
    (query: string) =>
      ({
        matches: matches(query),
        media: query,
        onchange: null,
        addListener() {},
        removeListener() {},
        addEventListener() {},
        removeEventListener() {},
        dispatchEvent: () => false,
      }) as unknown as MediaQueryList,
  );
}

beforeEach(() => stubMatchMedia(() => false));

afterEach(() => {
  cleanup();
  setEnv({ mode: "local" });
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

test("normalizes artifact metadata and retains a missing download URL as unavailable", () => {
  expect(
    parseToolArtifacts([artifact, { ...artifact, id: "gone", url: "" }]),
  ).toEqual([artifact, { ...artifact, id: "gone", url: undefined }]);
  expect(parseToolArtifacts([{ ...artifact, sha256: "not-a-digest" }])).toEqual(
    [],
  );
  // Where the file it was copied from is now; a state this page does not know
  // is left out, as an older server that sends none.
  expect(
    parseToolArtifacts([
      { ...artifact, sourceState: "deleted" },
      { ...artifact, id: "odd", sourceState: "moved" },
    ]),
  ).toEqual([
    { ...artifact, sourceState: "deleted" },
    { ...artifact, id: "odd" },
  ]);
});

test("renders completed share_file artifacts below the closed disclosure", () => {
  render(
    <ToolCallMessage
      toolCallId="share-1"
      title="share_file"
      status="completed"
      artifacts={[artifact]}
    />,
  );

  expect(screen.getByTestId("tool-artifact-card-artifact-1")).toBeVisible();
  expect(screen.getByText("release-notes.pdf")).toBeVisible();
  expect(screen.getByText("PDF · 2.0 KB")).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "Download release-notes.pdf" }),
  ).toBeNull();
  expect(
    screen.getByRole("button", { name: "Actions for release-notes.pdf" }),
  ).toBeVisible();
  expect(screen.getByTestId("tool-details-share-1")).not.toHaveAttribute(
    "open",
  );
});

test("does not render artifacts for a pending or unrelated tool call", () => {
  const { rerender } = render(
    <ToolCallMessage
      toolCallId="share-2"
      title="share_file"
      status="in_progress"
      artifacts={[artifact]}
    />,
  );
  expect(screen.queryByTestId("tool-artifact-card-artifact-1")).toBeNull();

  rerender(
    <ToolCallMessage
      toolCallId="other-1"
      title="write"
      status="completed"
      artifacts={[artifact]}
    />,
  );
  expect(screen.queryByTestId("tool-artifact-card-artifact-1")).toBeNull();
});

test("marks an artifact without a URL as unavailable", () => {
  render(
    <ToolCallMessage
      toolCallId="share-3"
      title="share_file"
      status="completed"
      artifacts={[unavailableArtifact]}
    />,
  );

  expect(screen.getByText("Download unavailable")).toBeVisible();
  fireEvent.click(
    screen.getByRole("button", { name: "Actions for release-notes.pdf" }),
  );
  expect(screen.getByRole("menuitem", { name: "Download" })).toBeDisabled();
});

// A picture whose source is not in the chat's workspace (none is recorded
// here) has only its copy to show: the thumbnail opens it in the lightbox.
test("opens an inline image artifact in the shared lightbox", () => {
  const { container } = render(
    <ArtifactCard
      inline
      artifact={{
        ...artifact,
        name: "release-overview.png",
        previewUrl: "/coddy/sessions/s1/artifacts/artifact-1/preview",
      }}
    />,
  );

  fireEvent.click(
    screen.getByRole("button", { name: "Open release-overview.png" }),
  );
  expect(screen.getByRole("dialog")).toHaveTextContent("release-overview.png");
  expect(
    screen.getByRole("img", { name: "release-overview.png" }),
  ).toHaveAttribute("src", "/coddy/sessions/s1/artifacts/artifact-1/preview");
  expect(
    container.querySelector(".inline-artifact-extension")?.parentElement,
  ).toHaveClass("tool-artifact-card");
});

test("opens the actions menu outside the card, which clips what overflows it", () => {
  render(<ArtifactCard inline artifact={artifact} />);
  const card = screen.getByTestId("inline-artifact-card-artifact-1");
  const trigger = screen.getByRole("button", {
    name: "Actions for release-notes.pdf",
  });

  fireEvent.click(trigger);
  const menu = screen.getByRole("menu");
  expect(card).not.toContainElement(menu);
  expect(menu.parentElement).toBe(document.body);
  expect(trigger).toHaveAttribute("aria-expanded", "true");
  expect(trigger).toHaveAttribute("aria-haspopup", "menu");
  // Mention, Copy and Download, in that order; the ways to copy are under
  // Copy, and the card itself opens the file.
  expect(
    screen.getAllByRole("menuitem").map((item) => item.textContent),
  ).toEqual(["Mention in chat", "Copy", "Download"]);
  // The first item that can be picked takes the focus, so the keyboard starts
  // inside the menu (Mention is disabled: this file names no source).
  expect(
    screen.getByRole("menuitem", { name: "Mention in chat" }),
  ).toBeDisabled();
  expect(screen.getByRole("menuitem", { name: "Copy" })).toHaveFocus();
  expect(screen.getByRole("menuitem", { name: "Copy" })).toHaveAttribute(
    "aria-haspopup",
    "menu",
  );
});

test("the copy items of the menu work without a clipboard API", () => {
  const page = pageWithoutClipboardApi();
  render(
    <ArtifactCard
      inline
      artifact={{ ...artifact, sourcePath: "/work/release-notes.pdf" }}
    />,
  );
  const trigger = screen.getByRole("button", {
    name: "Actions for release-notes.pdf",
  });
  for (const name of ["Copy name", "Copy absolute path"]) {
    fireEvent.click(trigger);
    fireEvent.click(screen.getByRole("menuitem", { name }));
  }
  expect(page.copies).toEqual([
    ...selectedOnce("release-notes.pdf"),
    ...selectedOnce("/work/release-notes.pdf"),
  ]);
});

test("draws the trigger's dots as an svg, not a text glyph", () => {
  render(<ArtifactCard inline artifact={artifact} />);
  const trigger = screen.getByRole("button", {
    name: "Actions for release-notes.pdf",
  });
  expect(trigger.querySelector("svg")).not.toBeNull();
  expect(trigger.textContent).toBe("");
});

test("the trigger closes the menu it opened", () => {
  render(<ArtifactCard inline artifact={artifact} />);
  const trigger = screen.getByRole("button", {
    name: "Actions for release-notes.pdf",
  });

  fireEvent.click(trigger);
  expect(screen.getByRole("menu")).toBeVisible();
  // A real click starts with a press on the trigger, which is outside the menu.
  fireEvent.mouseDown(trigger);
  fireEvent.click(trigger);
  expect(screen.queryByRole("menu")).toBeNull();
  expect(trigger).toHaveAttribute("aria-expanded", "false");
});

test("a press outside the card and the menu closes the menu", () => {
  render(<ArtifactCard inline artifact={artifact} />);
  fireEvent.click(
    screen.getByRole("button", { name: "Actions for release-notes.pdf" }),
  );
  fireEvent.mouseDown(document.body);
  expect(screen.queryByRole("menu")).toBeNull();
});

test("Escape closes the menu wherever the focus is and hands it back to the trigger", () => {
  render(<ArtifactCard inline artifact={artifact} />);
  const trigger = screen.getByRole("button", {
    name: "Actions for release-notes.pdf",
  });
  fireEvent.click(trigger);
  trigger.focus();

  fireEvent.keyDown(document.activeElement!, { key: "Escape" });
  expect(screen.queryByRole("menu")).toBeNull();
  expect(trigger).toHaveFocus();
});

test("the arrow keys move between the items that can be picked", () => {
  render(
    <ArtifactCard
      inline
      artifact={{
        ...unavailableArtifact,
        sourcePath: "/work/release-notes.pdf",
      }}
    />,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Actions for release-notes.pdf" }),
  );
  const menu = screen.getByRole("menu");
  expect(
    screen.getByRole("menuitem", { name: "Mention in chat" }),
  ).toHaveFocus();

  fireEvent.keyDown(menu, { key: "ArrowDown" });
  expect(screen.getByRole("menuitem", { name: "Copy" })).toHaveFocus();
  // Download is disabled without a URL, so down from Copy wraps to Mention.
  fireEvent.keyDown(menu, { key: "ArrowDown" });
  expect(screen.getByRole("menuitem", { name: "Download" })).toBeDisabled();
  expect(
    screen.getByRole("menuitem", { name: "Mention in chat" }),
  ).toHaveFocus();
  fireEvent.keyDown(menu, { key: "End" });
  expect(screen.getByRole("menuitem", { name: "Copy" })).toHaveFocus();
  fireEvent.keyDown(menu, { key: "Home" });
  expect(
    screen.getByRole("menuitem", { name: "Mention in chat" }),
  ).toHaveFocus();
});

test("the menu follows its trigger while the page scrolls and closes once the trigger has left the window", () => {
  render(<ArtifactCard inline artifact={artifact} />);
  const trigger = screen.getByRole("button", {
    name: "Actions for release-notes.pdf",
  });
  const at = (top: number) =>
    vi
      .spyOn(trigger, "getBoundingClientRect")
      .mockReturnValue(
        DOMRect.fromRect({ x: 272, y: top, width: 28, height: 28 }),
      );

  at(100);
  fireEvent.click(trigger);
  expect(screen.getByRole("menu")).toHaveStyle({ top: "134px" });

  at(400);
  fireEvent.scroll(window);
  expect(screen.getByRole("menu")).toHaveStyle({ top: "434px" });

  at(-60);
  fireEvent.scroll(window);
  expect(screen.queryByRole("menu")).toBeNull();
});

// The transcript scrolls under the chat's sticky title and the docked composer
// inside the window, so a trigger can be hidden without leaving it; the menu
// used to stay open over the composer, following a trigger nobody could see.
test("closes the menu once something else covers its trigger", () => {
  render(<ArtifactCard inline artifact={artifact} />);
  const trigger = screen.getByRole("button", {
    name: "Actions for release-notes.pdf",
  });
  vi.spyOn(trigger, "getBoundingClientRect").mockReturnValue(
    DOMRect.fromRect({ x: 272, y: 300, width: 28, height: 28 }),
  );
  const composer = document.createElement("div");
  document.body.append(composer);
  let onTop: Element = trigger;
  const hit = vi.fn(() => onTop);
  Object.defineProperty(document, "elementFromPoint", {
    value: hit,
    configurable: true,
  });
  try {
    fireEvent.click(trigger);
    // The menu itself over the trigger (a window too short to keep them
    // apart) still counts as the trigger being there.
    onTop = screen.getByRole("menuitem", { name: "Copy" });
    fireEvent.scroll(window);
    expect(screen.getByRole("menu")).toBeVisible();

    onTop = composer;
    fireEvent.scroll(window);
    expect(screen.queryByRole("menu")).toBeNull();
    expect(hit).toHaveBeenCalledWith(286, 314);
  } finally {
    delete (document as { elementFromPoint?: unknown }).elementFromPoint;
    composer.remove();
  }
});

// The focused item goes with the menu; without a hand-back the focus fell to
// the body and the next Tab started over from the top of the page.
test("a menu closed by its trigger scrolling out of sight hands the focus back to the trigger", () => {
  render(<ArtifactCard inline artifact={artifact} />);
  const trigger = screen.getByRole("button", {
    name: "Actions for release-notes.pdf",
  });
  const rect = vi
    .spyOn(trigger, "getBoundingClientRect")
    .mockReturnValue(
      DOMRect.fromRect({ x: 272, y: 300, width: 28, height: 28 }),
    );
  fireEvent.click(trigger);
  expect(screen.getByRole("menuitem", { name: "Copy" })).toHaveFocus();

  rect.mockReturnValue(
    DOMRect.fromRect({ x: 272, y: -80, width: 28, height: 28 }),
  );
  fireEvent.scroll(window);
  expect(screen.queryByRole("menu")).toBeNull();
  expect(trigger).toHaveFocus();
});

const present = {
  ...artifact,
  sourcePath: "/work/repo/reports/release-notes.pdf",
  relativePath: "reports/release-notes.pdf",
  sourceState: "present" as const,
};

function listenForOpens() {
  const opened: OpenFileRequest[] = [];
  const stop = onOpenWorkspaceFile((request) => opened.push(request));
  return { opened, stop };
}

// The card opens the file it was copied from in the Files window; "Reveal on
// server" asked the server's desktop instead, and a headless server could
// only answer that it failed.
test("a click on a shared file's card opens its source in the Files window", () => {
  const { opened, stop } = listenForOpens();
  try {
    render(<ArtifactCard inline artifact={present} />);
    fireEvent.click(
      screen.getByRole("button", {
        name: "Open release-notes.pdf in the Files window",
      }),
    );
    expect(opened).toEqual([
      { path: "reports/release-notes.pdf", line: undefined },
    ]);

    // The menu has no Open item and no reveal on the server: the card opens.
    fireEvent.click(
      screen.getByRole("button", { name: "Actions for release-notes.pdf" }),
    );
    expect(screen.queryByRole("menuitem", { name: /open|reveal/i })).toBeNull();
    expect(
      screen.getByRole("menuitem", { name: "Mention in chat" }),
    ).toHaveFocus();
  } finally {
    stop();
  }
});

// A click on the file opens it in the Files window, a picture included: the
// window draws pictures too, and only a picture whose source is gone opens its
// copy in the lightbox.
test("a picture's thumbnail opens its source in the Files window", () => {
  const { opened, stop } = listenForOpens();
  try {
    render(
      <ArtifactCard
        inline
        artifact={{
          ...present,
          name: "chart.png",
          relativePath: "reports/chart.png",
          previewUrl: "/coddy/sessions/s1/artifacts/artifact-1/preview",
        }}
      />,
    );
    fireEvent.click(
      screen.getByRole("button", {
        name: "Open chart.png in the Files window",
      }),
    );
    expect(opened).toEqual([{ path: "reports/chart.png", line: undefined }]);
    expect(screen.queryByRole("dialog")).toBeNull();
  } finally {
    stop();
  }
});

test("a shared file whose source was deleted says so and keeps its copy downloadable", () => {
  const { opened, stop } = listenForOpens();
  try {
    render(
      <ArtifactCard inline artifact={{ ...present, sourceState: "deleted" }} />,
    );
    const card = screen.getByTestId("inline-artifact-card-artifact-1");
    expect(card).toHaveClass("tool-artifact-card--deleted");
    expect(screen.getByText("File deleted")).toBeVisible();
    expect(
      screen.queryByRole("button", {
        name: "Open release-notes.pdf in the Files window",
      }),
    ).toBeNull();

    expect(card).toHaveAttribute(
      "title",
      "The file was deleted from the workspace. Its shared copy can still be downloaded.",
    );

    fireEvent.click(
      screen.getByRole("button", { name: "Actions for release-notes.pdf" }),
    );
    // Nothing to mention in the chat; its paths can still be copied.
    const mention = screen.getByRole("menuitem", { name: "Mention in chat" });
    expect(mention).toBeDisabled();
    expect(mention).toHaveAttribute(
      "title",
      "The file was deleted from the workspace. Its shared copy can still be downloaded.",
    );
    expect(screen.getByRole("menuitem", { name: "Copy" })).toBeEnabled();
    expect(screen.getByRole("menuitem", { name: "Download" })).toBeEnabled();
    expect(opened).toEqual([]);
  } finally {
    stop();
  }
});

test("a shared file that is not in the chat's workspace cannot be opened there", () => {
  render(
    <ArtifactCard
      inline
      artifact={{ ...present, sourceState: "unavailable" }}
    />,
  );
  expect(screen.getByText("2.0 KB")).toBeVisible();
  expect(
    screen.queryByRole("button", {
      name: "Open release-notes.pdf in the Files window",
    }),
  ).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Actions for release-notes.pdf" }),
  );
  const mention = screen.getByRole("menuitem", { name: "Mention in chat" });
  expect(mention).toBeDisabled();
  expect(mention).toHaveAttribute(
    "title",
    "The file is not in this chat's workspace",
  );
});

// The listing is read at the end of a turn; a file deleted since then is found
// out when the card is about to open it, and the card says so then.
test("a card whose file was deleted since the listing finds it out on the click", async () => {
  const { opened, stop } = listenForOpens();
  const fetchMock = vi.fn(async () =>
    Response.json({ object: "coddy.workspace_stat", entries: [] }),
  );
  vi.stubGlobal("fetch", fetchMock);
  try {
    render(
      <WorkspaceFilesContext.Provider
        value={{ sessionId: "s1", workspacePath: "/work/repo" }}
      >
        <ArtifactCard inline artifact={present} />
      </WorkspaceFilesContext.Provider>,
    );
    fireEvent.click(
      screen.getByRole("button", {
        name: "Open release-notes.pdf in the Files window",
      }),
    );
    expect(await screen.findByText("File deleted")).toBeVisible();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(opened).toEqual([]);
  } finally {
    stop();
  }
});

test("a card checks its file before it opens it and opens it when it is there", async () => {
  const { opened, stop } = listenForOpens();
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      Response.json({
        object: "coddy.workspace_stat",
        entries: [{ path_rel: "reports/release-notes.pdf", kind: "file" }],
      }),
    ),
  );
  try {
    render(
      <WorkspaceFilesContext.Provider
        value={{ sessionId: "s1", workspacePath: "/work/repo" }}
      >
        <ArtifactCard inline artifact={present} />
      </WorkspaceFilesContext.Provider>,
    );
    fireEvent.click(
      screen.getByRole("button", {
        name: "Open release-notes.pdf in the Files window",
      }),
    );
    await waitFor(() =>
      expect(opened).toEqual([
        { path: "reports/release-notes.pdf", line: undefined },
      ]),
    );
    expect(screen.queryByText("File deleted")).toBeNull();
  } finally {
    stop();
  }
});

// The check before the open takes a round trip. A chat left meanwhile is not
// where the file opens: the Files window would read the path against the
// chat on screen by then.
test("a card whose chat was left before its check answered opens nothing", async () => {
  const { opened, stop } = listenForOpens();
  let answer: (response: Response) => void = () => {};
  vi.stubGlobal(
    "fetch",
    vi.fn(
      () =>
        new Promise<Response>((resolve) => {
          answer = resolve;
        }),
    ),
  );
  try {
    const card = (sessionId: string) => (
      <WorkspaceFilesContext.Provider
        value={{ sessionId, workspacePath: "/work/repo" }}
      >
        <ArtifactCard inline artifact={present} />
      </WorkspaceFilesContext.Provider>
    );
    const { rerender } = render(card("s1"));
    fireEvent.click(
      screen.getByRole("button", {
        name: "Open release-notes.pdf in the Files window",
      }),
    );
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    rerender(card("s2"));
    answer(
      Response.json({
        object: "coddy.workspace_stat",
        entries: [{ path_rel: "reports/release-notes.pdf", kind: "file" }],
      }),
    );
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(opened).toEqual([]);
  } finally {
    stop();
  }
});

// The ways to copy are one item, Copy, whose submenu opens beside the menu
// the way a desktop app's does: the absolute path, the relative path and the
// file name, in that order.
test("Copy opens a submenu beside the menu with the paths and the name", () => {
  const writeText = vi.fn(async () => {});
  vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
  render(<ArtifactCard inline artifact={present} />);
  fireEvent.click(
    screen.getByRole("button", { name: "Actions for release-notes.pdf" }),
  );
  const copy = screen.getByRole("menuitem", { name: "Copy" });
  expect(copy).toHaveAttribute("aria-expanded", "false");
  fireEvent.click(copy);
  expect(copy).toHaveAttribute("aria-expanded", "true");
  const submenu = screen.getByRole("menu", { name: "Copy" });
  expect(submenu.parentElement).toBe(document.body);
  expect(
    Array.from(submenu.querySelectorAll('[role="menuitem"]')).map(
      (item) => item.textContent,
    ),
  ).toEqual(["Absolute path", "Relative path", "File name"]);

  fireEvent.click(screen.getByRole("menuitem", { name: "Relative path" }));
  expect(writeText).toHaveBeenCalledWith("reports/release-notes.pdf");
  expect(screen.queryByRole("menu")).toBeNull();
});

test("the pointer opens the Copy submenu, and another item puts it away", () => {
  render(<ArtifactCard inline artifact={present} />);
  fireEvent.click(
    screen.getByRole("button", { name: "Actions for release-notes.pdf" }),
  );
  fireEvent.mouseEnter(screen.getByRole("menuitem", { name: "Copy" }));
  expect(screen.getByRole("menu", { name: "Copy" })).toBeVisible();
  fireEvent.mouseEnter(screen.getByRole("menuitem", { name: "Download" }));
  expect(screen.queryByRole("menu", { name: "Copy" })).toBeNull();
  expect(screen.getByRole("menu")).toBeVisible();
});

// A real click starts with the pointer entering Copy, which opens the submenu;
// the click must leave it open, not toggle it shut again.
test("a click on Copy after the pointer entered it keeps the submenu open", () => {
  render(<ArtifactCard inline artifact={present} />);
  fireEvent.click(
    screen.getByRole("button", { name: "Actions for release-notes.pdf" }),
  );
  const copy = screen.getByRole("menuitem", { name: "Copy" });
  fireEvent.mouseEnter(copy);
  fireEvent.click(copy);
  expect(copy).toHaveAttribute("aria-expanded", "true");
  expect(screen.getByRole("menu", { name: "Copy" })).toBeVisible();
});

// The keyboard after the pointer: right on a Copy whose submenu the pointer
// already opened walks into it, and moving to another item puts it away, so
// the next Escape closes the menu.
test("the keyboard takes over a submenu the pointer opened", () => {
  render(<ArtifactCard inline artifact={present} />);
  const trigger = screen.getByRole("button", {
    name: "Actions for release-notes.pdf",
  });
  fireEvent.click(trigger);
  const menu = screen.getByRole("menu");
  const copy = screen.getByRole("menuitem", { name: "Copy" });
  fireEvent.mouseEnter(copy);
  copy.focus();
  fireEvent.keyDown(copy, { key: "ArrowRight" });
  expect(screen.getByRole("menuitem", { name: "Absolute path" })).toHaveFocus();

  fireEvent.keyDown(screen.getByRole("menu", { name: "Copy" }), {
    key: "ArrowLeft",
  });
  fireEvent.mouseEnter(copy);
  fireEvent.keyDown(menu, { key: "ArrowDown" });
  expect(screen.getByRole("menuitem", { name: "Download" })).toHaveFocus();
  expect(screen.queryByRole("menu", { name: "Copy" })).toBeNull();
  fireEvent.keyDown(document.activeElement!, { key: "Escape" });
  expect(screen.queryByRole("menu")).toBeNull();
  expect(trigger).toHaveFocus();
});

// WAI-ARIA's submenu keys: right opens it on its first item, left and Escape
// go back to Copy, and the next Escape closes the menu.
test("the keyboard walks into the Copy submenu and back out", () => {
  render(<ArtifactCard inline artifact={present} />);
  const trigger = screen.getByRole("button", {
    name: "Actions for release-notes.pdf",
  });
  fireEvent.click(trigger);
  const menu = screen.getByRole("menu");
  fireEvent.keyDown(menu, { key: "ArrowDown" });
  const copy = screen.getByRole("menuitem", { name: "Copy" });
  expect(copy).toHaveFocus();

  fireEvent.keyDown(copy, { key: "ArrowRight" });
  const submenu = screen.getByRole("menu", { name: "Copy" });
  expect(screen.getByRole("menuitem", { name: "Absolute path" })).toHaveFocus();
  fireEvent.keyDown(submenu, { key: "ArrowDown" });
  expect(screen.getByRole("menuitem", { name: "Relative path" })).toHaveFocus();
  fireEvent.keyDown(submenu, { key: "ArrowLeft" });
  expect(screen.queryByRole("menu", { name: "Copy" })).toBeNull();
  expect(copy).toHaveFocus();

  fireEvent.keyDown(copy, { key: "Enter" });
  expect(screen.getByRole("menuitem", { name: "Absolute path" })).toHaveFocus();
  fireEvent.keyDown(document.activeElement!, { key: "Escape" });
  expect(screen.queryByRole("menu", { name: "Copy" })).toBeNull();
  expect(screen.getByRole("menu")).toBeVisible();
  expect(copy).toHaveFocus();
  fireEvent.keyDown(document.activeElement!, { key: "Escape" });
  expect(screen.queryByRole("menu")).toBeNull();
  expect(trigger).toHaveFocus();
});

// A phone or a touch screen has no pointer to slide into a submenu: the menu
// is one list there, the ways to copy under a Copy heading, as the other menus
// of the stacked shell fold theirs.
test("on a phone the menu is one list, the copy items under a Copy heading", () => {
  stubMatchMedia((query) => query === shellStackMaxWidthMediaQuery);
  try {
    render(<ArtifactCard inline artifact={present} />);
    fireEvent.click(
      screen.getByRole("button", { name: "Actions for release-notes.pdf" }),
    );
    const menu = screen.getByRole("menu");
    expect(
      Array.from(menu.querySelectorAll('[role="menuitem"]')).map(
        (item) => item.textContent,
      ),
    ).toEqual([
      "Mention in chat",
      "Absolute path",
      "Relative path",
      "File name",
      "Download",
    ]);
    expect(within(menu).getByRole("group", { name: "Copy" })).toBeVisible();
    expect(screen.queryByRole("menuitem", { name: "Copy" })).toBeNull();
  } finally {
    cleanup();
  }
});

test("places the submenu beside the menu, on its left when the right has no room", () => {
  const view = { width: 1280, height: 800 };
  const sub = { width: 180, height: 110 };
  const menu = { left: 100, right: 300 };
  // Beside the menu's right edge, its first item level with Copy.
  expect(
    placeArtifactSubmenu({ top: 200, bottom: 230 }, menu, sub, view),
  ).toEqual({ left: 302, top: 196 });
  // No room on the right: on the left of the menu.
  expect(
    placeArtifactSubmenu(
      { top: 200, bottom: 230 },
      { left: 1000, right: 1200 },
      sub,
      view,
    ),
  ).toEqual({ left: 818, top: 196 });
  // At the window's foot it rises to stay inside.
  expect(
    placeArtifactSubmenu({ top: 760, bottom: 790 }, menu, sub, view),
  ).toEqual({ left: 302, top: 682 });
});

test("Tab closes the menu and leaves the focus moving on from the trigger", () => {
  render(<ArtifactCard inline artifact={artifact} />);
  const trigger = screen.getByRole("button", {
    name: "Actions for release-notes.pdf",
  });
  fireEvent.click(trigger);
  const tab = fireEvent.keyDown(screen.getByRole("menu"), { key: "Tab" });
  // Not prevented: the browser's own Tab then moves on from the trigger.
  expect(tab).toBe(true);
  expect(screen.queryByRole("menu")).toBeNull();
  expect(trigger).toHaveFocus();
});

test("places the menu under the trigger, above it at the window's foot, and inside the window", () => {
  const view = { width: 390, height: 720 };
  const menu = { width: 200, height: 238 };
  // Under the trigger, right edges together.
  expect(
    placeArtifactMenu({ top: 100, right: 360, bottom: 128 }, menu, view),
  ).toEqual({ left: 160, top: 134 });
  // A trigger at a phone's left edge keeps the menu on the screen.
  expect(
    placeArtifactMenu({ top: 100, right: 130, bottom: 128 }, menu, view),
  ).toEqual({ left: 8, top: 134 });
  // No room below: above the trigger.
  expect(
    placeArtifactMenu({ top: 650, right: 360, bottom: 678 }, menu, view),
  ).toEqual({ left: 160, top: 406 });
  // No room on either side: as low as the window allows.
  expect(
    placeArtifactMenu({ top: 120, right: 360, bottom: 148 }, menu, {
      width: 390,
      height: 300,
    }),
  ).toEqual({
    left: 160,
    top: 54,
  });
});

test("downloads a local artifact through a direct anchor without fetching", () => {
  const fetchSpy = vi.spyOn(window, "fetch");
  const click = vi
    .spyOn(HTMLAnchorElement.prototype, "click")
    .mockImplementation(() => {});
  render(
    <ToolCallMessage
      toolCallId="share-local"
      title="share_file"
      status="completed"
      artifacts={[artifact]}
    />,
  );

  fireEvent.click(
    screen.getByRole("button", { name: "Actions for release-notes.pdf" }),
  );
  fireEvent.click(screen.getByRole("menuitem", { name: "Download" }));
  expect(click).toHaveBeenCalledTimes(1);
  expect(fetchSpy).not.toHaveBeenCalled();
});

test("downloads a relay-mounted artifact through the environment request and releases its blob URL", async () => {
  setEnv({
    mode: "remote",
    baseUrl: "https://relay.example/swarm/nodes/nas02",
    token: "secret",
  });
  const fetchSpy = vi
    .spyOn(window, "fetch")
    .mockResolvedValue(new Response(new Blob(["artifact"]), { status: 200 }));
  const createObjectURL = vi.fn(() => "blob:artifact-download");
  const revokeObjectURL = vi.fn();
  vi.stubGlobal("URL", { ...URL, createObjectURL, revokeObjectURL });
  const click = vi
    .spyOn(HTMLAnchorElement.prototype, "click")
    .mockImplementation(() => {});

  render(
    <ToolCallMessage
      toolCallId="share-4"
      title="share_file"
      status="completed"
      artifacts={[artifact]}
    />,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Actions for release-notes.pdf" }),
  );
  fireEvent.click(screen.getByRole("menuitem", { name: "Download" }));

  await waitFor(() => expect(fetchSpy).toHaveBeenCalledTimes(1));
  expect(fetchSpy.mock.calls[0]?.[0]).toBe(
    "https://relay.example/swarm/nodes/nas02" + artifact.url,
  );
  expect(
    new Headers(fetchSpy.mock.calls[0]?.[1]?.headers).get("Authorization"),
  ).toBe("Bearer secret");
  expect(click).toHaveBeenCalledTimes(1);
  await waitFor(() =>
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:artifact-download"),
  );
});
