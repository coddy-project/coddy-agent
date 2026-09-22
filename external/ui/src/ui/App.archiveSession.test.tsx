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
  onArchive?: (id: string, archived: boolean) => void;
  onPick?: (id: string) => void;
};
let sidebarOnArchive: SidebarProps["onArchive"];
let sidebarOnPick: SidebarProps["onPick"];

vi.mock("./sessions/SessionsSidebar", () => ({
  SessionsSidebar: (props: SidebarProps) => {
    sidebarOnArchive = props.onArchive;
    sidebarOnPick = props.onPick;
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
  if ((init?.method ?? "GET") === "PATCH") {
    const patchMatch = url.match(/\/coddy\/sessions\/([^/?]+)/);
    const body = JSON.parse(String(init?.body ?? "{}")) as {
      archived?: boolean;
    };
    if (patchMatch && body.archived !== undefined) {
      const sid = decodeURIComponent(patchMatch[1] ?? "");
      archivedById.set(sid, body.archived);
      if (holdArchivePatch) {
        return new Promise<Response>((resolve) => {
          patchGate.push(() => resolve(json({ ok: true })));
        });
      }
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
  chatScreenRenders.length = 0;
  chatOnUnarchive = undefined;
  sidebarOnArchive = undefined;
  sidebarOnPick = undefined;
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
