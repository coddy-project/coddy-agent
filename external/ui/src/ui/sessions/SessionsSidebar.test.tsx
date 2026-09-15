import React from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { SessionsSidebar } from "./SessionsSidebar";
import type { SessionRow } from "./types";

afterEach(() => cleanup());

const row = (id: string, title: string): SessionRow => ({
  id,
  title,
});

test("delete lives in the row menu and does not bubble to row pick", async () => {
  const onPick = vi.fn();
  const onDelete = vi.fn().mockResolvedValue(undefined);
  render(
    <SessionsSidebar
      sessionId="current"
      sessions={[row("current", "A"), row("other", "B")]}
      open
      onPick={onPick}
      onDelete={onDelete}
      searchDraft=""
      onSearchDraftChange={() => {}}
      onSearchClear={() => {}}
      hasMore={false}
      loadingMore={false}
      onLoadMore={() => {}}
    />,
  );
  // The row carries one control, not a strip of them; the actions are behind it.
  expect(screen.queryByTestId("session-delete-other")).toBeNull();

  fireEvent.click(screen.getByTestId("session-menu-other"));
  fireEvent.click(screen.getByTestId("session-menu-delete-other"));
  expect(onDelete).toHaveBeenCalledTimes(1);
  expect(onDelete).toHaveBeenCalledWith("other");
  expect(onPick).not.toHaveBeenCalled();
});

test("opening a row menu does not open the session", () => {
  const onPick = vi.fn();
  renderDrawer({ sessions: [row("other", "B")], onPick });
  fireEvent.click(screen.getByTestId("session-menu-other"));
  expect(screen.getByTestId("session-menu-delete-other")).toBeInTheDocument();
  expect(onPick).not.toHaveBeenCalled();
});

test("only one row menu is open at a time", () => {
  renderDrawer({ sessions: [row("a", "A"), row("b", "B")] });
  fireEvent.click(screen.getByTestId("session-menu-a"));
  expect(screen.getByTestId("session-menu-delete-a")).toBeInTheDocument();

  fireEvent.click(screen.getByTestId("session-menu-b"));
  expect(screen.queryByTestId("session-menu-delete-a")).toBeNull();
  expect(screen.getByTestId("session-menu-delete-b")).toBeInTheDocument();
});

test("escape closes the row menu", () => {
  renderDrawer({ sessions: [row("a", "A")] });
  fireEvent.click(screen.getByTestId("session-menu-a"));
  fireEvent.keyDown(window, { key: "Escape" });
  expect(screen.queryByTestId("session-menu-delete-a")).toBeNull();
});

test("clicking session row outside the text picks the session", () => {
  const onPick = vi.fn();
  render(
    <SessionsSidebar
      sessionId="current"
      sessions={[row("current", "A"), row("other", "B")]}
      open
      onPick={onPick}
      onDelete={() => Promise.resolve()}
      searchDraft=""
      onSearchDraftChange={() => {}}
      onSearchClear={() => {}}
      hasMore={false}
      loadingMore={false}
      onLoadMore={() => {}}
    />,
  );

  fireEvent.click(screen.getByTestId("session-row-other"));

  expect(onPick).toHaveBeenCalledTimes(1);
  expect(onPick).toHaveBeenCalledWith("other");
});

test("session row is a link with session hash href", () => {
  render(
    <SessionsSidebar
      sessionId="current"
      sessions={[row("sess-one", "Alpha")]}
      open
      onPick={() => {}}
      onDelete={() => Promise.resolve()}
      searchDraft=""
      onSearchDraftChange={() => {}}
      onSearchClear={() => {}}
      hasMore={false}
      loadingMore={false}
      onLoadMore={() => {}}
    />,
  );
  const link = screen.getByRole("link", { name: /Alpha/i });
  expect(link).toHaveAttribute("href", "#/s/sess-one");
});

test("draft session row links to #/draft/<id>", () => {
  render(
    <SessionsSidebar
      sessionId="draft_1"
      sessions={[row("draft_1", "Draft: hello")]}
      open
      onPick={() => {}}
      onDelete={() => Promise.resolve()}
      searchDraft=""
      onSearchDraftChange={() => {}}
      onSearchClear={() => {}}
      hasMore={false}
      loadingMore={false}
      onLoadMore={() => {}}
    />,
  );
  const link = screen.getByRole("link", { name: /Draft: hello/i });
  expect(link).toHaveAttribute("href", "#/draft/draft_1");
});

test("shows spinner and unread dot for other sessions", () => {
  render(
    <SessionsSidebar
      sessionId="current"
      sessions={[
        { id: "current", title: "A" },
        {
          id: "busy",
          title: "B",
          turnActive: true,
          unreadComplete: true,
        },
      ]}
      open
      onPick={() => {}}
      onDelete={() => Promise.resolve()}
      searchDraft=""
      onSearchDraftChange={() => {}}
      onSearchClear={() => {}}
      hasMore={false}
      loadingMore={false}
      onLoadMore={() => {}}
    />,
  );
  expect(screen.getByTestId("session-spinner-busy")).toBeInTheDocument();
  expect(screen.getByTestId("session-unread-busy")).toBeInTheDocument();
  expect(screen.queryByTestId("session-spinner-current")).toBeNull();
});

test("question pending hides spinner and shows animated question icon", () => {
  render(
    <SessionsSidebar
      sessionId="current"
      sessions={[
        { id: "current", title: "A" },
        { id: "q", title: "B", turnActive: true },
      ]}
      questionPendingSessionIds={new Set(["q"])}
      open
      onPick={() => {}}
      onDelete={() => Promise.resolve()}
      searchDraft=""
      onSearchDraftChange={() => {}}
      onSearchClear={() => {}}
      hasMore={false}
      loadingMore={false}
      onLoadMore={() => {}}
    />,
  );
  expect(screen.queryByTestId("session-spinner-q")).toBeNull();
  expect(screen.getByTestId("session-question-q")).toBeInTheDocument();
});

// --- grouping and the archive ---

const NOW = Date.parse("2026-09-15T12:00:00");

/** Renders the drawer with the props a grouping test needs, defaults filled in. */
function renderDrawer(
  props: Partial<Parameters<typeof SessionsSidebar>[0]> = {},
) {
  return render(
    <SessionsSidebar
      sessionId="current"
      sessions={[]}
      open
      onPick={() => {}}
      onDelete={() => Promise.resolve()}
      searchDraft=""
      onSearchDraftChange={() => {}}
      onSearchClear={() => {}}
      hasMore={false}
      loadingMore={false}
      onLoadMore={() => {}}
      now={NOW}
      {...props}
    />,
  );
}

const dated = (id: string, title: string, updatedAt: string): SessionRow => ({
  id,
  title,
  updatedAt,
});

test("without grouping the list is flat and carries no headings", () => {
  renderDrawer({
    sessions: [dated("a", "A", "2026-09-15T09:00:00")],
    groupMode: "none",
  });
  expect(screen.queryByTestId("session-group-today")).toBeNull();
  expect(screen.getByTestId("session-row-a")).toBeInTheDocument();
});

test("grouping by date puts every row under its own heading", () => {
  renderDrawer({
    sessions: [
      dated("today", "A", "2026-09-15T09:00:00"),
      dated("old", "B", "2026-01-02T09:00:00"),
    ],
    groupMode: "time",
  });
  expect(screen.getByTestId("session-group-today")).toHaveTextContent("Today");
  expect(screen.getByTestId("session-group-older")).toHaveTextContent("Older");
  expect(screen.getByTestId("session-row-today")).toBeInTheDocument();
  expect(screen.getByTestId("session-row-old")).toBeInTheDocument();
});

test("a heading collapses the rows under it and opens them again", () => {
  renderDrawer({
    sessions: [
      dated("today", "A", "2026-09-15T09:00:00"),
      dated("old", "B", "2026-01-02T09:00:00"),
    ],
    groupMode: "time",
  });
  fireEvent.click(screen.getByTestId("session-group-toggle-today"));
  expect(screen.queryByTestId("session-row-today")).toBeNull();
  // Collapsing one group leaves the others alone.
  expect(screen.getByTestId("session-row-old")).toBeInTheDocument();

  fireEvent.click(screen.getByTestId("session-group-toggle-today"));
  expect(screen.getByTestId("session-row-today")).toBeInTheDocument();
});

test("the filter menu is closed until its control is pressed, and shuts again", () => {
  renderDrawer();
  expect(screen.queryByTestId("sessions-filter-menu")).toBeNull();

  fireEvent.click(screen.getByTestId("sessions-filter-trigger"));
  expect(screen.getByTestId("sessions-filter-menu")).toBeInTheDocument();

  fireEvent.click(screen.getByTestId("sessions-filter-trigger"));
  expect(screen.queryByTestId("sessions-filter-menu")).toBeNull();
});

test("a section keeps its options folded until it is opened", () => {
  renderDrawer({ groupMode: "none" });
  fireEvent.click(screen.getByTestId("sessions-filter-trigger"));

  // The menu is four rows, each naming its current value; the choices live one
  // level in, so the panel stays the size of a menu rather than a list of lists.
  expect(
    screen.getByTestId("sessions-filter-section-group"),
  ).toBeInTheDocument();
  expect(screen.queryByTestId("sessions-filter-group-workspace")).toBeNull();

  fireEvent.click(screen.getByTestId("sessions-filter-section-group"));
  expect(
    screen.getByTestId("sessions-filter-group-workspace"),
  ).toBeInTheDocument();
});

test("a section row says which value is currently in force", () => {
  renderDrawer({
    groupMode: "workspace",
    sortKey: "title",
    archiveFilter: "only",
  });
  fireEvent.click(screen.getByTestId("sessions-filter-trigger"));

  expect(screen.getByTestId("sessions-filter-section-group")).toHaveTextContent(
    "Folder",
  );
  expect(screen.getByTestId("sessions-filter-section-sort")).toHaveTextContent(
    "Name",
  );
  expect(
    screen.getByTestId("sessions-filter-section-status"),
  ).toHaveTextContent("Archived");
});

test("hovering a section opens it and closes the one before it", () => {
  renderDrawer();
  fireEvent.click(screen.getByTestId("sessions-filter-trigger"));

  fireEvent.mouseEnter(screen.getByTestId("sessions-filter-section-group"));
  expect(screen.getByTestId("sessions-filter-group-tag")).toBeInTheDocument();

  fireEvent.mouseEnter(screen.getByTestId("sessions-filter-section-sort"));
  expect(screen.queryByTestId("sessions-filter-group-tag")).toBeNull();
  expect(screen.getByTestId("sessions-filter-sort-title")).toBeInTheDocument();
});

test("picking a grouping reports it up and closes the menu", () => {
  const onGroupModeChange = vi.fn();
  renderDrawer({ groupMode: "none", onGroupModeChange });
  fireEvent.click(screen.getByTestId("sessions-filter-trigger"));
  fireEvent.click(screen.getByTestId("sessions-filter-section-group"));
  fireEvent.click(screen.getByTestId("sessions-filter-group-workspace"));
  expect(onGroupModeChange).toHaveBeenCalledWith("workspace");
  expect(screen.queryByTestId("sessions-filter-menu")).toBeNull();
});

test("status is the three sides of the archive, with the current one ticked", () => {
  const onArchiveFilterChange = vi.fn();
  renderDrawer({ archiveFilter: "exclude", onArchiveFilterChange });
  fireEvent.click(screen.getByTestId("sessions-filter-trigger"));
  fireEvent.click(screen.getByTestId("sessions-filter-section-status"));

  expect(screen.getByTestId("sessions-filter-status-exclude")).toHaveAttribute(
    "aria-checked",
    "true",
  );
  fireEvent.click(screen.getByTestId("sessions-filter-status-only"));
  expect(onArchiveFilterChange).toHaveBeenCalledWith("only");
});

test("sort is reported up for the server to apply", () => {
  const onSortKeyChange = vi.fn();
  renderDrawer({ sortKey: "updated", onSortKeyChange });
  fireEvent.click(screen.getByTestId("sessions-filter-trigger"));
  fireEvent.click(screen.getByTestId("sessions-filter-section-sort"));

  expect(screen.getByTestId("sessions-filter-sort-updated")).toHaveAttribute(
    "aria-checked",
    "true",
  );
  fireEvent.click(screen.getByTestId("sessions-filter-sort-title"));
  expect(onSortKeyChange).toHaveBeenCalledWith("title");
});

test("one environment is no choice at all, so the section stays out", () => {
  renderDrawer({
    environments: [
      { key: "local", label: "Local", active: true, onPick: () => {} },
    ],
  });
  fireEvent.click(screen.getByTestId("sessions-filter-trigger"));
  expect(
    screen.queryByTestId("sessions-filter-section-environment"),
  ).toBeNull();
});

test("an environment row switches where the history is read from", () => {
  const onPick = vi.fn();
  renderDrawer({
    environments: [
      { key: "local", label: "Local", active: true, onPick: () => {} },
      { key: "nas02", label: "nas02", active: false, onPick },
    ],
  });
  fireEvent.click(screen.getByTestId("sessions-filter-trigger"));
  fireEvent.click(screen.getByTestId("sessions-filter-section-environment"));
  fireEvent.click(screen.getByTestId("sessions-filter-env-nas02"));
  expect(onPick).toHaveBeenCalledTimes(1);
  expect(screen.queryByTestId("sessions-filter-menu")).toBeNull();
});

test("escape folds an open section first, and the menu next", () => {
  renderDrawer();
  fireEvent.click(screen.getByTestId("sessions-filter-trigger"));
  fireEvent.click(screen.getByTestId("sessions-filter-section-sort"));
  expect(screen.getByTestId("sessions-filter-sort-title")).toBeInTheDocument();

  fireEvent.keyDown(window, { key: "Escape" });
  expect(screen.queryByTestId("sessions-filter-sort-title")).toBeNull();
  expect(screen.getByTestId("sessions-filter-menu")).toBeInTheDocument();

  fireEvent.keyDown(window, { key: "Escape" });
  expect(screen.queryByTestId("sessions-filter-menu")).toBeNull();
});

test("a row archives from its menu, without opening the session", () => {
  const onArchive = vi.fn();
  const onPick = vi.fn();
  renderDrawer({
    sessions: [row("other", "B")],
    onArchive,
    onPick,
  });
  fireEvent.click(screen.getByTestId("session-menu-other"));
  fireEvent.click(screen.getByTestId("session-menu-archive-other"));
  expect(onArchive).toHaveBeenCalledWith("other", true);
  expect(onPick).not.toHaveBeenCalled();
  // The menu closes behind the action it performed.
  expect(screen.queryByTestId("session-menu-archive-other")).toBeNull();
});

test("an archived row says so and its menu puts it back", () => {
  const onArchive = vi.fn();
  renderDrawer({
    sessions: [{ id: "filed", title: "B", archived: true }],
    archiveFilter: "all",
    onArchive,
  });
  expect(screen.getByTestId("session-archived-filed")).toBeInTheDocument();
  fireEvent.click(screen.getByTestId("session-menu-filed"));
  expect(screen.getByTestId("session-menu-archive-filed")).toHaveTextContent(
    "Unarchive",
  );
  fireEvent.click(screen.getByTestId("session-menu-archive-filed"));
  expect(onArchive).toHaveBeenCalledWith("filed", false);
});

test("a shell that offers no archiving still offers delete", () => {
  renderDrawer({ sessions: [row("a", "A")] });
  fireEvent.click(screen.getByTestId("session-menu-a"));
  expect(screen.queryByTestId("session-menu-archive-a")).toBeNull();
  expect(screen.getByTestId("session-menu-delete-a")).toBeInTheDocument();
});

test("a row shows the tags it is filed under", () => {
  renderDrawer({
    sessions: [{ id: "a", title: "A", tags: ["backend", "ui"] }],
  });
  const tags = screen.getByTestId("session-tags-a");
  expect(tags).toHaveTextContent("backend");
  expect(tags).toHaveTextContent("ui");
});

test("a folder heading starts a new chat in that folder", () => {
  const onNewChatInWorkspace = vi.fn();
  renderDrawer({
    sessions: [
      { id: "a", title: "A", cwd: "/srv/one" },
      { id: "b", title: "B", cwd: "/srv/two" },
    ],
    groupMode: "workspace",
    onNewChatInWorkspace,
  });

  fireEvent.click(screen.getByTestId("session-group-new-cwd:/srv/one"));
  // The full path travels, not the name on the heading: that is what puts the
  // session in the right checkout and lets the server read its branch.
  expect(onNewChatInWorkspace).toHaveBeenCalledWith("/srv/one");
});

test("only a folder heading offers that: a date bucket is not a workspace", () => {
  renderDrawer({
    sessions: [dated("a", "A", "2026-09-15T09:00:00")],
    groupMode: "time",
    onNewChatInWorkspace: () => {},
  });
  expect(screen.queryByTestId("session-group-new-today")).toBeNull();
});

test("a row pins and unpins from its menu", () => {
  const onPin = vi.fn();
  renderDrawer({ sessions: [row("a", "A")], onPin });
  fireEvent.click(screen.getByTestId("session-menu-a"));
  expect(screen.getByTestId("session-menu-pin-a")).toHaveTextContent(
    "Pin to the top",
  );
  fireEvent.click(screen.getByTestId("session-menu-pin-a"));
  expect(onPin).toHaveBeenCalledWith("a", true);
});

test("a pinned row says so and offers to let it go", () => {
  const onPin = vi.fn();
  renderDrawer({ sessions: [{ id: "a", title: "A", pinned: true }], onPin });
  expect(screen.getByTestId("session-pinned-a")).toBeInTheDocument();

  fireEvent.click(screen.getByTestId("session-menu-a"));
  expect(screen.getByTestId("session-menu-pin-a")).toHaveTextContent("Unpin");
  fireEvent.click(screen.getByTestId("session-menu-pin-a"));
  expect(onPin).toHaveBeenCalledWith("a", false);
});

test("an archived row reads as put aside", () => {
  renderDrawer({
    sessions: [row("a", "A"), { id: "filed", title: "B", archived: true }],
    archiveFilter: "all",
  });
  expect(screen.getByTestId("session-row-filed").className).toContain(
    "is-archived",
  );
  expect(screen.getByTestId("session-row-a").className).not.toContain(
    "is-archived",
  );
});
