import React from "react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { SessionChangesCard } from "./SessionChangesCard";
import { setSessionChangesEnabled } from "../chat/sessionChangesConfig";
import {
  emitChangesSettled,
  resetChangesBusForTests,
  setChangesBusClockForTests,
} from "./sessionChangesBus";
import type { SessionChanges } from "./types";

const CHANGES: SessionChanges = {
  sessionId: "s1",
  files: [
    {
      path: "src/02-game.js",
      status: "modified",
      additions: 8,
      deletions: 0,
      binary: false,
      truncated: false,
    },
    {
      path: "index.html",
      status: "added",
      additions: 134,
      deletions: 2,
      binary: false,
      truncated: false,
    },
  ],
  totals: { files: 2, additions: 142, deletions: 2 },
};

const EMPTY: SessionChanges = {
  sessionId: "s1",
  files: [],
  totals: { files: 0, additions: 0, deletions: 0 },
};

function jsonResponse(body: unknown) {
  return {
    ok: true,
    status: 200,
    json: async () => body,
  } as unknown as Response;
}

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  setSessionChangesEnabled(true);
  window.sessionStorage.clear();
  fetchMock = vi.fn(async () => jsonResponse(CHANGES));
  vi.stubGlobal("fetch", fetchMock);
  // Every toggle request lands a second after the last, so the dedupe of
  // near-simultaneous presses never swallows one of these tests' presses.
  let clock = 0;
  setChangesBusClockForTests(() => (clock += 1000));
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  setSessionChangesEnabled(true);
  resetChangesBusForTests();
});

type CardProps = {
  sessionId: string;
  generating: boolean;
  toolActivity: number;
  onOpenReview: (path?: string) => void;
  onOpenViewer: () => void;
};

function renderCard(overrides: Partial<CardProps> = {}) {
  const props: CardProps = {
    sessionId: "s1",
    generating: false,
    toolActivity: 0,
    onOpenReview: () => {},
    onOpenViewer: () => {},
    ...overrides,
  };
  const view = render(<SessionChangesCard {...props} />);
  return {
    update(next: Partial<CardProps>) {
      Object.assign(props, next);
      view.rerender(<SessionChangesCard {...props} />);
    },
  };
}

function pressKey(init: KeyboardEventInit): KeyboardEvent {
  const ev = new KeyboardEvent("keydown", {
    bubbles: true,
    cancelable: true,
    ...init,
  });
  act(() => {
    window.dispatchEvent(ev);
  });
  return ev;
}

const pressCtrlS = (init: KeyboardEventInit = {}) =>
  pressKey({ key: "s", code: "KeyS", ctrlKey: true, ...init });

test("the card summarises the session and lists its files", async () => {
  render(
    <SessionChangesCard
      sessionId="s1"
      generating={false}
      onOpenReview={() => {}}
      onOpenViewer={() => {}}
    />,
  );
  const card = await screen.findByTestId("session-changes-card");
  expect(card).toHaveTextContent("2 files changed");
  expect(card).toHaveTextContent("+142");
  expect(card).toHaveTextContent("−2");
  expect(screen.getByTestId("changes-row-src/02-game.js")).toHaveTextContent(
    "+8",
  );
});

test("a session that changed nothing renders no card", async () => {
  fetchMock.mockResolvedValue(jsonResponse(EMPTY));
  render(
    <SessionChangesCard
      sessionId="s1"
      generating={false}
      onOpenReview={() => {}}
      onOpenViewer={() => {}}
    />,
  );
  await waitFor(() => expect(fetchMock).toHaveBeenCalled());
  expect(screen.queryByTestId("session-changes-card")).toBeNull();
});

test("the preference hides the card and stops it fetching", async () => {
  setSessionChangesEnabled(false);
  render(
    <SessionChangesCard
      sessionId="s1"
      generating={false}
      onOpenReview={() => {}}
      onOpenViewer={() => {}}
    />,
  );
  await waitFor(() => {
    expect(screen.queryByTestId("session-changes-card")).toBeNull();
  });
  expect(fetchMock).not.toHaveBeenCalled();
});

test("outside an editor the review opens the full review window", async () => {
  const onOpenReview = vi.fn();
  const onOpenViewer = vi.fn();
  render(
    <SessionChangesCard
      sessionId="s1"
      generating={false}
      onOpenReview={onOpenReview}
      onOpenViewer={onOpenViewer}
    />,
  );
  fireEvent.click(await screen.findByTestId("changes-review"));
  expect(onOpenViewer).toHaveBeenCalledTimes(1);
  // The drawer answers about one file, so Review must not open it.
  expect(onOpenReview).not.toHaveBeenCalled();
  // Only the change set was fetched; no IDE hand-off was attempted.
  expect(
    fetchMock.mock.calls.some((c) => String(c[0]).includes("open-in-ide")),
  ).toBe(false);
});

test("the summary opens the full review window too", async () => {
  const onOpenViewer = vi.fn();
  render(
    <SessionChangesCard
      sessionId="s1"
      generating={false}
      onOpenReview={() => {}}
      onOpenViewer={onOpenViewer}
    />,
  );
  fireEvent.click(await screen.findByTestId("changes-card-open"));
  expect(onOpenViewer).toHaveBeenCalledTimes(1);
});

test("clicking a row opens that file in the side drawer", async () => {
  const onOpenReview = vi.fn();
  const onOpenViewer = vi.fn();
  render(
    <SessionChangesCard
      sessionId="s1"
      generating={false}
      onOpenReview={onOpenReview}
      onOpenViewer={onOpenViewer}
    />,
  );
  fireEvent.click(await screen.findByTestId("changes-row-index.html"));
  expect(onOpenReview).toHaveBeenCalledWith("index.html");
  expect(onOpenViewer).not.toHaveBeenCalled();
});

test("undo asks before rewriting the workspace", async () => {
  render(
    <SessionChangesCard
      sessionId="s1"
      generating={false}
      onOpenReview={() => {}}
      onOpenViewer={() => {}}
    />,
  );
  fireEvent.click(await screen.findByTestId("changes-revert"));
  expect(
    fetchMock.mock.calls.some((c) => String(c[0]).includes("/revert")),
  ).toBe(false);

  fireEvent.click(screen.getByTestId("changes-revert-confirm"));
  await waitFor(() => {
    expect(
      fetchMock.mock.calls.some((c) => String(c[0]).includes("/revert")),
    ).toBe(true);
  });
});

test("declining the confirmation leaves the workspace alone", async () => {
  render(
    <SessionChangesCard
      sessionId="s1"
      generating={false}
      onOpenReview={() => {}}
      onOpenViewer={() => {}}
    />,
  );
  fireEvent.click(await screen.findByTestId("changes-revert"));
  fireEvent.click(screen.getByTestId("changes-revert-cancel"));
  expect(screen.getByTestId("changes-revert")).toBeTruthy();
  expect(
    fetchMock.mock.calls.some((c) => String(c[0]).includes("/revert")),
  ).toBe(false);
});

// While the agent works the card would describe a change set the turn is still
// moving; it steps aside until the turn's changes are recorded.
test("the card stays hidden while the agent works", async () => {
  const card = renderCard();
  await screen.findByTestId("session-changes-card");
  card.update({ generating: true });
  expect(screen.queryByTestId("session-changes-card")).toBeNull();
});

test("after the stop the card waits for the recorded changes, then shows them", async () => {
  const card = renderCard({ generating: true });
  expect(fetchMock).not.toHaveBeenCalled();
  card.update({ generating: false });
  expect(screen.queryByTestId("session-changes-card")).toBeNull();

  act(() => emitChangesSettled("another-session"));
  expect(screen.queryByTestId("session-changes-card")).toBeNull();

  act(() => emitChangesSettled("s1"));
  expect(await screen.findByTestId("session-changes-card")).toHaveTextContent(
    "2 files changed",
  );
});

// The server can store a short turn's diff before the client has seen the
// stream end; the card must not then wait for an event that already came.
test("recorded changes announced before the stop show the card at the stop", async () => {
  const card = renderCard({ generating: true });
  act(() => emitChangesSettled("s1"));
  expect(screen.queryByTestId("session-changes-card")).toBeNull();
  card.update({ generating: false });
  expect(await screen.findByTestId("session-changes-card")).toBeTruthy();
});

test("without the announcement the card shows itself 4 s after the stop", async () => {
  vi.useFakeTimers();
  try {
    const card = renderCard({ generating: true });
    card.update({ generating: false });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3900);
    });
    expect(screen.queryByTestId("session-changes-card")).toBeNull();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(200);
    });
    expect(screen.getByTestId("session-changes-card")).toBeTruthy();
  } finally {
    vi.useRealTimers();
  }
});

test("Ctrl+S toggles the card, and the browser does not get it", async () => {
  renderCard();
  await screen.findByTestId("session-changes-card");
  const hide = pressCtrlS();
  expect(hide.defaultPrevented).toBe(true);
  expect(screen.queryByTestId("session-changes-card")).toBeNull();
  // The Russian layout reports "ы" for the same physical key.
  const show = pressCtrlS({ key: "ы" });
  expect(show.defaultPrevented).toBe(true);
  expect(await screen.findByTestId("session-changes-card")).toBeTruthy();
});

test("Cmd+S does the same on macOS, and other keys are left alone", async () => {
  renderCard();
  await screen.findByTestId("session-changes-card");
  const plain = pressKey({ key: "s", code: "KeyS" });
  const shifted = pressCtrlS({ shiftKey: true });
  expect(plain.defaultPrevented).toBe(false);
  expect(shifted.defaultPrevented).toBe(false);
  expect(screen.getByTestId("session-changes-card")).toBeTruthy();
  const cmd = pressKey({ key: "s", code: "KeyS", metaKey: true });
  expect(cmd.defaultPrevented).toBe(true);
  expect(screen.queryByTestId("session-changes-card")).toBeNull();
});

test("opened mid-turn the card shows the session so far and follows every tool call", async () => {
  const card = renderCard({ generating: true, toolActivity: 0 });
  pressCtrlS();
  expect(await screen.findByTestId("session-changes-card")).toHaveTextContent(
    "2 files changed",
  );
  const before = fetchMock.mock.calls.length;
  card.update({ toolActivity: 1 });
  await waitFor(() => expect(fetchMock.mock.calls.length).toBe(before + 1), {
    timeout: 2000,
  });
});

test("hidden mid-turn the card fetches nothing as tools finish", async () => {
  const card = renderCard({ generating: true, toolActivity: 0 });
  card.update({ toolActivity: 3 });
  await new Promise((r) => setTimeout(r, 600));
  expect(fetchMock).not.toHaveBeenCalled();
});

// Pressed with nothing to show, the key still has to answer, or it looks broken.
test("toggled open with nothing changed the card says so", async () => {
  fetchMock.mockResolvedValue(jsonResponse(EMPTY));
  renderCard({ generating: true });
  pressCtrlS();
  expect(await screen.findByTestId("changes-card-empty")).toHaveTextContent(
    "This session changed no files.",
  );
});

test("with the card switched off Ctrl+S shows nothing", async () => {
  setSessionChangesEnabled(false);
  renderCard();
  const ev = pressCtrlS();
  expect(ev.defaultPrevented).toBe(true);
  await new Promise((r) => setTimeout(r, 50));
  expect(screen.queryByTestId("session-changes-card")).toBeNull();
  expect(fetchMock).not.toHaveBeenCalled();
});

test("a refused rollback shows its error and keeps the confirmation open", async () => {
  fetchMock.mockImplementation(async (path: string) =>
    path.endsWith("/revert")
      ? {
          ok: false,
          status: 409,
          json: async () => ({
            error: { message: "a.txt changed after this turn" },
          }),
        }
      : jsonResponse(CHANGES),
  );
  renderCard();
  await screen.findByTestId("session-changes-card");
  fireEvent.click(screen.getByTestId("changes-revert"));
  fireEvent.click(screen.getByTestId("changes-revert-confirm"));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "a.txt changed after this turn",
  );
  expect(screen.getByTestId("changes-revert-confirm")).not.toBeDisabled();
});
