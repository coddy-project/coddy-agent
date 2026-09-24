import React from "react";
import { act, cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";
import type { TranscriptItem } from "./chat/types";
import { ConfirmProvider } from "./components/useConfirm";
import { initLocale } from "./i18n/i18n";

/**
 * A long session is held as a window over its history (issue #338): it opens
 * on the newest page, a reload re-reads the window it holds, the page above is
 * read when the reader scrolls up to it and put in front, and what was read on
 * the way up is let go once the reader is back at the newest message.
 */

const SID = "sess_long";
const TURNS = 60;

type Msg = Record<string, unknown>;

/** Sixty turns of a prompt, a tool step and an answer: 240 messages. */
function buildHistory(): Msg[] {
  const out: Msg[] = [];
  for (let i = 1; i <= TURNS; i++) {
    out.push({ role: "user", content: `prompt ${i}` });
    out.push({
      role: "assistant",
      content: "",
      tool_calls: [
        { id: `call_${i}`, function: { name: "read_file", arguments: "{}" } },
      ],
    });
    out.push({ role: "tool", content: `result ${i}`, tool_call_id: `call_${i}` });
    out.push({ role: "assistant", content: `answer ${i}` });
  }
  return out;
}

let msgs: Msg[] = buildHistory();

/** The window a query names, as session.PageMessages computes it. */
function pageOf(query: URLSearchParams): { offset: number; end: number } {
  const total = msgs.length;
  const stepStart = (i: number) => {
    while (i > 0 && i < total && msgs[i]!.role === "tool") i--;
    return i;
  };
  const before = query.has("before") ? Number(query.get("before")) : -1;
  const end = before >= 0 && before < total ? stepStart(before) : total;
  if (query.has("from")) {
    return { offset: stepStart(Math.min(Number(query.get("from")), end)), end };
  }
  const limit = Number(query.get("limit") || 0);
  if (!limit) return { offset: 0, end };
  const cut = end - limit;
  if (cut <= 0) return { offset: 0, end };
  for (let i = cut; i >= 0 && i >= cut - Math.floor(limit / 2); i--) {
    if (msgs[i]!.role === "user") return { offset: i, end };
  }
  return { offset: stepStart(cut), end };
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const reads: string[] = [];
/** A read held back until a test lets it through, keyed by its query. */
const held = new Map<string, () => void>();
let holdQuery: string | null = null;

const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = new URL(String(input), "http://localhost");
  const path = url.pathname;
  if (path === "/coddy/events")
    return new Response(new ReadableStream<Uint8Array>(), {
      headers: { "Content-Type": "text/event-stream" },
    });
  if (path === "/v1/models") return json({ data: [] });
  if (path.startsWith("/coddy/sessions") && !path.includes(SID))
    return json({ sessions: [{ id: SID, title: "Long" }] });
  if (path === `/coddy/sessions/${SID}/messages`) {
    reads.push(url.search);
    if (holdQuery !== null && url.search === holdQuery) {
      await new Promise<void>((resolve) => held.set(url.search, resolve));
    }
    const { offset, end } = pageOf(url.searchParams);
    let turnsBefore = 0;
    for (const m of msgs.slice(0, offset)) if (m.role === "user") turnsBefore++;
    return json({
      messages: msgs.slice(offset, end),
      window: {
        offset,
        total: msgs.length,
        turnsBefore,
        userRowsBefore: turnsBefore,
      },
    });
  }
  if (path === `/coddy/sessions/${SID}/tool-calls`) return json({ toolCalls: [] });
  if (path === `/coddy/sessions/${SID}/rewind`) {
    // Cut the history at the prompt named, the way the server does.
    const body = JSON.parse(String(init?.body ?? "{}"));
    let seen = -1;
    const at = msgs.findIndex((m) => m.role === "user" && ++seen === body.userMessageIndex);
    if (at >= 0) msgs = msgs.slice(0, at);
    return json({ object: "coddy.session_rewound", sessionId: SID, messagesRev: 2 });
  }
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
  userMsgIndexBase?: number;
  transcriptHasOlder?: boolean;
  olderTranscriptLoad?: string;
  onLoadOlderTranscript?: () => void;
  onReaderAtTailChange?: (atTail: boolean) => void;
  onEdit?: (content: string, userMsgIdx: number) => void;
  onSend?: (text: string) => void;
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

beforeEach(() => {
  initLocale("en");
  localStorage.clear();
  msgs = buildHistory();
  reads.length = 0;
  held.clear();
  holdQuery = null;
  chat = null;
  history.replaceState(null, "", `/#/s/${SID}`);
  fetchMock.mockClear();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

async function openLongSession() {
  render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
  await waitFor(() => expect(prompts().length).toBeGreaterThan(0));
}

test("a long session opens on its newest page, numbered as the whole history", async () => {
  await openLongSession();
  expect(reads[0]).toBe("?limit=60");
  expect(prompts()[0]).toBe("prompt 46");
  expect(prompts().at(-1)).toBe(`prompt ${TURNS}`);
  expect(chat?.transcriptHasOlder).toBe(true);
  // An edit of "prompt 46" rewinds the 46th prompt: index 45.
  expect(chat?.userMsgIndexBase).toBe(45);
  expect(
    fetchMock.mock.calls.some((c) =>
      String(c[0]).endsWith(`/tool-calls?from=180&to=240`),
    ),
  ).toBe(true);
});

test("the page above arrives in front, and returning to the newest message lets it go", async () => {
  await openLongSession();
  await act(async () => chat!.onLoadOlderTranscript!());
  await waitFor(() => expect(prompts()[0]).toBe("prompt 26"));
  expect(reads).toContain("?limit=80&before=180");
  expect(prompts()).toHaveLength(35);
  expect(chat?.userMsgIndexBase).toBe(25);
  expect(new Set(chat!.items.map((it) => it.id)).size).toBe(chat!.items.length);

  // The next page joins the one before it.
  await act(async () => chat!.onLoadOlderTranscript!());
  await waitFor(() => expect(prompts()[0]).toBe("prompt 6"));
  expect(reads).toContain("?limit=80&before=100");

  await act(async () => chat!.onReaderAtTailChange!(true));
  await waitFor(() => expect(prompts()[0]).toBe("prompt 46"));
  expect(chat?.userMsgIndexBase).toBe(45);
  expect(chat?.transcriptHasOlder).toBe(true);
});

test("a window started over while a page above was read does not take that page", async () => {
  await openLongSession();
  holdQuery = "?limit=80&before=180";
  let loaded: Promise<void> | undefined;
  act(() => {
    loaded = Promise.resolve(chat!.onLoadOlderTranscript!());
  });
  await waitFor(() => expect(held.has("?limit=80&before=180")).toBe(true));
  // Meanwhile a hundred turns ran and the reader came back to the chat: the
  // same window, re-read from message 180, now holds 260 messages.
  for (let i = 1; i <= 100; i++) {
    msgs.push({ role: "user", content: `later ${i}` });
    msgs.push({ role: "assistant", content: `later answer ${i}` });
  }
  await act(async () => {
    history.replaceState(null, "", "/#/");
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });
  await act(async () => {
    history.replaceState(null, "", `/#/s/${SID}`);
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });
  await waitFor(() => expect(prompts().at(-1)).toBe("later 100"));
  expect(reads.at(-1)).toBe("?from=180");
  // At the newest message and idle, a window that large starts over from the
  // newest page.
  await act(async () => chat!.onReaderAtTailChange!(true));
  await waitFor(() => expect(prompts()[0]).not.toBe("prompt 46"));
  expect(reads.at(-1)).toBe("?limit=60");
  held.get("?limit=80&before=180")!();
  await act(async () => {
    await loaded;
  });
  await new Promise((r) => setTimeout(r, 20));
  // The page read against the old window borders nothing on screen now, and
  // the control is ready to ask for the page above the new one.
  expect(prompts()).not.toContain("prompt 26");
  expect(chat?.olderTranscriptLoad).toBe("idle");
  expect(prompts().at(-1)).toBe("later 100");
  expect(chat?.userMsgIndexBase).toBe(TURNS + 100 - prompts().length);
});

test("a page above that a rewind made moot leaves the control ready, not loading", async () => {
  await openLongSession();
  holdQuery = "?limit=80&before=180";
  let loaded: Promise<void> | undefined;
  act(() => {
    loaded = Promise.resolve(chat!.onLoadOlderTranscript!());
  });
  await waitFor(() => expect(held.has("?limit=80&before=180")).toBe(true));
  await waitFor(() => expect(chat?.olderTranscriptLoad).toBe("loading"));
  // Meanwhile the reader edits the last prompt: the history is cut there and
  // the window starts over from the newest page.
  await act(async () => chat!.onEdit!(`prompt ${TURNS}`, TURNS - 1));
  await act(async () => chat!.onSend!(`prompt ${TURNS}, edited`));
  // The kept prefix is read from its newest page, then the edit is sent.
  await waitFor(() =>
    expect(reads.filter((q) => q === "?limit=60").length).toBeGreaterThanOrEqual(2),
  );
  await waitFor(() => expect(prompts()).toContain(`prompt ${TURNS - 1}`));
  held.get("?limit=80&before=180")!();
  await act(async () => {
    await loaded;
  });
  await waitFor(() => expect(chat?.olderTranscriptLoad).toBe("idle"));
  expect(prompts()).not.toContain("prompt 26");
});
