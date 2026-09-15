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

test("delete click does not bubble to row pick", async () => {
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
  fireEvent.click(screen.getByTestId("session-delete-other"));
  expect(onDelete).toHaveBeenCalledTimes(1);
  expect(onDelete).toHaveBeenCalledWith("other");
  expect(onPick).not.toHaveBeenCalled();
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

test("picking a grouping reports it up and closes the menu", () => {
  const onGroupModeChange = vi.fn();
  renderDrawer({ groupMode: "none", onGroupModeChange });
  fireEvent.click(screen.getByTestId("sessions-filter-trigger"));
  fireEvent.click(screen.getByTestId("sessions-filter-group-workspace"));
  expect(onGroupModeChange).toHaveBeenCalledWith("workspace");
  expect(screen.queryByTestId("sessions-filter-menu")).toBeNull();
});

test("status is the three sides of the archive, with the current one ticked", () => {
  const onArchiveFilterChange = vi.fn();
  renderDrawer({ archiveFilter: "exclude", onArchiveFilterChange });
  fireEvent.click(screen.getByTestId("sessions-filter-trigger"));

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
  expect(screen.queryByTestId("sessions-filter-env-local")).toBeNull();
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
  fireEvent.click(screen.getByTestId("sessions-filter-env-nas02"));
  expect(onPick).toHaveBeenCalledTimes(1);
  expect(screen.queryByTestId("sessions-filter-menu")).toBeNull();
});

test("a row archives without opening the session", () => {
  const onArchive = vi.fn();
  const onPick = vi.fn();
  renderDrawer({
    sessions: [row("other", "B")],
    onArchive,
    onPick,
  });
  fireEvent.click(screen.getByTestId("session-archive-other"));
  expect(onArchive).toHaveBeenCalledWith("other", true);
  expect(onPick).not.toHaveBeenCalled();
});

test("an archived row says so and its button puts it back", () => {
  const onArchive = vi.fn();
  renderDrawer({
    sessions: [{ id: "filed", title: "B", archived: true }],
    archiveFilter: "all",
    onArchive,
  });
  expect(screen.getByTestId("session-archived-filed")).toBeInTheDocument();
  fireEvent.click(screen.getByTestId("session-archive-filed"));
  expect(onArchive).toHaveBeenCalledWith("filed", false);
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
