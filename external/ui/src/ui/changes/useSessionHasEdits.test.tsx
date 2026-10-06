import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { emitChangesSettled, resetChangesBusForTests } from "./sessionChangesBus";
import { useSessionHasEdits } from "./useSessionHasEdits";

/**
 * Whether a chat has edits decides whether its header shows the Edits button,
 * whatever the changed-files card does: the card can be hidden (Ctrl+S, the
 * end of a turn not settled yet) and then it reads nothing.
 */

let files = 0;
let fetchMock: ReturnType<typeof vi.fn>;

function changes(n: number) {
  return new Response(
    JSON.stringify({
      sessionId: "s1",
      files: Array.from({ length: n }, (_, i) => ({
        path: `f${i}.txt`,
        status: "modified",
        additions: 1,
        deletions: 0,
        binary: false,
        truncated: false,
      })),
      totals: { files: n, additions: n, deletions: 0 },
    }),
    { status: 200, headers: { "Content-Type": "application/json" } },
  );
}

beforeEach(() => {
  files = 0;
  resetChangesBusForTests();
  fetchMock = vi.fn(async () => changes(files));
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function Probe(props: { sessionId: string; enabled?: boolean }) {
  const has = useSessionHasEdits(props.sessionId, props.enabled ?? true);
  return <output data-testid="has">{String(has)}</output>;
}

test("a chat without edits has none, and the server's word that its set moved brings them", async () => {
  render(<Probe sessionId="s1" />);
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
  expect(screen.getByTestId("has").textContent).toBe("false");
  files = 2;
  await act(async () => {
    emitChangesSettled("s1");
  });
  await waitFor(() => expect(screen.getByTestId("has").textContent).toBe("true"));
  // An undo empties the set again.
  files = 0;
  await act(async () => {
    emitChangesSettled("s1");
  });
  await waitFor(() => expect(screen.getByTestId("has").textContent).toBe("false"));
});

test("another chat's settled set is not read", async () => {
  render(<Probe sessionId="s1" />);
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
  await act(async () => {
    emitChangesSettled("s2");
  });
  expect(fetchMock).toHaveBeenCalledTimes(1);
});

test("a failed read keeps what was known", async () => {
  files = 1;
  render(<Probe sessionId="s1" />);
  await waitFor(() => expect(screen.getByTestId("has").textContent).toBe("true"));
  fetchMock.mockResolvedValueOnce(new Response("down", { status: 503 }));
  await act(async () => {
    emitChangesSettled("s1");
  });
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2));
  expect(screen.getByTestId("has").textContent).toBe("true");
});

test("switched off, or without a chat, there are no edits and nothing is read", async () => {
  const { rerender } = render(<Probe sessionId="s1" enabled={false} />);
  rerender(<Probe sessionId="" />);
  await act(async () => new Promise((r) => setTimeout(r, 20)));
  expect(fetchMock).not.toHaveBeenCalled();
  expect(screen.getByTestId("has").textContent).toBe("false");
});
