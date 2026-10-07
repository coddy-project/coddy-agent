import React from "react";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { afterEach, expect, onTestFinished, test, vi } from "vitest";
import { setEnv } from "../env/remoteEnv";
import { SessionsSidebar } from "./SessionsSidebar";
import type { SessionRow } from "./types";

afterEach(() => {
  cleanup();
  setEnv({ mode: "local" });
  window.localStorage.clear();
});

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

test("shows activity on every running session without duplicating unread state", () => {
  render(
    <SessionsSidebar
      sessionId="current"
      sessions={[
        { id: "current", title: "A", turnActive: true },
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
  expect(screen.getByTestId("session-activity-busy")).toBeInTheDocument();
  expect(screen.queryByTestId("session-unread-busy")).toBeNull();
  expect(screen.getByTestId("session-activity-current")).toBeInTheDocument();
  expect(screen.queryByTestId("session-unread-current")).toBeNull();
});

test("question pending hides the activity dot and shows animated question icon", () => {
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
  expect(screen.queryByTestId("session-activity-q")).toBeNull();
  expect(screen.queryByTestId("session-idle-q")).toBeNull();
  expect(screen.getByTestId("session-question-q")).toBeInTheDocument();
});

test("server-reported permission owns the state slot and exposes its name", () => {
  renderDrawer({
    sessionId: "other",
    sessions: [
      {
        id: "permission",
        title: "Permission",
        turnActive: true,
        permissionPending: true,
      },
    ],
  });

  expect(screen.queryByTestId("session-activity-permission")).toBeNull();
  expect(screen.queryByTestId("session-idle-permission")).toBeNull();
  expect(
    screen.getByRole("img", { name: "Permission required" }),
  ).toBeInTheDocument();
});

test("server-reported question owns the state slot before its chat is opened", () => {
  renderDrawer({
    sessionId: "other",
    sessions: [
      {
        id: "question",
        title: "Question",
        turnActive: true,
        questionPending: true,
      },
    ],
  });

  expect(screen.queryByTestId("session-activity-question")).toBeNull();
  expect(
    screen.getByRole("img", { name: "Question pending" }),
  ).toBeInTheDocument();
});

test("permission marker wins when a row reports both pending states", () => {
  renderDrawer({
    sessionId: "other",
    sessions: [
      {
        id: "both",
        title: "Both",
        turnActive: true,
        permissionPending: true,
        questionPending: true,
      },
    ],
  });

  expect(screen.getByTestId("session-permission-both")).toBeInTheDocument();
  expect(screen.queryByTestId("session-question-both")).toBeNull();
  expect(
    screen.getAllByRole("img", { name: /pending|required/i }),
  ).toHaveLength(1);
});

test("finished and failed rows carry a state dot that distinguishes unseen errors", () => {
  renderDrawer({
    sessionId: "current",
    sessions: [
      { id: "idle", title: "Idle" },
      {
        id: "unseen-error",
        title: "Unseen error",
        lastErrorSeq: 4,
        readActivitySeq: 3,
      },
      {
        id: "seen-error",
        title: "Seen error",
        lastErrorSeq: 4,
        readActivitySeq: 4,
        unreadComplete: true,
      },
    ],
  });

  expect(screen.getByTestId("session-idle-idle")).toBeInTheDocument();
  expect(screen.getByTestId("session-error-unseen-error")).not.toHaveClass(
    "is-seen",
  );
  expect(screen.getByTestId("session-error-seen-error")).toHaveClass("is-seen");
  expect(screen.queryByTestId("session-unread-seen-error")).toBeNull();
});

test("the dot names background work when the row has no turn running", () => {
  renderDrawer({
    sessionId: "other",
    sessions: [
      { id: "bg", title: "Detached", backgroundRunning: 1 },
      { id: "turn", title: "Answering", turnActive: true },
    ],
  });
  expect(screen.getByTestId("session-activity-bg")).toHaveAttribute(
    "aria-label",
    "Background tasks running",
  );
  expect(screen.getByTestId("session-activity-bg")).toHaveAttribute(
    "title",
    "Background tasks running",
  );
  // A turn in flight keeps its own wording even when tasks run beside it.
  expect(screen.getByTestId("session-activity-turn")).toHaveAttribute(
    "aria-label",
    "Turn running",
  );
});

test("the state marks stand apart from the title so the tags line up under its text", () => {
  render(
    <SessionsSidebar
      sessionId="other"
      sessions={[
        { id: "busy", title: "Busy", turnActive: true, tags: ["planning"] },
        { id: "calm", title: "Calm", tags: ["scheduling"] },
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
  const busy = screen.getByTestId("session-row-busy");
  const marks = busy.querySelector(".session-row-marks");
  expect(marks).not.toBeNull();
  expect(marks?.contains(screen.getByTestId("session-activity-busy"))).toBe(
    true,
  );
  expect(
    busy.querySelector(".session-row-leading .session-activity-dot"),
  ).toBeNull();
  // The transparent finished ring gives grouped rows the same left anchor as
  // a busy row without pretending work is still running.
  const calm = screen.getByTestId("session-row-calm");
  expect(calm.querySelector(".session-row-marks")).not.toBeNull();
  expect(screen.getByTestId("session-idle-calm")).toBeInTheDocument();
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

test("a collapsed workspace group survives navigation and temporary absence", () => {
  const sessions = [
    { id: "workspace", title: "Report", cwd: "/srv/reports" },
  ] as SessionRow[];
  const first = renderDrawer({ sessions, groupMode: "workspace" });
  const toggle = screen.getByTestId("session-group-toggle-cwd:/srv/reports");
  fireEvent.click(toggle);
  expect(toggle).toHaveAttribute("aria-expanded", "false");
  first.unmount();

  const withoutGroup = renderDrawer({ sessions: [], groupMode: "workspace" });
  withoutGroup.unmount();

  renderDrawer({ sessions, groupMode: "workspace" });
  expect(
    screen.getByTestId("session-group-toggle-cwd:/srv/reports"),
  ).toHaveAttribute("aria-expanded", "false");
  expect(screen.queryByTestId("session-row-workspace")).toBeNull();
});

test("collapsed groups are scoped to their environment", () => {
  const sessions = [dated("today", "Report", "2026-09-15T09:00:00")];
  setEnv({ mode: "remote", baseUrl: "https://alpha.example", token: "" });
  const alpha = renderDrawer({ sessions, groupMode: "time" });
  fireEvent.click(screen.getByTestId("session-group-toggle-today"));
  alpha.unmount();

  setEnv({ mode: "remote", baseUrl: "https://beta.example", token: "" });
  const beta = renderDrawer({ sessions, groupMode: "time" });
  expect(screen.getByTestId("session-group-toggle-today")).toHaveAttribute(
    "aria-expanded",
    "true",
  );
  beta.unmount();

  setEnv({ mode: "remote", baseUrl: "https://alpha.example", token: "" });
  renderDrawer({ sessions, groupMode: "time" });
  expect(screen.getByTestId("session-group-toggle-today")).toHaveAttribute(
    "aria-expanded",
    "false",
  );
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

  pointer(screen.getByTestId("sessions-filter-section-group"), "pointerover", {
    y: 0,
  });
  expect(screen.getByTestId("sessions-filter-group-tag")).toBeInTheDocument();

  pointer(screen.getByTestId("sessions-filter-section-sort"), "pointerover", {
    y: 0,
  });
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
      {
        kind: "origin",
        key: "local",
        label: "Local",
        active: true,
        onPick: () => {},
      },
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
      {
        kind: "origin",
        key: "local",
        label: "Local",
        active: true,
        onPick: () => {},
      },
      { kind: "switch", key: "nas02", label: "nas02", active: false, onPick },
    ],
  });
  fireEvent.click(screen.getByTestId("sessions-filter-trigger"));
  fireEvent.click(screen.getByTestId("sessions-filter-section-environment"));
  fireEvent.click(screen.getByTestId("sessions-filter-env-nas02"));
  expect(onPick).toHaveBeenCalledTimes(1);
  expect(screen.queryByTestId("sessions-filter-menu")).toBeNull();
});

test("environment origins and remote switches keep independent menu semantics", () => {
  renderDrawer({
    environments: [
      {
        kind: "origin",
        key: "all",
        label: "All",
        active: false,
        onPick: () => {},
      },
      {
        kind: "origin",
        key: "gateway",
        label: "Gateway",
        active: true,
        onPick: () => {},
      },
      {
        kind: "switch",
        key: "nas02",
        label: "nas02",
        active: true,
        onPick: () => {},
      },
    ],
  });
  fireEvent.click(screen.getByTestId("sessions-filter-trigger"));
  const section = screen.getByTestId("sessions-filter-section-environment");
  expect(section).toHaveTextContent("nas02 · Gateway");
  expect(section.querySelector(".sessions-filter-value")).not.toHaveClass(
    "is-default",
  );

  fireEvent.click(section);
  expect(screen.getByTestId("sessions-filter-env-gateway")).toHaveAttribute(
    "role",
    "menuitemradio",
  );
  expect(screen.getByTestId("sessions-filter-env-gateway")).toHaveAttribute(
    "aria-checked",
    "true",
  );
  expect(screen.getByTestId("sessions-filter-env-nas02")).toHaveAttribute(
    "role",
    "menuitem",
  );
  expect(screen.getByTestId("sessions-filter-env-nas02")).toHaveAttribute(
    "aria-current",
    "true",
  );
  expect(screen.getByTestId("sessions-filter-env-nas02")).not.toHaveAttribute(
    "aria-checked",
  );
  expect(screen.getByTestId("sessions-filter-env-nas02")).toHaveClass(
    "starts-group",
  );
});

test("environment summary names only the remote when All is selected", () => {
  renderDrawer({
    environments: [
      {
        kind: "origin",
        key: "all",
        label: "All",
        active: true,
        onPick: () => {},
      },
      {
        kind: "switch",
        key: "nas02",
        label: "nas02",
        active: true,
        onPick: () => {},
      },
    ],
  });
  fireEvent.click(screen.getByTestId("sessions-filter-trigger"));
  const section = screen.getByTestId("sessions-filter-section-environment");
  expect(section.querySelector(".sessions-filter-value")).toHaveTextContent(
    "nas02",
  );
  expect(section.querySelector(".sessions-filter-value")).not.toHaveClass(
    "is-default",
  );
});

test("environment summary uses the origin and defaults only to All locally", () => {
  renderDrawer({
    environments: [
      {
        kind: "origin",
        key: "all",
        label: "All",
        active: true,
        onPick: () => {},
      },
      {
        kind: "origin",
        key: "gateway",
        label: "Gateway",
        active: false,
        onPick: () => {},
      },
    ],
  });
  fireEvent.click(screen.getByTestId("sessions-filter-trigger"));
  let value = screen
    .getByTestId("sessions-filter-section-environment")
    .querySelector(".sessions-filter-value");
  expect(value).toHaveTextContent("All");
  expect(value).toHaveClass("is-default");

  cleanup();
  renderDrawer({
    environments: [
      {
        kind: "origin",
        key: "all",
        label: "All",
        active: false,
        onPick: () => {},
      },
      {
        kind: "origin",
        key: "gateway",
        label: "Gateway",
        active: true,
        onPick: () => {},
      },
    ],
  });
  fireEvent.click(screen.getByTestId("sessions-filter-trigger"));
  value = screen
    .getByTestId("sessions-filter-section-environment")
    .querySelector(".sessions-filter-value");
  expect(value).toHaveTextContent("Gateway");
  expect(value).not.toHaveClass("is-default");
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

test("a pinned row stands under the heading, unmarked, and offers to let it go", () => {
  const onPin = vi.fn();
  renderDrawer({ sessions: [{ id: "a", title: "A", pinned: true }], onPin });
  expect(screen.getByTestId("session-group-pinned")).toContainElement(
    screen.getByTestId("session-row-a"),
  );
  expect(document.querySelector(".session-pin-mark")).toBeNull();

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

// --- Reordering the pins ----------------------------------------------------

/** Fires a pointer event; jsdom has no PointerEvent, and React reads only the type. */
function pointer(
  target: EventTarget,
  type: string,
  init: { x?: number; y: number; kind?: string },
) {
  const ev = new MouseEvent(type, {
    bubbles: true,
    cancelable: true,
    button: 0,
    clientX: init.x ?? 10,
    clientY: init.y,
  });
  Object.defineProperty(ev, "pointerId", { value: 1 });
  Object.defineProperty(ev, "pointerType", { value: init.kind ?? "mouse" });
  act(() => {
    target.dispatchEvent(ev);
  });
}

/** Three pins, 40px tall each, stacked from the top of the list. */
function renderPins(props: Partial<Parameters<typeof SessionsSidebar>[0]>) {
  const pins = ["a", "b", "c"].map((id) => ({ id, title: id, pinned: true }));
  vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(
    function (this: Element) {
      const at = pins.findIndex(
        (p) => this.getAttribute("data-testid") === `session-row-${p.id}`,
      );
      return { top: at * 40, height: 40 } as DOMRect;
    },
  );
  onTestFinished(() => {
    vi.restoreAllMocks();
  });
  return renderDrawer({ sessions: pins, ...props });
}

test("a pinned row has no grip and is dragged by the row itself", () => {
  const onReorderPins = vi.fn();
  const onPick = vi.fn();
  renderPins({ onReorderPins, onPick });
  expect(document.querySelector(".session-drag-grip")).toBeNull();

  const rowA = screen.getByTestId("session-row-a");
  expect(rowA.className).toContain("is-reorderable");
  pointer(rowA, "pointerdown", { y: 20 });
  pointer(window, "pointermove", { y: 110 });
  expect(rowA.className).toContain("is-dragging");
  pointer(window, "pointerup", { y: 110 });
  // The click the release produces must not open the row that was moved.
  fireEvent.click(rowA);

  expect(onReorderPins).toHaveBeenCalledWith(["b", "c", "a"]);
  expect(onPick).not.toHaveBeenCalled();
});

test("a mouse press that stays within the slop is a click, not a drag", () => {
  const onReorderPins = vi.fn();
  const onPick = vi.fn();
  renderPins({ onReorderPins, onPick });
  const rowB = screen.getByTestId("session-row-b");
  pointer(rowB, "pointerdown", { y: 60 });
  pointer(window, "pointermove", { y: 62 });
  pointer(window, "pointerup", { y: 62 });
  fireEvent.click(rowB);

  expect(onReorderPins).not.toHaveBeenCalled();
  expect(onPick).toHaveBeenCalledWith("b");
});

test("a finger drags a pin only after holding it, and a swipe scrolls", () => {
  vi.useFakeTimers();
  onTestFinished(() => {
    vi.useRealTimers();
  });
  const onReorderPins = vi.fn();
  renderPins({ onReorderPins });
  const rowA = screen.getByTestId("session-row-a");

  // Moving straight away is a scroll: the hold never takes the row.
  pointer(rowA, "pointerdown", { y: 20, kind: "touch" });
  pointer(window, "pointermove", { y: 110, kind: "touch" });
  act(() => vi.advanceTimersByTime(1000));
  expect(rowA.className).not.toContain("is-dragging");
  pointer(window, "pointerup", { y: 110, kind: "touch" });
  expect(onReorderPins).not.toHaveBeenCalled();

  // Holding first takes the row, and then the finger moves it.
  pointer(rowA, "pointerdown", { y: 20, kind: "touch" });
  act(() => vi.advanceTimersByTime(500));
  expect(rowA.className).toContain("is-dragging");
  pointer(window, "pointermove", { y: 110, kind: "touch" });
  pointer(window, "pointerup", { y: 110, kind: "touch" });
  expect(onReorderPins).toHaveBeenCalledWith(["b", "c", "a"]);
});

// --- Renaming and filing from the row menu ---------------------------------

const filed = (id: string, title: string, tags: string[]): SessionRow => ({
  id,
  title,
  tags,
});

test("the row menu offers renaming and the tags, above the rule", () => {
  renderDrawer({
    sessions: [filed("a", "A", ["api"])],
    onTitleSave: () => {},
    onTagsSave: () => {},
    onArchive: () => {},
  });
  fireEvent.click(screen.getByTestId("session-menu-a"));
  // Neither of the two starts a group: renaming and filing change what the row
  // says about itself, and the rule below them opens the pair that takes the
  // conversation out of the list.
  expect(screen.getByTestId("session-menu-rename-a").className).not.toContain(
    "starts-group",
  );
  expect(screen.getByTestId("session-menu-tags-a").className).not.toContain(
    "starts-group",
  );
  expect(screen.getByTestId("session-menu-archive-a").className).toContain(
    "starts-group",
  );
});

test("renaming a row edits the title in place and saves on Enter", () => {
  const onTitleSave = vi.fn();
  renderDrawer({ sessions: [row("a", "Old name")], onTitleSave });
  fireEvent.click(screen.getByTestId("session-menu-a"));
  fireEvent.click(screen.getByTestId("session-menu-rename-a"));

  const input = screen.getByTestId("session-rename-a") as HTMLInputElement;
  expect(input.value).toBe("Old name");
  fireEvent.change(input, { target: { value: "New name" } });
  fireEvent.keyDown(input, { key: "Enter" });
  expect(onTitleSave).toHaveBeenCalledWith("a", "New name");
  expect(screen.queryByTestId("session-rename-a")).toBeNull();
});

test("escape leaves a rename without writing anything", () => {
  const onTitleSave = vi.fn();
  renderDrawer({ sessions: [row("a", "Old name")], onTitleSave });
  fireEvent.click(screen.getByTestId("session-menu-a"));
  fireEvent.click(screen.getByTestId("session-menu-rename-a"));
  const input = screen.getByTestId("session-rename-a");
  fireEvent.change(input, { target: { value: "Something else" } });
  fireEvent.keyDown(input, { key: "Escape" });
  expect(onTitleSave).not.toHaveBeenCalled();
  expect(screen.queryByTestId("session-rename-a")).toBeNull();
});

test("the tag editor drops a label by its cross", () => {
  const onTagsSave = vi.fn();
  renderDrawer({
    sessions: [filed("a", "A", ["api", "ui"]), filed("b", "B", ["sessions"])],
    onTagsSave,
  });
  fireEvent.click(screen.getByTestId("session-menu-a"));
  fireEvent.click(screen.getByTestId("session-menu-tags-a"));

  fireEvent.click(screen.getByTestId("session-tag-remove-ui"));
  // The whole set goes, not a diff: PATCH replaces it.
  expect(onTagsSave).toHaveBeenCalledWith("a", ["api"]);
});

test("a label typed the way it reads is filed the way it is stored", () => {
  const onTagsSave = vi.fn();
  renderDrawer({ sessions: [filed("a", "A", ["api"])], onTagsSave });
  fireEvent.click(screen.getByTestId("session-menu-a"));
  fireEvent.click(screen.getByTestId("session-menu-tags-a"));

  const input = screen.getByTestId("session-tag-input");
  fireEvent.change(input, { target: { value: "Session Store" } });
  fireEvent.keyDown(input, { key: "Enter" });
  expect(onTagsSave).toHaveBeenCalledWith("a", ["api", "session-store"]);
});

test("a second gesture builds on the row the first one left", () => {
  // The shell takes the new set before its request answers (App.saveSessionTags
  // is optimistic), so the row the editor reads is already the edited one. The
  // drawer must pass that through: computing the next set from a stale row is
  // how the second gesture undoes the first.
  const onTagsSave = vi.fn();
  const { rerender } = renderDrawer({
    sessions: [filed("a", "A", ["api", "ui"])],
    onTagsSave,
  });
  fireEvent.click(screen.getByTestId("session-menu-a"));
  fireEvent.click(screen.getByTestId("session-menu-tags-a"));

  fireEvent.click(screen.getByTestId("session-tag-remove-ui"));
  expect(onTagsSave).toHaveBeenCalledWith("a", ["api"]);

  rerender(
    <SessionsSidebar
      sessionId="current"
      sessions={[filed("a", "A", ["api"])]}
      open
      onPick={() => {}}
      onDelete={() => Promise.resolve()}
      onTagsSave={onTagsSave}
      searchDraft=""
      onSearchDraftChange={() => {}}
      onSearchClear={() => {}}
      hasMore={false}
      loadingMore={false}
      onLoadMore={() => {}}
      now={NOW}
    />,
  );

  const input = screen.getByTestId("session-tag-input");
  fireEvent.change(input, { target: { value: "docs" } });
  fireEvent.keyDown(input, { key: "Enter" });
  expect(onTagsSave).toHaveBeenLastCalledWith("a", ["api", "docs"]);
});

test("a rename ended by Escape is not saved by the blur that follows", () => {
  const onTitleSave = vi.fn();
  renderDrawer({ sessions: [row("a", "Old name")], onTitleSave });
  fireEvent.click(screen.getByTestId("session-menu-a"));
  fireEvent.click(screen.getByTestId("session-menu-rename-a"));

  const input = screen.getByTestId("session-rename-a");
  fireEvent.change(input, { target: { value: "Discarded" } });
  fireEvent.keyDown(input, { key: "Escape" });
  fireEvent.blur(input);
  expect(onTitleSave).not.toHaveBeenCalled();
});

test("a rename saved by Enter is not saved a second time by the blur", () => {
  const onTitleSave = vi.fn();
  renderDrawer({ sessions: [row("a", "Old name")], onTitleSave });
  fireEvent.click(screen.getByTestId("session-menu-a"));
  fireEvent.click(screen.getByTestId("session-menu-rename-a"));

  const input = screen.getByTestId("session-rename-a");
  fireEvent.change(input, { target: { value: "New name" } });
  fireEvent.keyDown(input, { key: "Enter" });
  fireEvent.blur(input);
  expect(onTitleSave).toHaveBeenCalledTimes(1);
  expect(onTitleSave).toHaveBeenCalledWith("a", "New name");
});

test("the tag editor offers the labels this history already uses", () => {
  renderDrawer({
    sessions: [filed("a", "A", ["api"]), filed("b", "B", ["sessions", "api"])],
    onTagsSave: () => {},
  });
  fireEvent.click(screen.getByTestId("session-menu-a"));
  fireEvent.click(screen.getByTestId("session-menu-tags-a"));
  // Its own label is not offered again; the one from the other row is.
  expect(
    screen.getByTestId("session-tag-suggest-sessions"),
  ).toBeInTheDocument();
  expect(screen.queryByTestId("session-tag-suggest-api")).toBeNull();
});

test("a refused tag write says so in the editor", async () => {
  // The row is put back by the shell; the editor is where the operator is
  // looking, so that is where the refusal is said.
  const onTagsSave = vi.fn().mockResolvedValue(false);
  renderDrawer({ sessions: [filed("a", "A", ["api"])], onTagsSave });
  fireEvent.click(screen.getByTestId("session-menu-a"));
  fireEvent.click(screen.getByTestId("session-menu-tags-a"));

  fireEvent.click(screen.getByTestId("session-tag-remove-api"));
  expect(await screen.findByTestId("session-tag-error")).toBeInTheDocument();
});

test("a tag write that lands says nothing", async () => {
  const onTagsSave = vi.fn().mockResolvedValue(true);
  renderDrawer({ sessions: [filed("a", "A", ["api"])], onTagsSave });
  fireEvent.click(screen.getByTestId("session-menu-a"));
  fireEvent.click(screen.getByTestId("session-menu-tags-a"));

  fireEvent.click(screen.getByTestId("session-tag-remove-api"));
  await Promise.resolve();
  expect(screen.queryByTestId("session-tag-error")).toBeNull();
});

// A refused archive puts the row back; the reason is said on that row, since
// the list error at the top is out of view once History has been scrolled.
test("a row carries the error of the change it refused, and only that row", () => {
  renderDrawer({
    sessions: [row("a", "A"), row("b", "B")],
    rowErrors: { b: "The conversation was not archived" },
  });
  const note = screen.getByTestId("session-row-error-b");
  expect(note).toHaveTextContent("The conversation was not archived");
  expect(note).toHaveAttribute("role", "alert");
  expect(note).toHaveClass("session-row-error");
  expect(screen.getByTestId("session-row-b")).toContainElement(note);
  // Beside the link, not inside it: the link is named by the conversation,
  // not by the error of the last thing done to it.
  expect(note.closest("a")).toBeNull();
  expect(
    screen.getByTestId("session-row-b").querySelector("a")?.textContent,
  ).not.toContain("not archived");
  expect(screen.queryByTestId("session-row-error-a")).toBeNull();
  expect(screen.queryByTestId("sessions-error")).toBeNull();
});
