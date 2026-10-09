import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  emitChangesSettled,
  resetChangesBusForTests,
} from "./sessionChangesBus";
import {
  forgetWorkingCopies,
  hasEdits,
  TOOL_ACTIVITY_DEBOUNCE_MS,
  useWorkingCopy,
} from "./workingCopy";

/**
 * What git reports for a chat's folder is read once and shared by every view
 * that shows it: git's count on the plate over the composer and the edits
 * window.
 */

let files = 0;
let vcs = "git";
let fetchMock: ReturnType<typeof vi.fn>;

function changes(n: number) {
  return new Response(
    JSON.stringify({
      object: "coddy.session_changes",
      sessionId: "s1",
      vcs,
      files: Array.from({ length: n }, (_, i) => ({
        path: `f${i}.txt`,
        status: "modified",
        additions: 2,
        deletions: 1,
        binary: false,
        truncated: false,
      })),
      totals: { files: n, additions: 2 * n, deletions: n },
      skipped: 0,
    }),
    { status: 200, headers: { "Content-Type": "application/json" } },
  );
}

beforeEach(() => {
  files = 0;
  vcs = "git";
  resetChangesBusForTests();
  forgetWorkingCopies();
  fetchMock = vi.fn(async () => changes(files));
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function Probe(props: {
  sessionId: string;
  enabled?: boolean;
  toolActivity?: number;
  testId?: string;
}) {
  const wc = useWorkingCopy(props.sessionId, {
    enabled: props.enabled ?? true,
    toolActivity: props.toolActivity ?? 0,
  });
  return (
    <output data-testid={props.testId ?? "wc"}>
      {`${wc.loaded}|${hasEdits(wc)}|${wc.changes.totals.files}|+${wc.changes.totals.additions}`}
    </output>
  );
}

test("one read serves every view of the chat", async () => {
  files = 2;
  render(
    <>
      <Probe sessionId="s1" testId="a" />
      <Probe sessionId="s1" testId="b" />
    </>,
  );
  await waitFor(() =>
    expect(screen.getByTestId("a").textContent).toBe("true|true|2|+4"),
  );
  expect(screen.getByTestId("b").textContent).toBe("true|true|2|+4");
  expect(fetchMock).toHaveBeenCalledTimes(1);
  expect(String(fetchMock.mock.calls[0]![0])).toBe(
    "/coddy/sessions/s1/changes",
  );
});

test("the server's word that a folder may have moved reads it again, whichever chat it names", async () => {
  render(<Probe sessionId="s1" />);
  await waitFor(() =>
    expect(screen.getByTestId("wc").textContent).toBe("true|false|0|+0"),
  );
  files = 3;
  // Chats share folders: another chat's turn or discard moves this one's too.
  await act(async () => {
    emitChangesSettled("s2");
  });
  await waitFor(() =>
    expect(screen.getByTestId("wc").textContent).toBe("true|true|3|+6"),
  );
  files = 1;
  await act(async () => {
    emitChangesSettled("s1");
  });
  await waitFor(() =>
    expect(screen.getByTestId("wc").textContent).toBe("true|true|1|+2"),
  );
});

test("a finished tool call reads the folder again, once for a burst", async () => {
  const { rerender } = render(<Probe sessionId="s1" toolActivity={0} />);
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
  files = 1;
  rerender(<Probe sessionId="s1" toolActivity={1} />);
  rerender(<Probe sessionId="s1" toolActivity={2} />);
  rerender(<Probe sessionId="s1" toolActivity={3} />);
  await act(
    async () =>
      new Promise((r) => setTimeout(r, TOOL_ACTIVITY_DEBOUNCE_MS + 100)),
  );
  await waitFor(() =>
    expect(screen.getByTestId("wc").textContent).toBe("true|true|1|+2"),
  );
  expect(fetchMock).toHaveBeenCalledTimes(2);
});

test("coming back to the page reads the folder again: it may have been edited elsewhere", async () => {
  render(<Probe sessionId="s1" />);
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
  files = 4;
  await act(async () => {
    window.dispatchEvent(new Event("focus"));
  });
  await waitFor(() =>
    expect(screen.getByTestId("wc").textContent).toBe("true|true|4|+8"),
  );
});

test("a failed read keeps what was known", async () => {
  files = 1;
  render(<Probe sessionId="s1" />);
  await waitFor(() =>
    expect(screen.getByTestId("wc").textContent).toBe("true|true|1|+2"),
  );
  fetchMock.mockResolvedValueOnce(new Response("down", { status: 503 }));
  await act(async () => {
    emitChangesSettled("s1");
  });
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
  expect(screen.getByTestId("wc").textContent).toBe("true|true|1|+2");
});

test("a folder in no repository has no edits, whatever it holds", async () => {
  vcs = "";
  render(<Probe sessionId="s1" />);
  await waitFor(() =>
    expect(screen.getByTestId("wc").textContent).toBe("true|false|0|+0"),
  );
});

test("switched off, or without a chat, nothing is read", async () => {
  const { rerender } = render(<Probe sessionId="s1" enabled={false} />);
  rerender(<Probe sessionId="" />);
  await act(async () => new Promise((r) => setTimeout(r, 20)));
  expect(fetchMock).not.toHaveBeenCalled();
  expect(screen.getByTestId("wc").textContent).toBe("false|false|0|+0");
});

test("another environment forgets what the last one reported", async () => {
  files = 2;
  const { unmount } = render(<Probe sessionId="s1" />);
  await waitFor(() =>
    expect(screen.getByTestId("wc").textContent).toBe("true|true|2|+4"),
  );
  unmount();
  forgetWorkingCopies();
  fetchMock.mockImplementation(() => new Promise(() => {}));
  render(<Probe sessionId="s1" />);
  expect(screen.getByTestId("wc").textContent).toBe("false|false|0|+0");
});

function ErrorProbe(props: { sessionId: string }) {
  const wc = useWorkingCopy(props.sessionId);
  return <output data-testid="err">{`${wc.loaded}|${wc.error}`}</output>;
}

test("a first read that fails says why, and the next one that answers clears it", async () => {
  fetchMock.mockResolvedValueOnce(
    new Response(JSON.stringify({ error: { message: "server restarting" } }), {
      status: 503,
      headers: { "Content-Type": "application/json" },
    }),
  );
  render(<ErrorProbe sessionId="s1" />);
  await waitFor(() =>
    expect(screen.getByTestId("err").textContent).toBe(
      "false|server restarting",
    ),
  );
  await act(async () => {
    emitChangesSettled("s1");
  });
  await waitFor(() =>
    expect(screen.getByTestId("err").textContent).toBe("true|"),
  );
});

test("new files git reports but could not list are edits too", async () => {
  fetchMock.mockImplementation(
    async () =>
      new Response(
        JSON.stringify({
          sessionId: "s1",
          vcs: "git",
          files: [],
          totals: { files: 0, additions: 0, deletions: 0 },
          skipped: 2,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
  );
  render(<Probe sessionId="s1" />);
  await waitFor(() =>
    expect(screen.getByTestId("wc").textContent).toBe("true|true|0|+0"),
  );
});

test("a view that stays mounted across another environment reads the new one", async () => {
  files = 1;
  render(<Probe sessionId="s1" />);
  await waitFor(() =>
    expect(screen.getByTestId("wc").textContent).toBe("true|true|1|+2"),
  );
  await act(async () => {
    forgetWorkingCopies();
  });
  expect(screen.getByTestId("wc").textContent).toBe("false|false|0|+0");
  files = 3;
  await act(async () => {
    emitChangesSettled("s1");
  });
  await waitFor(() =>
    expect(screen.getByTestId("wc").textContent).toBe("true|true|3|+6"),
  );
});
