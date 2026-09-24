import React from "react";
import { act, cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";
import { fakeChannels, fakeWorkers } from "./chat/sharedServerEvents.fakes";
import { ConfirmProvider } from "./components/useConfirm";
import { initLocale } from "./i18n/i18n";

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

type ChatScreenProps = {
  sessionId: string | undefined;
  sessionArchived: boolean | undefined;
};
const chatScreenRenders: ChatScreenProps[] = [];
let chatOnUnarchive: (() => void) | undefined;

vi.mock("./chat/ChatScreen", () => ({
  ChatScreen: (props: {
    sessionId?: string;
    sessionArchived?: boolean;
    onUnarchiveSession?: () => void;
  }) => {
    chatScreenRenders.push({
      sessionId: props.sessionId,
      sessionArchived: props.sessionArchived,
    });
    chatOnUnarchive = props.onUnarchiveSession;
    return <div data-testid="chat-screen-stub" />;
  },
}));

type SidebarProps = {
  sessions?: Array<{ id: string; archived?: boolean }>;
  error?: string | null;
  loadingMore?: boolean;
  onArchiveFilterChange?: (value: "exclude" | "only" | "all") => void;
  rowErrors?: Readonly<Record<string, string>>;
  onArchive?: (id: string, archived: boolean) => void;
  onPick?: (id: string) => void;
  onClose?: () => void;
  onLoadMore?: () => void;
};
let sidebarOnArchive: SidebarProps["onArchive"];
let sidebarOnPick: SidebarProps["onPick"];
// The props of the latest sidebar render: what History shows right now.
let sidebar: SidebarProps = {};

vi.mock("./sessions/SessionsSidebar", () => ({
  SessionsSidebar: (props: SidebarProps) => {
    sidebarOnArchive = props.onArchive;
    sidebarOnPick = props.onPick;
    sidebar = props;
    return <div data-testid="sessions-sidebar-stub" />;
  },
}));

// The fake backend holds the archive flag per session id; the messages
// endpoint reports the value as of the moment the read was issued, the PATCH
// flips it - like the real server.
const archivedById = new Map<string, boolean>();
// Deferred gates: a flagged queue resolves only when the test releases it.
let holdMessages = false;
let holdArchivePatch = false;
const messageGate: Array<() => void> = [];
const patchGate: Array<() => void> = [];
// The stored history the listing pages through, in listing order. Like the
// real server, a page is an offset into the rows the filter keeps, so a row
// archived since the last page shifts the ones after it up by one.
let storedIds: string[] = [];
// The PATCH reaches the disk only when the test releases it, so a listing
// read while it is in flight still carries the row.
let applyPatchOnRelease = false;
let failArchivePatch = false;
// Later pages (a request with a cursor) wait for the test, or fail outright.
let holdCursorPages = false;
let throwCursorPages = false;
const pageGate: Array<() => void> = [];

function listPage(url: string) {
  const q = new URL(url, "http://coddy.test").searchParams;
  const filter = q.get("archived") ?? "exclude";
  const limit = Number(q.get("limit") ?? "30");
  const offset = Number(q.get("cursor") ?? "0");
  const rows = storedIds
    .map((id) => ({
      id,
      title: id,
      archived: archivedById.get(id) ?? false,
    }))
    .filter((row) =>
      filter === "all" ? true : filter === "only" ? row.archived : !row.archived,
    );
  const end = offset + limit;
  return json({
    sessions: rows.slice(offset, end),
    nextCursor: end < rows.length ? String(end) : null,
    hasMore: end < rows.length,
  });
}

function listCalls(): string[] {
  return fetchStub.mock.calls
    .map(([input]) => (typeof input === "string" ? input : String(input)))
    .filter((url) => url.startsWith("/coddy/sessions?"));
}

function shownIds(): string[] {
  return (sidebar.sessions ?? []).map((row) => row.id);
}

const fetchStub = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url =
    typeof input === "string"
      ? input
      : input instanceof URL
        ? input.href
        : input.url;
  const msgMatch = url.match(/\/coddy\/sessions\/([^/?]+)\/messages/);
  if (msgMatch) {
    const sid = decodeURIComponent(msgMatch[1] ?? "");
    // The flag is read at issue time, so a deferred reply carries the older
    // value even when the PATCH has moved the flag since.
    const archived = archivedById.get(sid) ?? false;
    const answer = () =>
      json({
        messages: [],
        session_id: sid,
        title: "",
        archived,
      });
    if (holdMessages) {
      return new Promise<Response>((resolve) => {
        messageGate.push(() => resolve(answer()));
      });
    }
    return answer();
  }
  if (url.startsWith("/coddy/sessions?")) {
    if (url.includes("cursor=")) {
      if (throwCursorPages) throw new TypeError("Failed to fetch");
      if (holdCursorPages) {
        // The page is read when it is answered, like the real server's.
        return new Promise<Response>((resolve) => {
          pageGate.push(() => resolve(listPage(url)));
        });
      }
    }
    return listPage(url);
  }
  if ((init?.method ?? "GET") === "PATCH") {
    const patchMatch = url.match(/\/coddy\/sessions\/([^/?]+)/);
    const body = JSON.parse(String(init?.body ?? "{}")) as {
      archived?: boolean;
    };
    if (patchMatch && body.archived !== undefined) {
      const sid = decodeURIComponent(patchMatch[1] ?? "");
      const archived = body.archived;
      const answer = () => {
        if (failArchivePatch) return json({ error: "disk full" }, 500);
        if (applyPatchOnRelease) archivedById.set(sid, archived);
        return json({ ok: true });
      };
      if (!applyPatchOnRelease && !failArchivePatch) {
        archivedById.set(sid, archived);
      }
      if (holdArchivePatch) {
        return new Promise<Response>((resolve) => {
          patchGate.push(() => resolve(answer()));
        });
      }
      return answer();
    }
    return json({ ok: true });
  }
  return json({});
});

function releaseMessages() {
  messageGate.splice(0).forEach((release) => release());
}

function releaseArchivePatch() {
  patchGate.splice(0).forEach((release) => release());
}

beforeEach(() => {
  initLocale("en");
  // History remembers its filters in cookies; a test that switched to the
  // archive must not open the next one there.
  for (const pair of document.cookie.split(";")) {
    const name = pair.split("=")[0]?.trim();
    if (name) document.cookie = `${name}=; Max-Age=0; path=/`;
  }
  chatScreenRenders.length = 0;
  chatOnUnarchive = undefined;
  sidebarOnArchive = undefined;
  sidebarOnPick = undefined;
  sidebar = {};
  storedIds = [];
  applyPatchOnRelease = false;
  failArchivePatch = false;
  holdCursorPages = false;
  throwCursorPages = false;
  pageGate.length = 0;
  archivedById.clear();
  holdMessages = false;
  holdArchivePatch = false;
  messageGate.length = 0;
  patchGate.length = 0;
  fetchStub.mockClear();
  fakeChannels();
  fakeWorkers(fetchStub);
  vi.stubGlobal("fetch", fetchStub);
  window.location.hash = "#/s/sess_x";
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.location.hash = "";
});

function renderApp() {
  return render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
}

async function archiveFromSidebar(id: string, archived: boolean) {
  // The drawer is what mounts the sidebar and hands it onArchive.
  const historyBtn = document.querySelector(
    '[data-testid="nav-history"]',
  ) as HTMLElement;
  expect(historyBtn).toBeTruthy();
  await act(async () => {
    historyBtn.click();
  });
  expect(sidebarOnArchive).toBeDefined();
  await act(async () => {
    sidebarOnArchive?.(id, archived);
  });
}

test("archiving the open session swaps the composer for the archived notice", async () => {
  renderApp();
  await waitFor(() =>
    expect(chatScreenRenders.at(-1)?.sessionArchived).toBe(false),
  );

  await archiveFromSidebar("sess_x", true);

  await waitFor(() =>
    expect(chatScreenRenders.at(-1)?.sessionArchived).toBe(true),
  );
});

test("unarchiving the open session brings the composer back", async () => {
  archivedById.set("sess_x", true);
  renderApp();
  await waitFor(() =>
    expect(chatScreenRenders.at(-1)?.sessionArchived).toBe(true),
  );

  await archiveFromSidebar("sess_x", false);

  await waitFor(() =>
    expect(chatScreenRenders.at(-1)?.sessionArchived).toBe(false),
  );
});

test("archiving a different session leaves the open composer alone", async () => {
  renderApp();
  await waitFor(() =>
    expect(chatScreenRenders.at(-1)?.sessionArchived).toBe(false),
  );

  await archiveFromSidebar("sess_other", true);

  // Let the PATCH and the listing reload settle; the flag must stay false on
  // every render the action produced.
  await waitFor(() =>
    expect(
      fetchStub.mock.calls.some(
        ([, init]) => (init?.method ?? "GET") === "PATCH",
      ),
    ).toBe(true),
  );
  await act(async () => {});
  expect(chatScreenRenders.every((r) => !r.sessionArchived)).toBe(true);
});

test("an archive PATCH that lands after the viewer moved on marks no session", async () => {
  holdArchivePatch = true;
  renderApp();
  await waitFor(() =>
    expect(chatScreenRenders.at(-1)?.sessionArchived).toBe(false),
  );

  await archiveFromSidebar("sess_x", true);
  // The PATCH for sess_x is still in flight; the viewer moves to another chat.
  expect(sidebarOnPick).toBeDefined();
  await act(async () => {
    sidebarOnPick?.("sess_y");
  });
  await waitFor(() =>
    expect(chatScreenRenders.at(-1)?.sessionId).toBe("sess_y"),
  );

  releaseArchivePatch();
  await act(async () => {});

  // sess_y must not inherit a flag that belonged to sess_x.
  expect(chatScreenRenders.at(-1)?.sessionArchived).toBe(false);
  expect(
    chatScreenRenders.filter((r) => r.sessionId === "sess_y").every((r) => !r.sessionArchived),
  ).toBe(true);
});

test("a transcript read issued before the archive PATCH cannot put the flag back", async () => {
  holdMessages = true; // the boot loadMessages for sess_x stays in flight
  renderApp();

  await archiveFromSidebar("sess_x", true);
  await waitFor(() =>
    expect(chatScreenRenders.at(-1)?.sessionArchived).toBe(true),
  );

  // The stale read now lands carrying archived:false, the flag from before
  // the write; the PATCH's answer is newer and must stay on screen.
  holdMessages = false;
  releaseMessages();
  await act(async () => {});

  expect(chatScreenRenders.at(-1)?.sessionArchived).toBe(true);
});

test("the notice's unarchive control brings the composer back", async () => {
  archivedById.set("sess_x", true);
  renderApp();
  await waitFor(() =>
    expect(chatScreenRenders.at(-1)?.sessionArchived).toBe(true),
  );
  expect(chatOnUnarchive).toBeDefined();

  await act(async () => {
    chatOnUnarchive?.();
  });

  await waitFor(() =>
    expect(chatScreenRenders.at(-1)?.sessionArchived).toBe(false),
  );
});

function sessionIds(n: number): string[] {
  return Array.from({ length: n }, (_, i) => `sess_${String(i).padStart(2, "0")}`);
}

async function openHistory() {
  const historyBtn = document.querySelector(
    '[data-testid="nav-history"]',
  ) as HTMLElement;
  await act(async () => {
    historyBtn.click();
  });
  await waitFor(() => expect(shownIds().length).toBeGreaterThan(0));
}

async function loadNextPage() {
  const before = shownIds().length;
  await act(async () => {
    sidebar.onLoadMore?.();
  });
  await waitFor(() => expect(shownIds().length).toBeGreaterThan(before));
}

// Archiving from History used to wait for the PATCH and then read the list
// again from its first page: the rows scrolling had loaded were gone, and so
// was the place in the list the next conversation to archive sat at.
test("archiving from History takes the row out at once and keeps the rows scrolling loaded", async () => {
  storedIds = sessionIds(45);
  holdArchivePatch = true;
  renderApp();
  await openHistory();
  await loadNextPage();
  expect(shownIds()).toHaveLength(45);
  const listsBefore = listCalls().length;

  await act(async () => {
    sidebar.onArchive?.("sess_40", true);
  });

  // Gone before the server has answered.
  expect(patchGate).toHaveLength(1);
  expect(shownIds()).not.toContain("sess_40");
  expect(shownIds()).toHaveLength(44);

  releaseArchivePatch();
  await act(async () => {});

  // The list is not read again: the second page is still on screen.
  expect(listCalls()).toHaveLength(listsBefore);
  expect(shownIds()).toHaveLength(44);
  expect(shownIds()).toContain("sess_44");
  expect(shownIds()).not.toContain("sess_40");
});

test("a refused archive puts the row back where it was and says so on it", async () => {
  storedIds = sessionIds(12);
  holdArchivePatch = true;
  failArchivePatch = true;
  renderApp();
  await openHistory();
  const order = shownIds();

  await act(async () => {
    sidebar.onArchive?.("sess_05", true);
  });
  expect(shownIds()).not.toContain("sess_05");

  releaseArchivePatch();
  await waitFor(() => expect(shownIds()).toEqual(order));
  expect(sidebar.rowErrors?.["sess_05"]).toBe("The conversation was not archived");
  expect((sidebar.sessions ?? []).find((row) => row.id === "sess_05")?.archived).toBeFalsy();
  // Trying again takes the note away with the row.
  failArchivePatch = false;
  await act(async () => {
    sidebar.onArchive?.("sess_05", true);
  });
  expect(sidebar.rowErrors?.["sess_05"]).toBeUndefined();
  expect(shownIds()).not.toContain("sess_05");
  releaseArchivePatch();
  await act(async () => {});
});

// The listing pages by offset: a row archived out of the loaded part moves the
// rest of the history up by one, so the next page starts one row earlier or a
// conversation falls between the two pages.
test("the page after an archive starts where the list now ends, so nothing is skipped", async () => {
  storedIds = sessionIds(45);
  renderApp();
  await openHistory();
  expect(shownIds()).toHaveLength(30);

  await act(async () => {
    sidebar.onArchive?.("sess_03", true);
  });
  await waitFor(() => expect(archivedById.get("sess_03")).toBe(true));
  await act(async () => {});
  expect(shownIds()).toHaveLength(29);

  await loadNextPage();
  expect(listCalls().at(-1)).toContain("cursor=29");
  expect(shownIds()).toEqual(sessionIds(45).filter((id) => id !== "sess_03"));
});

test("a listing read while the archive is in flight does not bring the row back", async () => {
  storedIds = sessionIds(12);
  holdArchivePatch = true;
  applyPatchOnRelease = true;
  renderApp();
  await openHistory();

  await act(async () => {
    sidebar.onArchive?.("sess_05", true);
  });
  expect(shownIds()).not.toContain("sess_05");

  // Closing and opening History reads the first page again while the server
  // still lists the row as it was.
  const listsBefore = listCalls().length;
  await act(async () => {
    sidebar.onClose?.();
  });
  await openHistory();
  await waitFor(() => expect(listCalls().length).toBeGreaterThan(listsBefore));
  await act(async () => {});
  expect(shownIds()).not.toContain("sess_05");

  releaseArchivePatch();
  await act(async () => {});
  expect(shownIds()).not.toContain("sess_05");
  expect(shownIds()).toHaveLength(11);
});

// A page asked for while the PATCH is still out may be answered after the
// server has already taken the row out of its listing: counted from the old
// offset, that page would start one row too far and a conversation would fall
// between two pages. It starts a row earlier instead; the row it fetches twice
// is dropped by id.
test("a page loaded while an archive is in flight skips no conversation", async () => {
  storedIds = sessionIds(45);
  holdArchivePatch = true;
  renderApp();
  await openHistory();
  expect(shownIds()).toHaveLength(30);

  await act(async () => {
    sidebar.onArchive?.("sess_03", true);
  });
  // The fake server has the flag from the moment the PATCH arrived.
  await loadNextPage();
  expect(listCalls().at(-1)).toContain("cursor=29");

  releaseArchivePatch();
  await act(async () => {});
  expect(shownIds()).toEqual(sessionIds(45).filter((id) => id !== "sess_03"));
});

// The operator switched to the archive while the PATCH was out; when it is
// refused, the row it would put back belongs to a listing no longer on screen.
test("a refused archive does not put the row into a list of another filter", async () => {
  storedIds = sessionIds(12);
  archivedById.set("sess_10", true);
  holdArchivePatch = true;
  failArchivePatch = true;
  renderApp();
  await openHistory();

  await act(async () => {
    sidebar.onArchive?.("sess_05", true);
  });
  await act(async () => {
    sidebar.onArchiveFilterChange?.("only");
  });
  await waitFor(() => expect(shownIds()).toEqual(["sess_10"]));

  releaseArchivePatch();
  await act(async () => {});
  expect(shownIds()).toEqual(["sess_10"]);
  expect(sidebar.rowErrors?.["sess_05"]).toBeUndefined();
  expect(sidebar.error).toBe("The conversation was not archived");
});

// A page asked for before the list was read again from the top belongs to
// the listing that read replaced: appended to the new one, it would mix in
// rows of another filter (or of an older order).
test("a page that comes back after the list was read again is dropped", async () => {
  storedIds = sessionIds(45);
  archivedById.set("sess_40", true);
  renderApp();
  await openHistory();
  holdCursorPages = true;
  await act(async () => {
    sidebar.onLoadMore?.();
  });
  expect(pageGate).toHaveLength(1);

  await act(async () => {
    sidebar.onArchiveFilterChange?.("only");
  });
  await waitFor(() => expect(shownIds()).toEqual(["sess_40"]));

  await act(async () => {
    pageGate.splice(0).forEach((release) => release());
  });
  await act(async () => {});
  expect(shownIds()).toEqual(["sess_40"]);
});

test("a page that fails to load leaves History able to load it again", async () => {
  storedIds = sessionIds(45);
  throwCursorPages = true;
  renderApp();
  await openHistory();

  await act(async () => {
    sidebar.onLoadMore?.();
  });
  await waitFor(() => expect(sidebar.error).toBe("Backend is unavailable (0)"));
  expect(sidebar.loadingMore).toBe(false);

  throwCursorPages = false;
  await loadNextPage();
  expect(shownIds()).toHaveLength(45);
});
