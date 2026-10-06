import React from "react";
import { act, cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";
import type { TranscriptItem } from "./chat/types";
import { ConfirmProvider } from "./components/useConfirm";
import { initLocale } from "./i18n/i18n";

/**
 * Editing a sent message is a state the operator can see and leave: the pencil
 * remembers the draft it replaced, cancelling puts that draft back, and once
 * the edit is sent the server's rewindUndo lets the conversation be restored.
 */

const SID = "sess_edit";

type Msg = Record<string, unknown>;

const ORIGINAL: Msg[] = [
  { role: "user", content: "first question" },
  { role: "assistant", content: "first answer" },
  { role: "user", content: "second question" },
  { role: "assistant", content: "second answer" },
];

let msgs: Msg[] = [...ORIGINAL];
let rewindUndo: { userMessageIndex: number } | null = null;

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const fetchMock = vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
  const url = new URL(String(input), "http://localhost");
  const path = url.pathname;
  if (path === "/coddy/events")
    return new Response(new ReadableStream<Uint8Array>(), {
      headers: { "Content-Type": "text/event-stream" },
    });
  if (path === "/v1/models") return json({ data: [] });
  if (path.startsWith("/coddy/sessions") && !path.includes(SID))
    return json({ sessions: [{ id: SID, title: "Edit" }] });
  if (path === `/coddy/sessions/${SID}/messages`) {
    return json({
      messages: msgs,
      window: { offset: 0, total: msgs.length, turnsBefore: 0, userRowsBefore: 0 },
      ...(rewindUndo ? { rewindUndo } : {}),
    });
  }
  if (path === `/coddy/sessions/${SID}/rewind`) {
    msgs = msgs.slice(0, 2);
    rewindUndo = { userMessageIndex: 1 };
    return json({ object: "coddy.session_rewound", sessionId: SID, messagesRev: 3 });
  }
  if (path === `/coddy/sessions/${SID}/rewind/undo`) {
    msgs = [...ORIGINAL];
    rewindUndo = null;
    return json({ object: "coddy.session_rewind_undone", sessionId: SID, messagesRev: 9 });
  }
  if (path === `/coddy/sessions/${SID}/tool-calls`) return json({ toolCalls: [] });
  if (path === `/coddy/sessions/${SID}/activity`)
    return json({ sessionId: SID, turnActive: false });
  if (path === `/coddy/sessions/${SID}/background-tasks`)
    return json({ data: [], running: 0 });
  if (path === `/coddy/sessions/${SID}/stats`) return json({ stats: {} });
  if (path === `/coddy/sessions/${SID}/queue`) return json({ messages: [] });
  if (path === "/coddy/workspace/context")
    return json({ cwd: "/workspace", is_git_repo: false });
  return json({});
});

type ChatProps = {
  sessionId: string;
  items: TranscriptItem[];
  draft: string;
  onDraftChange: (v: string) => void;
  onEdit?: (content: string, userMsgIdx: number) => void;
  onSend?: (text: string, files?: File[]) => void;
  editingUserMsgIdx?: number | null;
  editingSnippet?: string;
  onCancelEdit?: () => void;
  rewindUndoUserMsgIdx?: number | null;
  rewindUndoBanner?: boolean;
  onUndoEdit?: () => void;
  onDismissRewindUndo?: () => void;
};
let chat: ChatProps | null = null;

vi.mock("./chat/ChatScreen", () => ({
  ChatScreen: (props: ChatProps) => {
    chat = props;
    return <div data-testid="chat-screen-stub" />;
  },
}));

const prompts = () =>
  (chat?.items ?? [])
    .filter((it) => it.type === "user_message")
    .map((it) => (it as { content: string }).content);

const calledPath = (p: string) =>
  fetchMock.mock.calls.some((c) => new URL(String(c[0]), "http://localhost").pathname === p);

beforeEach(() => {
  initLocale("en");
  localStorage.clear();
  msgs = [...ORIGINAL];
  rewindUndo = null;
  chat = null;
  history.replaceState(null, "", `/#/s/${SID}`);
  fetchMock.mockClear();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function renderApp() {
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
}

async function openSession() {
  renderApp();
  await waitFor(() => expect(prompts()).toEqual(["first question", "second question"]));
}

test("the pencil names the message being edited and cancelling restores the draft it replaced", async () => {
  await openSession();
  await act(async () => chat!.onDraftChange("half-written follow-up"));
  expect(chat!.editingUserMsgIdx ?? null).toBeNull();

  await act(async () => chat!.onEdit!("second question", 1));
  expect(chat!.draft).toBe("second question");
  expect(chat!.editingUserMsgIdx).toBe(1);
  expect(chat!.editingSnippet).toBe("second question");

  // A second pencil while editing switches the message, but the draft to come
  // back to is still the one written before the first.
  await act(async () => chat!.onEdit!("first question", 0));
  expect(chat!.editingUserMsgIdx).toBe(0);

  await act(async () => chat!.onCancelEdit!());
  expect(chat!.draft).toBe("half-written follow-up");
  expect(chat!.editingUserMsgIdx ?? null).toBeNull();
  expect(calledPath(`/coddy/sessions/${SID}/rewind`)).toBe(false);
});

test("an undoable rewind is offered on its prompt, and Undo restores the conversation", async () => {
  // The state after an edit of the second prompt was sent and answered.
  msgs = [
    ORIGINAL[0]!,
    ORIGINAL[1]!,
    { role: "user", content: "second question, edited" },
    { role: "assistant", content: "edited answer" },
  ];
  rewindUndo = { userMessageIndex: 1 };
  renderApp();
  await waitFor(() =>
    expect(prompts()).toEqual(["first question", "second question, edited"]),
  );
  await waitFor(() => expect(chat!.rewindUndoUserMsgIdx).toBe(1));
  expect(chat!.rewindUndoBanner).toBe(true);

  await act(async () => chat!.onUndoEdit!());
  await waitFor(() => expect(prompts()).toEqual(["first question", "second question"]));
  expect(calledPath(`/coddy/sessions/${SID}/rewind/undo`)).toBe(true);
  await waitFor(() => expect(chat!.rewindUndoUserMsgIdx ?? null).toBeNull());
  expect(chat!.rewindUndoBanner).toBe(false);
});

test("dismissing the banner keeps Undo on the prompt", async () => {
  msgs = [
    ORIGINAL[0]!,
    ORIGINAL[1]!,
    { role: "user", content: "second question, edited" },
    { role: "assistant", content: "edited answer" },
  ];
  rewindUndo = { userMessageIndex: 1 };
  renderApp();
  await waitFor(() => expect(chat?.rewindUndoUserMsgIdx).toBe(1));
  await act(async () => chat!.onDismissRewindUndo!());
  expect(chat!.rewindUndoBanner).toBe(false);
  expect(chat!.rewindUndoUserMsgIdx).toBe(1);
});

test("sending the edit gives back the draft the pencil set aside", async () => {
  await openSession();
  await act(async () => chat!.onDraftChange("half-written follow-up"));
  await act(async () => chat!.onEdit!("second question", 1));
  await act(async () => chat!.onDraftChange("second question, edited"));
  await act(async () => chat!.onSend!("second question, edited"));
  await waitFor(() => expect(calledPath(`/coddy/sessions/${SID}/rewind`)).toBe(true));
  await waitFor(() => expect(chat!.editingUserMsgIdx ?? null).toBeNull());
  expect(chat!.draft).toBe("half-written follow-up");
});

test("two clicks on Undo post one undo", async () => {
  msgs = [
    ORIGINAL[0]!,
    ORIGINAL[1]!,
    { role: "user", content: "second question, edited" },
    { role: "assistant", content: "edited answer" },
  ];
  rewindUndo = { userMessageIndex: 1 };
  renderApp();
  await waitFor(() => expect(chat?.rewindUndoUserMsgIdx).toBe(1));
  await act(async () => {
    chat!.onUndoEdit!();
    chat!.onUndoEdit!();
  });
  await waitFor(() => expect(prompts()).toEqual(["first question", "second question"]));
  const undoPosts = fetchMock.mock.calls.filter(
    (c) => new URL(String(c[0]), "http://localhost").pathname === `/coddy/sessions/${SID}/rewind/undo`,
  );
  expect(undoPosts).toHaveLength(1);
});

test("a banner put away comes back for the next edit of the same prompt", async () => {
  msgs = [
    ORIGINAL[0]!,
    ORIGINAL[1]!,
    { role: "user", content: "second question, edited" },
    { role: "assistant", content: "edited answer" },
  ];
  rewindUndo = { userMessageIndex: 1 };
  renderApp();
  await waitFor(() => expect(chat?.rewindUndoUserMsgIdx).toBe(1));
  await act(async () => chat!.onDismissRewindUndo!());
  expect(chat!.rewindUndoBanner).toBe(false);
  await act(async () => chat!.onUndoEdit!());
  await waitFor(() => expect(prompts()).toEqual(["first question", "second question"]));

  await act(async () => chat!.onEdit!("second question", 1));
  await act(async () => chat!.onSend!("second question, again"));
  await waitFor(() => expect(chat!.rewindUndoUserMsgIdx).toBe(1));
  expect(chat!.rewindUndoBanner).toBe(true);
});
