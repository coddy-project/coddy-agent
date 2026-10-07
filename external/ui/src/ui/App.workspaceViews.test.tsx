import React from "react";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";
import { ConfirmProvider } from "./components/useConfirm";
import { initLocale } from "./i18n/i18n";
import { t } from "./i18n/i18n";
import { resetSettingsConfigForTests } from "./settings/settingsConfigStore";
import { shellStackMaxWidthMediaQuery } from "./shellBreakpoint";
import { forgetOpenFiles } from "./files/FilesView";

/**
 * The views of a session are buttons of the chat header: its background tasks
 * open in the dock beside the chat, without a tab strip; its edits open in a
 * window of their own, and its files in a window over the chat, the way the
 * documentation does, each with an address of its own.
 */

vi.mock("./chat/ChatScreen", () => ({
  ChatScreen: (props: {
    onOpenBackgroundTasks?: () => void;
    onOpenEdits?: () => void;
    onOpenFiles?: () => void;
  }) => (
    <div data-testid="chat-screen-stub">
      <button
        type="button"
        data-testid="open-tasks"
        onClick={() => props.onOpenBackgroundTasks?.()}
      >
        Open tasks
      </button>
      <button
        type="button"
        data-testid="open-edits"
        onClick={() => props.onOpenEdits?.()}
      >
        Open edits
      </button>
      <button
        type="button"
        data-testid="open-files"
        onClick={() => props.onOpenFiles?.()}
      >
        Open files
      </button>
    </div>
  ),
}));

const SID = "sess_views";

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

function heldStream() {
  return new Response(new ReadableStream<Uint8Array>({ start: () => {} }), {
    headers: { "Content-Type": "text/event-stream" },
  });
}

const fetchMock = vi.fn(
  async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === "/coddy/events") return heldStream();
    if (path.startsWith("/coddy/sessions?")) {
      return json({
        active_count: 1,
        sessions: [{ id: SID, title: "A chat" }],
      });
    }
    if (path.startsWith(`/coddy/sessions/${SID}/messages`)) {
      return json({ session_id: SID, messages: [] });
    }
    if (path.startsWith(`/coddy/sessions/${SID}/changes`)) {
      return json({
        session_id: SID,
        vcs: "git",
        files: [
          {
            path: "notes.txt",
            status: "modified",
            additions: 1,
            deletions: 0,
            binary: false,
            truncated: false,
          },
        ],
        totals: { files: 1, additions: 1, deletions: 0 },
      });
    }
    if (path.includes("/workspace/tree")) {
      return json({
        entries: [
          {
            name: "notes.txt",
            path_rel: "notes.txt",
            kind: "file",
            size_bytes: 10,
            mod_time: "2026-10-05T12:00:00Z",
          },
        ],
        has_more: false,
        next_cursor: "",
      });
    }
    if (path.includes("/workspace/raw") && init?.method === "HEAD") {
      return new Response(null, {
        headers: {
          ETag: '"v1"',
          "Content-Type": "text/plain; charset=utf-8",
          "Content-Length": "22",
          "Last-Modified": "Mon, 05 Oct 2026 12:00:00 GMT",
        },
      });
    }
    if (path.includes("/workspace/text")) {
      return json({
        path_rel: "notes.txt",
        lines: ["first note", "second note"],
        offset: 0,
        next_offset: 2,
        has_more: false,
        etag: '"v1"',
      });
    }
    return json({}, 404);
  },
);

beforeEach(() => {
  resetSettingsConfigForTests();
  forgetOpenFiles();
  initLocale("en");
  localStorage.clear();
  Element.prototype.scrollIntoView = vi.fn();
  history.replaceState(null, "", "/");
  fetchMock.mockClear();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  history.replaceState(null, "", "/");
});

function mountAt(hash: string) {
  history.replaceState(null, "", `/${hash}`);
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
}

const pressEscape = (
  target: Element = document.activeElement ?? document.body,
) => fireEvent.keyDown(target, { key: "Escape" });

test("the edits open in their window, and the address names them", async () => {
  mountAt(`#/s/${SID}`);
  fireEvent.click(await screen.findByTestId("open-edits"));
  const win = await screen.findByTestId("edits-view");
  expect(within(win).getByText(t("changes.viewer.title"))).toBeTruthy();
  // No dock face for the edits: the window is their only view.
  expect(screen.queryByTestId("changes-panel")).toBeNull();
  expect(screen.queryByTestId("bgtasks-panel")).toBeNull();
  await waitFor(() => expect(window.location.hash).toBe(`#/s/${SID}/changes`));
});

test("an edits address opens the window, and Escape closes it", async () => {
  mountAt(`#/s/${SID}/changes`);
  await screen.findByTestId("edits-view");
  pressEscape();
  await waitFor(() => expect(screen.queryByTestId("edits-view")).toBeNull());
  await waitFor(() => expect(window.location.hash).toBe(`#/s/${SID}`));
});

// The edits window sits over the chat like the Files window: the shell's
// backdrop covers the chat under it, and a click there puts it away.
test("a click on the backdrop beside the edits window puts it away", async () => {
  mountAt(`#/s/${SID}/changes`);
  await screen.findByTestId("edits-view");
  const backdrop = document.querySelector(".backdrop") as HTMLElement;
  expect(backdrop).toHaveClass("is-open");
  fireEvent.click(backdrop);
  await waitFor(() => expect(screen.queryByTestId("edits-view")).toBeNull());
  await waitFor(() => expect(window.location.hash).toBe(`#/s/${SID}`));
});

test("the background tasks open in the dock with no tab strip", async () => {
  mountAt(`#/s/${SID}`);
  fireEvent.click(await screen.findByTestId("open-tasks"));
  const panel = await screen.findByTestId("bgtasks-panel");
  expect(within(panel).getByText(t("tasks.panelTitle"))).toBeTruthy();
  expect(document.querySelector('[role="tablist"]')).toBeNull();
});

test("the files open in a window over the chat, not in the dock, and Escape closes it", async () => {
  mountAt(`#/s/${SID}`);
  fireEvent.click(await screen.findByTestId("open-files"));
  const win = await screen.findByTestId("files-view");
  expect(win.getAttribute("role")).toBe("dialog");
  expect(screen.queryByTestId("changes-panel")).toBeNull();
  expect(screen.queryByTestId("bgtasks-panel")).toBeNull();
  await waitFor(() => expect(window.location.hash).toBe(`#/s/${SID}/files`));
  pressEscape();
  await waitFor(() => expect(screen.queryByTestId("files-view")).toBeNull());
  await waitFor(() => expect(window.location.hash).toBe(`#/s/${SID}`));
});

test("a files address opens the window on its file", async () => {
  mountAt(`#/s/${SID}/files?path=notes.txt&line=2`);
  const win = await screen.findByTestId("files-view");
  await within(win).findByText("second note");
  expect(within(win).getByRole("tab", { selected: true }).textContent).toBe(
    "notes.txt",
  );
});

test("over the background tasks the files window is the first thing Escape takes away", async () => {
  mountAt(`#/s/${SID}`);
  fireEvent.click(await screen.findByTestId("open-tasks"));
  await screen.findByTestId("bgtasks-panel");
  fireEvent.click(screen.getByTestId("open-files"));
  await screen.findByTestId("files-view");
  expect(screen.getByTestId("bgtasks-panel")).toBeTruthy();
  pressEscape();
  await waitFor(() => expect(screen.queryByTestId("files-view")).toBeNull());
  expect(screen.getByTestId("bgtasks-panel")).toBeTruthy();
  pressEscape();
  await waitFor(() => expect(screen.queryByTestId("bgtasks-panel")).toBeNull());
});

test("Ctrl+Shift+F opens the files window, and closes it", async () => {
  mountAt(`#/s/${SID}`);
  await screen.findByTestId("chat-screen-stub");
  // Matched on the key's place, so the Russian layout opens it too.
  await act(async () => {
    fireEvent.keyDown(document.body, {
      key: "А",
      code: "KeyF",
      ctrlKey: true,
      shiftKey: true,
    });
  });
  await screen.findByTestId("files-view");
  await act(async () => {
    fireEvent.keyDown(document.body, {
      key: "F",
      code: "KeyF",
      ctrlKey: true,
      shiftKey: true,
    });
  });
  await waitFor(() => expect(screen.queryByTestId("files-view")).toBeNull());
});

test("a click beside the files window closes it and gives the address back to the chat", async () => {
  mountAt(`#/s/${SID}`);
  fireEvent.click(await screen.findByTestId("open-files"));
  await screen.findByTestId("files-view");
  fireEvent.click(document.querySelector(".backdrop.is-open")!);
  await waitFor(() => expect(screen.queryByTestId("files-view")).toBeNull());
  await waitFor(() => expect(window.location.hash).toBe(`#/s/${SID}`));
});

// The stacked shell shows the dock over the chat; the files window opens over
// the dock and leaves it there.
test("on the stacked shell the files window opens over the background tasks and leaves them open", async () => {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: query === shellStackMaxWidthMediaQuery,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  }));
  mountAt(`#/s/${SID}`);
  fireEvent.click(await screen.findByTestId("open-tasks"));
  await screen.findByTestId("bgtasks-panel");
  fireEvent.click(screen.getByTestId("open-files"));
  await screen.findByTestId("files-view");
  await act(async () => new Promise((r) => setTimeout(r, 30)));
  expect(screen.getByTestId("bgtasks-panel")).toBeTruthy();
  pressEscape();
  await waitFor(() => expect(screen.queryByTestId("files-view")).toBeNull());
  expect(screen.getByTestId("bgtasks-panel")).toBeTruthy();
  await waitFor(() => expect(window.location.hash).toBe(`#/s/${SID}/tasks`));
});

test("the files window opened over the edits window takes its place", async () => {
  mountAt(`#/s/${SID}`);
  fireEvent.click(await screen.findByTestId("open-edits"));
  await screen.findByTestId("edits-view");
  fireEvent.click(screen.getByTestId("open-files"));
  await screen.findByTestId("files-view");
  expect(screen.queryByTestId("edits-view")).toBeNull();
});

test("an edits address over the files window puts the files away", async () => {
  mountAt(`#/s/${SID}/files?path=notes.txt`);
  await screen.findByTestId("files-view");
  await act(async () => {
    window.location.hash = `#/s/${SID}/changes`;
  });
  await screen.findByTestId("edits-view");
  await waitFor(() => expect(screen.queryByTestId("files-view")).toBeNull());
});
