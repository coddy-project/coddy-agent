import { afterEach, expect, test, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";

import {
  OperatorCommandCardLazy,
  __setOperatorCommandCardImporter,
} from "./OperatorCommandCardLazy";

afterEach(() => {
  cleanup();
  __setOperatorCommandCardImporter(null);
});

const ARGS = JSON.stringify({
  command: "make deploy",
  reason: "needs the key",
});

// A chunk that does not load (a network blip, a tab left open across an
// upgrade) must not leave an empty card: the command stays readable, the
// operator can decline it, and the chunk is asked for again.
test("a card whose chunk fails to load shows the command, Decline and Retry", async () => {
  const importer = vi
    .fn()
    .mockRejectedValueOnce(new Error("chunk"))
    .mockImplementation(() => import("./OperatorCommandCard"));
  __setOperatorCommandCardImporter(importer);
  render(
    <OperatorCommandCardLazy
      toolCallId="call_x"
      sessionId="sess_x"
      status="in_progress"
      argsText={ARGS}
      state={{
        requestId: "oc_x",
        request: {
          sessionId: "sess_x",
          requestId: "oc_x",
          toolCallId: "call_x",
          command: "make deploy",
          reason: "needs the key",
          cwd: "/w",
          shell: "bash",
          shellPath: "/bin/bash",
          host: "h",
        },
      }}
    />,
  );
  const fallback = await screen.findByTestId("operator-command-fallback");
  expect(fallback).toHaveTextContent("make deploy");
  expect(screen.getByTestId("operator-command-decline")).toBeVisible();
  await act(async () => {
    fireEvent.click(screen.getByTestId("operator-command-retry"));
  });
  expect(await screen.findByTestId("operator-command-card")).toBeVisible();
  expect(importer).toHaveBeenCalledTimes(2);
});

function failingFallback(
  state: Record<string, unknown>,
  status = "in_progress",
) {
  __setOperatorCommandCardImporter(() => Promise.reject(new Error("chunk")));
  return render(
    <OperatorCommandCardLazy
      toolCallId="call_x"
      sessionId="sess_x"
      status={status}
      argsText={JSON.stringify({ command: "rm -rf build", reason: "r" })}
      state={{
        requestId: "oc_x",
        request: {
          sessionId: "sess_x",
          requestId: "oc_x",
          toolCallId: "call_x",
          command: "make deploy",
          reason: "needs the key",
          cwd: "/w",
          shell: "bash",
          shellPath: "/bin/bash",
          host: "h",
        },
        ...state,
      }}
    />,
  );
}

// The fallback shows the command the server holds, and its Decline is a real
// answer to the request.
test("the fallback shows the request's command and Decline posts the decline", async () => {
  const posts: Array<{ url: string; body: unknown }> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      posts.push({ url, body: JSON.parse(String(init?.body ?? "null")) });
      return new Response(null, { status: 204 });
    }),
  );
  try {
    failingFallback({});
    const fallback = await screen.findByTestId("operator-command-fallback");
    expect(fallback).toHaveTextContent("make deploy");
    expect(fallback).not.toHaveTextContent("rm -rf build");
    await act(async () => {
      fireEvent.click(screen.getByTestId("operator-command-decline"));
    });
    expect(posts).toEqual([
      {
        url: "/coddy/sessions/sess_x/operator-command",
        body: { requestId: "oc_x", action: "decline" },
      },
    ]);
  } finally {
    vi.unstubAllGlobals();
  }
});

// A request this tab already answered, or a call that is over, offers no
// Decline the server would refuse.
test("the fallback offers no Decline for an answered request or an ended call", async () => {
  for (const [state, status] of [
    [{ answered: "run" }, "in_progress"],
    [{}, "cancelled"],
    [{}, "failed"],
  ] as const) {
    const { unmount } = failingFallback(state, status);
    await screen.findByTestId("operator-command-fallback");
    expect(screen.queryByTestId("operator-command-decline")).toBeNull();
    unmount();
  }
});
