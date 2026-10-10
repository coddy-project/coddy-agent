import React from "react";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";
import { ConfirmProvider } from "./components/useConfirm";
import { initLocale } from "./i18n/i18n";

/**
 * The Scheduler's rail count is the scheduler's runs_active. A run is a turn
 * like any other, so the count follows the turn events of the events stream
 * whether or not the Scheduler is open: it used to be read only while the
 * Scheduler was open, and a run that ended with it closed left its count on
 * the rail until the page was reloaded.
 */

vi.mock("./chat/ChatScreen", () => ({
  ChatScreen: () => <div data-testid="chat-screen" />,
}));

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

class EventsStream {
  controller!: ReadableStreamDefaultController<Uint8Array>;
  response = new Response(
    new ReadableStream<Uint8Array>({
      start: (controller) => {
        this.controller = controller;
      },
    }),
    { headers: { "Content-Type": "text/event-stream" } },
  );
  frame(event: string, data: unknown) {
    this.controller.enqueue(
      new TextEncoder().encode(
        `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`,
      ),
    );
  }
}

let runsActive = 0;
let streams: EventsStream[] = [];
/** Answers of GET /coddy/scheduler/jobs a test holds back, in request order. */
let holdJobs: Array<Promise<void>> = [];

const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const path = String(input);
  if (path === "/coddy/events") {
    const stream = new EventsStream();
    streams.push(stream);
    return stream.response;
  }
  if (path.startsWith("/coddy/scheduler/jobs")) {
    // The count is read when the request is answered by the server, not when
    // the test lets the answer through.
    const body = {
      scheduler: { enabled: true, runs_active: runsActive },
      jobs: [],
    };
    const held = holdJobs.shift();
    if (held) await held;
    return json(body);
  }
  if (path === "/v1/models") return json({ data: [] });
  if (path.startsWith("/coddy/sessions?"))
    return json({ sessions: [], active_count: 0 });
  if (path.startsWith("/coddy/workspace/context")) {
    return json({ path: "/projects/demo", name: "demo", is_git_repo: false });
  }
  return json({}, 404);
});

const count = () => screen.queryByTestId("nav-scheduler-active-count");

beforeEach(() => {
  initLocale("en");
  localStorage.clear();
  history.replaceState(null, "", "/");
  runsActive = 0;
  streams = [];
  holdJobs = [];
  fetchMock.mockClear();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function mountApp() {
  return render(
    <ConfirmProvider>
      <App />
    </ConfirmProvider>,
  );
}

test("the Scheduler count shows a run without opening the Scheduler and leaves when it ends", async () => {
  runsActive = 1;
  mountApp();
  await waitFor(() => expect(count()).toHaveTextContent("1"));
  await waitFor(() => expect(streams.length).toBeGreaterThan(0));

  runsActive = 0;
  await act(async () => {
    streams[0]!.frame("turn_ended", { sessionId: "sess_run" });
  });
  await waitFor(() => expect(count()).toBeNull());
});

test("a late answer from an earlier read does not put an ended run back", async () => {
  mountApp();
  await waitFor(() => expect(streams.length).toBeGreaterThan(0));
  await waitFor(() =>
    expect(
      fetchMock.mock.calls.some(([input]) =>
        String(input).startsWith("/coddy/scheduler/jobs"),
      ),
    ).toBe(true),
  );

  let releaseStart: () => void = () => {};
  holdJobs.push(
    new Promise<void>((resolve) => {
      releaseStart = resolve;
    }),
  );
  runsActive = 1;
  await act(async () => {
    streams[0]!.frame("turn_started", { sessionId: "sess_run" });
  });
  runsActive = 0;
  await act(async () => {
    streams[0]!.frame("turn_ended", { sessionId: "sess_run" });
  });
  await act(async () => {
    releaseStart();
  });
  await new Promise((resolve) => setTimeout(resolve, 30));
  expect(count()).toBeNull();
});
